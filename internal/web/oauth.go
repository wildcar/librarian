package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wildcar/librarian/internal/users"
)

// The service is its own OAuth 2.1 authorization server for MCP (ADR-0007):
// assistants register themselves, the owner allows them through the same
// letter as the web login, and their tokens open /mcp.

// Resource is the protected resource: the MCP endpoint.
func (s *Server) Resource() string { return s.cfg.Public + "/mcp" }

// ResourceMetadataURL is announced in the 401 of /mcp.
func (s *Server) ResourceMetadataURL() string {
	return s.cfg.Public + "/.well-known/oauth-protected-resource/mcp"
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	cors(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func preflight(w http.ResponseWriter, _ *http.Request) {
	cors(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) protectedResource(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.Resource(),
		"authorization_servers":    []string{s.cfg.Public},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Библиотека",
	})
}

func (s *Server) authServer(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.cfg.Public,
		"authorization_endpoint":                         s.cfg.Public + "/oauth/authorize",
		"token_endpoint":                                 s.cfg.Public + "/oauth/token",
		"registration_endpoint":                          s.cfg.Public + "/oauth/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_post", "client_secret_basic"},
		"authorization_response_iss_parameter_supported": true,
	})
}

// register is dynamic client registration (RFC 7591).
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string   `json:"client_name"`
		Redirects  []string `json:"redirect_uris"`
		AuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata", "error_description": "JSON expected"})
		return
	}
	method := in.AuthMethod
	switch method {
	case "":
		method = "client_secret_basic" // the RFC 7591 default
	case "none", "client_secret_post", "client_secret_basic":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata", "error_description": "unsupported token_endpoint_auth_method"})
		return
	}
	c, err := s.cfg.Registry.RegisterClient(r.Context(), in.Name, in.Redirects, method != "none", s.cfg.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_redirect_uri", "error_description": err.Error()})
		return
	}
	s.log.Printf("oauth: registered %q (%s)", c.Name, strings.Join(c.RedirectURIs, " "))
	out := map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        s.cfg.Now().Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"token_endpoint_auth_method": method,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	}
	if c.Secret != "" {
		out["client_secret"], out["client_secret_expires_at"] = c.Secret, 0
	}
	writeJSON(w, http.StatusCreated, out)
}

// authRequest reads and checks an authorization request. A bad client or
// redirect gets an error page (never a redirect to an unchecked address);
// other faults go back to the client as OAuth errors.
func (s *Server) authRequest(ctx context.Context, q url.Values) (users.AuthRequest, users.Client, string, error) {
	c, err := s.cfg.Registry.Client(ctx, q.Get("client_id"))
	if err != nil {
		return users.AuthRequest{}, users.Client{}, "", fmt.Errorf("неизвестный ассистент: пусть он подключится заново")
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	if !c.AllowsRedirect(redirect) {
		return users.AuthRequest{}, users.Client{}, "", fmt.Errorf("адрес возврата %q не зарегистрирован этим ассистентом", redirect)
	}
	req := users.AuthRequest{ClientID: c.ID, RedirectURI: redirect, State: q.Get("state"),
		Challenge: q.Get("code_challenge"), Resource: q.Get("resource"), Scope: q.Get("scope")}
	switch {
	case q.Get("response_type") != "code":
		return req, c, "unsupported_response_type", nil
	case req.Challenge == "" || q.Get("code_challenge_method") != "S256":
		return req, c, "invalid_request", nil
	case req.Resource != "" && strings.TrimRight(req.Resource, "/") != s.Resource() && strings.TrimRight(req.Resource, "/") != s.cfg.Public:
		return req, c, "invalid_target", nil
	}
	return req, c, "", nil
}

// back sends the browser to the assistant with a code or an error.
func (s *Server) back(req users.AuthRequest, params map[string]string) string {
	u, _ := url.Parse(req.RedirectURI)
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	if req.State != "" {
		q.Set("state", req.State)
	}
	q.Set("iss", s.cfg.Public)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) granted(ctx context.Context, email string, req users.AuthRequest) (string, error) {
	code, err := s.cfg.Registry.IssueCode(ctx, email, req, s.cfg.Now())
	if err != nil {
		return "", err
	}
	return s.back(req, map[string]string{"code": code}), nil
}

// sessionEmail is the e-mail of the browser's web session, if any.
func (s *Server) sessionEmail(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	email, err := s.cfg.Registry.Session(r.Context(), c.Value, s.cfg.Now())
	if err != nil {
		return ""
	}
	return email
}

func (s *Server) authorizeForm(w http.ResponseWriter, r *http.Request) {
	req, c, oauthErr, err := s.authRequest(r.Context(), r.URL.Query())
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, "message", map[string]any{"Title": "Подключение не удалось", "Text": err.Error()})
		return
	}
	if oauthErr != "" {
		http.Redirect(w, r, s.back(req, map[string]string{"error": oauthErr}), http.StatusFound)
		return
	}
	s.render(w, "authorize", map[string]any{"R": req, "Name": c.Name, "Host": users.Host(req.RedirectURI),
		"Session": s.sessionEmail(r)})
}

// authorize is the authorization page's form: allow (with a web session),
// deny, or an e-mail to send the letter to.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	req, c, oauthErr, err := s.authRequest(r.Context(), r.PostForm)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, "message", map[string]any{"Title": "Подключение не удалось", "Text": err.Error()})
		return
	}
	if oauthErr != "" {
		http.Redirect(w, r, s.back(req, map[string]string{"error": oauthErr}), http.StatusSeeOther)
		return
	}
	host := users.Host(req.RedirectURI)
	switch r.PostForm.Get("action") {
	case "deny":
		http.Redirect(w, r, s.back(req, map[string]string{"error": "access_denied"}), http.StatusSeeOther)
		return
	case "allow":
		// The web session came from a letter already: that is enough.
		email := s.sessionEmail(r)
		if email == "" {
			http.Redirect(w, r, "/oauth/authorize?"+r.PostForm.Encode(), http.StatusSeeOther)
			return
		}
		to, err := s.granted(r.Context(), email, req)
		if err != nil {
			s.fail(w, err)
			return
		}
		s.log.Printf("oauth: %s allowed %q (%s) from a web session", email, c.Name, host)
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}

	now := s.cfg.Now()
	email, err := users.NormEmail(r.PostForm.Get("email"))
	data := map[string]any{"R": req, "Name": c.Name, "Host": host, "Email": r.PostForm.Get("email")}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		data["Error"] = "Это не похоже на адрес почты."
		s.render(w, "authorize", data)
		return
	}
	ask := users.Request{Purpose: users.PurposeMCP, IP: clientIP(r), Agent: Agent(r.UserAgent()), Auth: &req}
	l, err := s.cfg.Registry.StartLogin(r.Context(), email, ask, now)
	switch {
	case errors.Is(err, users.ErrUnknown):
		s.log.Printf("oauth: unknown address from %s for %q", ask.IP, c.Name)
		s.render(w, "sent", map[string]any{"Email": email, "Unknown": true, "Assistant": c.Name})
		return
	case errors.Is(err, users.ErrTooMany):
		w.WriteHeader(http.StatusTooManyRequests)
		data["Error"] = "Писем на этот адрес было слишком много. Попробуйте через час."
		s.render(w, "authorize", data)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	subject := "Ассистент «" + c.Name + "» просит доступ к библиотеке"
	if err := s.cfg.Mail.Send(ctx, email, subject, s.assistantLetter(l, c.Name, host, ask, now)); err != nil {
		s.log.Printf("oauth letter: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		data["Error"] = "Письмо не ушло. Попробуйте позже."
		s.render(w, "authorize", data)
		return
	}
	s.set(w, waitCookie, l.Wait, users.LinkTTL)
	s.render(w, "sent", map[string]any{"Email": email, "Assistant": c.Name})
}

func (s *Server) assistantLetter(l users.Login, name, host string, req users.Request, now time.Time) string {
	site := strings.TrimPrefix(strings.TrimPrefix(s.cfg.Public, "https://"), "http://")
	return fmt.Sprintf(`Здравствуйте!

Ассистент, который называет себя «%s», просит доступ к библиотеке
%s с адресом %s. Ответ уйдёт на: %s.

Когда: %s
Откуда: IP %s, %s

С доступом ассистент сможет искать и читать ваши документы и уточнять их
(человек, характеристики, дата); его правки помечаются, пока вы их не подтвердите.

Чтобы разрешить, откройте ссылку и нажмите «Разрешить»:
%s/login/confirm?t=%s

Ссылка действует 15 минут и срабатывает один раз. Если вы не подключали
ассистента, просто удалите письмо: без нажатия кнопки доступа не будет.
Подключённых ассистентов можно отключить на странице %s/assistants.
`, name, site, l.Email, host, now.Local().Format("02.01.2006 15:04 MST"), req.IP, req.Agent,
		s.cfg.Public, l.Link, s.cfg.Public)
}

// token is the token endpoint: a code or a refresh token for tokens.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, sec, basic := r.BasicAuth()
	if !basic {
		id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	} else {
		id, _ = url.QueryUnescape(id)
		sec, _ = url.QueryUnescape(sec)
	}
	if id == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	now := s.cfg.Now()
	var t users.Tokens
	var err error
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		t, err = s.cfg.Registry.Exchange(r.Context(), r.PostForm.Get("code"), id, sec,
			r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"), now)
	case "refresh_token":
		t, err = s.cfg.Registry.Refresh(r.Context(), r.PostForm.Get("refresh_token"), id, sec, now)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	if errors.Is(err, users.ErrGrant) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	if err != nil {
		s.log.Printf("oauth token: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  t.Access,
		"token_type":    "Bearer",
		"expires_in":    int(t.ExpiresIn.Seconds()),
		"refresh_token": t.Refresh,
	})
}

// --- «Ассистенты» ------------------------------------------------------------

func (s *Server) assistants(w http.ResponseWriter, r *http.Request, v viewer) {
	cs, err := s.cfg.Registry.Connections(r.Context(), v.Acc.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "assistants", map[string]any{"V": v, "Conns": cs, "MCP": s.Resource(),
		"StaticToken": s.cfg.HasStaticToken != nil && s.cfg.HasStaticToken(v.Acc)})
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request, v viewer) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		err = s.cfg.Registry.Disconnect(r.Context(), v.Acc.ID, id, s.cfg.Now())
	}
	if err != nil && !errors.Is(err, users.ErrUnknown) {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/assistants", http.StatusSeeOther)
}
