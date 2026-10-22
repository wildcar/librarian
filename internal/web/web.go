// Package web is the owner's web interface (ADR-0007): login by a letter,
// the virtual tree of documents and the document page. Each page reads only
// the account of the session's e-mail, through the same per-account catalog
// as MCP.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/mail"
	"github.com/wildcar/librarian/internal/pipeline"
	"github.com/wildcar/librarian/internal/users"
)

//go:embed templates/*.html
var files embed.FS

const (
	sessionCookie = "lib_session"
	waitCookie    = "lib_wait"
)

// Config wires the web interface to the rest of the service.
type Config struct {
	Registry *users.Registry
	Mail     mail.Sender
	Public   string // the address people open, e.g. https://lib.wildcar.ru
	Catalog  func(*account.Account) (*catalog.Catalog, error)
	// HasStaticToken says whether the account has a token made by the CLI.
	HasStaticToken func(*account.Account) bool
	// Worker processes uploads in the background; nil shows no processing.
	Worker *pipeline.Worker
	Now    func() time.Time
	Log    io.Writer
}

// Server serves the web pages.
type Server struct {
	cfg    Config
	tmpl   *template.Template
	secure bool
	origin string
	log    *log.Logger
}

// New prepares the web interface.
func New(cfg Config) (*Server, error) {
	cfg.Public = strings.TrimRight(cfg.Public, "/")
	u, err := url.Parse(cfg.Public)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("public address %q: need e.g. https://lib.example.org", cfg.Public)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	t, err := template.New("").Funcs(template.FuncMap{
		"title": Title,
		"date":  shownDate,
		"since": dateSourceLabel,
		"stage": func(s string) string { return stageNames[s] },
		"usd":   func(f float64) string { return fmt.Sprintf("$%.2f", f) },
		"since_min": func(t time.Time) string {
			m := int(cfg.Now().Sub(t).Minutes())
			if m < 1 {
				return "меньше минуты"
			}
			return fmt.Sprintf("%d мин", m)
		},
	}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, tmpl: t, secure: u.Scheme == "https", origin: u.Scheme + "://" + u.Host,
		log: log.New(cfg.Log, "web: ", 0)}, nil
}

// Handler serves every web path.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.loginStart)
	mux.HandleFunc("GET /login/wait", s.loginWait)
	mux.HandleFunc("GET /login/confirm", s.confirmForm)
	mux.HandleFunc("POST /login/confirm", s.confirm)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /doc/{id}", s.authed(s.document))
	mux.HandleFunc("GET /upload", s.authed(s.uploadForm))
	mux.HandleFunc("POST /upload", s.authed(s.upload))
	mux.HandleFunc("GET /folder", s.authed(s.folder))
	mux.HandleFunc("POST /folder", s.authed(s.takeFolder))
	mux.HandleFunc("GET /batches", s.authed(s.batches))
	mux.HandleFunc("POST /batches/process", s.authed(s.process))
	mux.HandleFunc("GET /assistants", s.authed(s.assistants))
	mux.HandleFunc("POST /assistants/{id}/disconnect", s.authed(s.disconnect))

	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authServer)
	mux.HandleFunc("GET /.well-known/openid-configuration", s.authServer)
	mux.HandleFunc("OPTIONS /.well-known/", preflight)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("OPTIONS /oauth/register", preflight)
	mux.HandleFunc("GET /oauth/authorize", s.authorizeForm)
	mux.HandleFunc("POST /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("OPTIONS /oauth/token", preflight)
	mux.HandleFunc("GET /{$}", s.authed(s.tree))
	return s.guard(mux)
}

// guard sets the headers every page gets and refuses a form posted from
// another site (the session cookie is SameSite=Lax as well).
func (s *Server) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		// The confirm link must not leak to other sites; "no-referrer" would
		// also make browsers send "Origin: null" with forms, refused below.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		// Registration and the token endpoint are called by assistants, often
		// from their own pages; no cookie opens anything there.
		api := r.URL.Path == "/oauth/register" || r.URL.Path == "/oauth/token"
		if r.Method == http.MethodPost && !api {
			if o := r.Header.Get("Origin"); o != "" && o != s.origin {
				http.Error(w, "чужой источник запроса", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// --- session -----------------------------------------------------------------

type viewer struct {
	Email string
	Acc   *account.Account
	Cat   *catalog.Catalog
}

func (s *Server) authed(h func(http.ResponseWriter, *http.Request, viewer)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		email, err := s.cfg.Registry.Session(r.Context(), c.Value, s.cfg.Now())
		if err != nil {
			s.clear(w, sessionCookie)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		a, err := s.cfg.Registry.Account(r.Context(), email)
		if err != nil {
			s.fail(w, err)
			return
		}
		cat, err := s.cfg.Catalog(a)
		if err != nil {
			s.fail(w, err)
			return
		}
		h(w, r, viewer{Email: email, Acc: a, Cat: cat})
	}
}

func (s *Server) set(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) clear(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, email string) error {
	sess, err := s.cfg.Registry.NewSession(r.Context(), email, s.cfg.Now())
	if err != nil {
		return err
	}
	s.set(w, sessionCookie, sess, users.SessionTTL)
	return nil
}

// --- login -------------------------------------------------------------------

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "login", map[string]any{})
}

func (s *Server) loginStart(w http.ResponseWriter, r *http.Request) {
	now := s.cfg.Now()
	email, err := users.NormEmail(r.FormValue("email"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, "login", map[string]any{"Error": "Это не похоже на адрес почты.", "Email": r.FormValue("email")})
		return
	}
	req := users.Request{Purpose: users.PurposeWeb, IP: clientIP(r), Agent: Agent(r.UserAgent())}
	l, err := s.cfg.Registry.StartLogin(r.Context(), email, req, now)
	switch {
	case errors.Is(err, users.ErrUnknown):
		// The same page as for a known address, and no letter: nobody learns
		// which addresses have an account.
		s.log.Printf("login: unknown address from %s", req.IP)
		s.render(w, "sent", map[string]any{"Email": email, "Unknown": true})
		return
	case errors.Is(err, users.ErrTooMany):
		w.WriteHeader(http.StatusTooManyRequests)
		s.render(w, "login", map[string]any{"Error": "Писем на этот адрес было слишком много. Попробуйте через час.", "Email": email})
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.cfg.Mail.Send(ctx, email, "Вход в библиотеку", s.letter(l, req, now)); err != nil {
		s.log.Printf("login letter: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		s.render(w, "login", map[string]any{"Error": "Письмо не ушло. Попробуйте позже.", "Email": email})
		return
	}
	s.set(w, waitCookie, l.Wait, users.LinkTTL)
	s.render(w, "sent", map[string]any{"Email": email})
}

func (s *Server) letter(l users.Login, req users.Request, now time.Time) string {
	host := strings.TrimPrefix(strings.TrimPrefix(s.cfg.Public, "https://"), "http://")
	return fmt.Sprintf(`Здравствуйте!

Кто-то, надеемся, что вы, просит войти в веб-интерфейс библиотеки %s
с адресом %s.

Когда: %s
Откуда: IP %s, %s

Чтобы войти, откройте ссылку и нажмите «Войти»:
%s/login/confirm?t=%s

Ссылка действует 15 минут и срабатывает один раз. Если вы ничего
не запрашивали, просто удалите письмо: без нажатия кнопки никто не войдёт.
`, host, l.Email, now.Local().Format("02.01.2006 15:04 MST"), req.IP, req.Agent, s.cfg.Public, l.Link)
}

// loginWait answers the page that waits for the button in the letter; once
// pressed, this browser gets its session.
func (s *Server) loginWait(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{"state": "expired"}
	if c, err := r.Cookie(waitCookie); err == nil {
		st, tk, err := s.cfg.Registry.Take(r.Context(), c.Value, s.cfg.Now())
		if err != nil {
			s.fail(w, err)
			return
		}
		switch {
		case st == users.Waiting:
			out["state"] = "waiting"
		case st == users.Confirmed:
			to, err := s.finish(w, r, tk)
			if err != nil {
				s.fail(w, err)
				return
			}
			s.clear(w, waitCookie)
			out["state"], out["redirect"] = "ok", to
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

// finish completes a confirmed login in the browser that asked: a web
// session, or for an assistant the way back to it with a code.
func (s *Server) finish(w http.ResponseWriter, r *http.Request, tk users.Taken) (string, error) {
	if tk.Auth != nil {
		return s.granted(r.Context(), tk.Email, *tk.Auth)
	}
	return "/", s.startSession(w, r, tk.Email)
}

func (s *Server) confirmForm(w http.ResponseWriter, r *http.Request) {
	link := r.URL.Query().Get("t")
	p, err := s.cfg.Registry.Peek(r.Context(), link, s.cfg.Now())
	if err != nil {
		w.WriteHeader(http.StatusGone)
		s.render(w, "message", map[string]any{"Title": "Ссылка не действует",
			"Text": "Она устарела или уже использована. Запросите новое письмо на странице входа."})
		return
	}
	data := map[string]any{"Link": link, "P": p, "When": p.Created.Local().Format("02.01.2006 15:04")}
	if p.Auth != nil {
		c, err := s.cfg.Registry.Client(r.Context(), p.Auth.ClientID)
		if err != nil {
			s.fail(w, err)
			return
		}
		data["Assistant"], data["Host"] = c.Name, users.Host(p.Auth.RedirectURI)
	}
	s.render(w, "confirm", data)
}

// confirm is the button: the login is granted here, never on opening the
// link. The browser that pressed it is logged in too; the one that asked
// learns it on its next poll.
func (s *Server) confirm(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Registry.Confirm(r.Context(), r.FormValue("t"), s.cfg.Now())
	if err != nil {
		w.WriteHeader(http.StatusGone)
		s.render(w, "message", map[string]any{"Title": "Ссылка не действует",
			"Text": "Она устарела или уже использована. Запросите новое письмо на странице входа."})
		return
	}
	if p.Auth == nil {
		if err := s.startSession(w, r, p.Email); err != nil {
			s.fail(w, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// An assistant: the code goes to the browser that asked. When that is
	// this one, continue right here.
	if c, err := r.Cookie(waitCookie); err == nil {
		if st, tk, err := s.cfg.Registry.Take(r.Context(), c.Value, s.cfg.Now()); err == nil && st == users.Confirmed {
			to, err := s.finish(w, r, tk)
			if err != nil {
				s.fail(w, err)
				return
			}
			s.clear(w, waitCookie)
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
	}
	s.render(w, "message", map[string]any{"Title": "Доступ разрешён",
		"Text": "Вернитесь в окно, где вы вводили почту: оно само продолжит подключение ассистента."})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.cfg.Registry.EndSession(r.Context(), c.Value)
	}
	s.clear(w, sessionCookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- documents ---------------------------------------------------------------

type levelOption struct {
	Value, Label string
}

func (s *Server) tree(w http.ResponseWriter, r *http.Request, v viewer) {
	docs, err := v.Cat.Shelf(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	persons, err := v.Cat.Persons(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	labels := map[string]string{}
	for _, p := range persons {
		labels[p.Code] = p.Label
	}
	options := []levelOption{{LevelPerson, "Человек"}, {LevelYear, "Год"}}
	keys := map[string]bool{}
	for _, d := range docs {
		for k := range d.Attrs {
			keys[k] = true
		}
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		options = append(options, levelOption{k, k})
	}

	levels := []string{}
	seen := map[string]bool{}
	for _, l := range r.URL.Query()["by"] {
		if l != "" && !seen[l] && (l == LevelPerson || l == LevelYear || keys[l]) {
			levels, seen[l] = append(levels, l), true
		}
	}
	if _, asked := r.URL.Query()["by"]; !asked {
		levels = []string{LevelPerson, LevelYear}
	}
	chosen := make([]string, 3)
	copy(chosen, levels)
	s.render(w, "tree", map[string]any{
		"V": v, "Root": Build(docs, levels, labels), "Options": options, "Chosen": chosen,
	})
}

type docPage struct {
	ID, Title, Date, Partial, DateSource string
	Persons                              []string
	Attrs                                []catalog.SourcedAttr
	Batches                              []int64
	Review                               []string
	Summary, Essential                   string
	Verbatim                             []catalog.Page
	Pages                                int
}

func (s *Server) document(w http.ResponseWriter, r *http.Request, v viewer) {
	ctx, id := r.Context(), r.PathValue("id")
	d, tx, err := v.Cat.GetDocument(ctx, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		s.render(w, "message", map[string]any{"V": v, "Title": "Нет такого документа", "Text": "В вашей библиотеке нет документа " + id + "."})
		return
	}
	p := docPage{ID: d.ID, Title: Title(tx.Summary), Summary: tx.Summary, Essential: tx.Essential, Pages: d.LastPage - d.FirstPage + 1}
	if p.Date, p.Partial, p.DateSource, err = v.Cat.DocDateOf(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	persons, err := v.Cat.Persons(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	labels := map[string]string{}
	for _, pr := range persons {
		labels[pr.Code] = pr.Label
	}
	codes, err := v.Cat.DocPersons(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, c := range codes {
		p.Persons = append(p.Persons, personLabel(c, labels))
	}
	if p.Attrs, err = v.Cat.DocAttrSources(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	if p.Batches, err = v.Cat.DocBatches(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	if p.Review, err = v.Cat.DocReview(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	pages, err := v.Cat.Pages(ctx, d.SHA256)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, pg := range pages {
		if pg.Page >= d.FirstPage && pg.Page <= d.LastPage {
			p.Verbatim = append(p.Verbatim, pg)
		}
	}
	s.render(w, "doc", map[string]any{"V": v, "D": p})
}

// --- helpers -----------------------------------------------------------------

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tmpl.ExecuteTemplate(w, name+".html", data); err != nil {
		s.log.Printf("render %s: %v", name, err)
	}
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Printf("error: %v", err)
	http.Error(w, "внутренняя ошибка", http.StatusInternalServerError)
}

// clientIP is the address of the person: behind Apache, the last address it
// appended to X-Forwarded-For (earlier ones come from the client and prove
// nothing).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			parts := strings.Split(f, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
}

// Agent names the browser and system for the letter, roughly.
func Agent(ua string) string {
	browser := "неизвестный браузер"
	for _, b := range []struct{ token, name string }{
		{"YaBrowser", "Яндекс Браузер"}, {"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"},
		{"Chrome/", "Chrome"}, {"Safari/", "Safari"}, {"curl/", "curl"},
	} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	for _, o := range []struct{ token, name string }{
		{"Android", "Android"}, {"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Windows", "Windows"},
		{"Mac OS X", "macOS"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, o.token) {
			return browser + ", " + o.name
		}
	}
	return browser
}

func shownDate(date, partial string) string {
	switch {
	case date != "":
		return ruDate(date)
	case len(partial) == 7:
		return partial[5:7] + "." + partial[:4]
	case len(partial) == 4:
		return partial + " г."
	case strings.HasPrefix(partial, "--") && len(partial) == 7:
		return partial[5:7] + "." + partial[2:4] + ", год неизвестен"
	}
	return "без даты"
}

func ruDate(d string) string {
	if t, err := time.Parse("2006-01-02", d); err == nil {
		return t.Format("02.01.2006")
	}
	return d
}

func dateSourceLabel(s string) string { return catalog.DateSourceLabel(s) }
