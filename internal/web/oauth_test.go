package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/mcpserver"
	"github.com/wildcar/librarian/internal/pipeline"
	"github.com/wildcar/librarian/internal/users"
	"github.com/wildcar/librarian/internal/web"
)

// service runs MCP, the web and OAuth as `librarian serve` does.
func service(t *testing.T, home string, box *outbox) (*httptest.Server, *users.Registry) {
	t.Helper()
	ts, reg, _ := serviceWith(t, home, box, nil)
	return ts, reg
}

// serviceWith also gives the web a worker; its catalogs are the server's.
func serviceWith(t *testing.T, home string, box *outbox, worker *pipeline.Worker) (*httptest.Server, *users.Registry, *mcpserver.Server) {
	t.Helper()
	reg, err := users.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	m := mcpserver.New(home)
	t.Cleanup(func() { m.Close() })
	ts := httptest.NewServer(nil)
	t.Cleanup(ts.Close)
	if worker != nil {
		worker.Catalog = m.Catalog
	}
	w, err := web.New(web.Config{Registry: reg, Mail: box, Public: ts.URL, Catalog: m.Catalog,
		HasStaticToken: (*account.Account).HasToken, Worker: worker})
	if err != nil {
		t.Fatal(err)
	}
	m.SetWeb(w.Handler())
	m.SetOAuth(func(ctx context.Context, token string) (string, string, time.Time, error) {
		a, err := reg.VerifyAccess(ctx, token, time.Now())
		return a.Account, a.Assistant, a.Expires, err
	}, w.ResourceMetadataURL())
	ts.Config.Handler = m.Handler()
	return ts, reg, m
}

// TestAssistantByLetter connects the SDK's own MCP client: it discovers the
// authorization server from the 401, registers itself, and the owner allows
// it by the letter, pressing the button on another device.
func TestAssistantByLetter(t *testing.T) {
	home := t.TempDir()
	doc := makeAccount(t, home, "acc", "Пациент: Евтушенко Борис Васильевич. ЭКГ")
	box := &outbox{}
	ts, reg := service(t, home, box)
	ctx := context.Background()
	if err := reg.SetEmail(ctx, "owner@example.org", "acc", time.Now()); err != nil {
		t.Fatal(err)
	}

	// Without a token: 401 naming the metadata, which names the server.
	resp, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/mcp") {
		t.Fatalf("401: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	desk, phone := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	redirect := "http://127.0.0.1:43210/callback"
	asked := 0
	fetch := func(_ context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		if asked++; asked > 1 {
			return nil, errors.New("the owner does not allow it again")
		}
		u, err := url.Parse(args.URL)
		if err != nil {
			return nil, err
		}
		if _, _, page := desk.do("GET", u.RequestURI(), nil); !strings.Contains(page, "«Test assistant»") || !strings.Contains(page, "этот компьютер") {
			t.Errorf("authorize page: %s", page)
		}
		form := u.Query()
		form.Set("email", "owner@example.org")
		form.Set("action", "email")
		if _, _, page := desk.do("POST", "/oauth/authorize", form); !strings.Contains(page, "Test assistant") {
			t.Errorf("sent page: %s", page)
		}
		letter := box.last()
		if !strings.Contains(letter, "называет себя «Test assistant»") || !strings.Contains(letter, "этот компьютер (127.0.0.1)") {
			t.Errorf("letter: %s", letter)
		}
		link := regexp.MustCompile(`/login/confirm\?t=[\w-]+`).FindString(letter)
		if _, _, page := phone.do("GET", link, nil); !strings.Contains(page, "Разрешить доступ ассистенту?") {
			t.Errorf("confirm page: %s", page)
		}
		code, _, page := phone.do("POST", "/login/confirm", url.Values{"t": {strings.TrimPrefix(link, "/login/confirm?t=")}}, "Origin", ts.URL)
		if code != 200 || !strings.Contains(page, "Доступ разрешён") {
			t.Errorf("button on the phone: %d %s", code, page)
		}
		_, _, body := desk.do("GET", "/login/wait", nil)
		var j struct{ State, Redirect string }
		json.Unmarshal([]byte(body), &j)
		back, err := url.Parse(j.Redirect)
		if j.State != "ok" || err != nil || !strings.HasPrefix(j.Redirect, redirect) {
			return nil, errors.New("wait: " + body)
		}
		q := back.Query()
		return &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}, nil
	}
	h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "Test assistant", RedirectURIs: []string{redirect}, TokenEndpointAuthMethod: "none",
			GrantTypes: []string{"authorization_code", "refresh_token"},
		}},
		RedirectURL:              redirect,
		AuthorizationCodeFetcher: fetch,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", OAuthHandler: h}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_persons", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_persons: %v %+v", err, res)
	}
	// An edit is signed with the assistant's name.
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: "update_document",
		Arguments: map[string]any{"id": doc, "reason": "пользователь сказал", "add_attrs": []string{"тема=сердце"}}})
	if err != nil || res.IsError {
		t.Fatalf("update_document: %v %+v", err, res)
	}
	a, _ := account.Open(home, "acc")
	c, _ := catalog.Open(a.CatalogPath())
	ch, err := c.DocChanges(ctx, doc)
	c.Close()
	if err != nil || len(ch) != 1 || ch[0].Actor != "Test assistant (этот компьютер (127.0.0.1))" {
		t.Errorf("change log: %+v %v", ch, err)
	}

	// The owner sees it on «Ассистенты» and disconnects it.
	if _, err := reg.Confirm(ctx, "x", time.Now()); err == nil {
		t.Error("forged link")
	}
	login := newBrowser(t, ts.URL)
	login.do("POST", "/login", url.Values{"email": {"owner@example.org"}})
	link := regexp.MustCompile(`/login/confirm\?t=([\w-]+)`).FindStringSubmatch(box.last())
	login.do("POST", "/login/confirm", url.Values{"t": {link[1]}})
	_, _, page := login.do("GET", "/assistants", nil)
	id := regexp.MustCompile(`/assistants/(\d+)/disconnect`).FindStringSubmatch(page)
	if !strings.Contains(page, "Test assistant") || id == nil {
		t.Fatalf("assistants page: %s", page)
	}
	login.do("POST", "/assistants/"+id[1]+"/disconnect", url.Values{})
	if _, _, page := login.do("GET", "/assistants", nil); !strings.Contains(page, "Подключённых ассистентов нет") {
		t.Errorf("after disconnect: %s", page)
	}
	// The client finds its token dead and asks the owner again.
	if _, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_persons", Arguments: map[string]any{}}); err == nil || asked != 2 {
		t.Errorf("disconnected assistant: %v, asked %d times", err, asked)
	}
}

// TestAuthorizeChecks: what never reaches the owner's page, and the shortcut
// for a browser that is already logged in.
func TestAuthorizeChecks(t *testing.T) {
	home := t.TempDir()
	makeAccount(t, home, "acc", "x")
	box := &outbox{}
	ts, reg := service(t, home, box)
	ctx, now := context.Background(), time.Now()
	reg.SetEmail(ctx, "owner@example.org", "acc", now)
	c, err := reg.RegisterClient(ctx, "Claude", []string{"https://claude.ai/api/mcp/auth_callback"}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, ts.URL)
	q := url.Values{"response_type": {"code"}, "client_id": {c.ID}, "redirect_uri": {"https://evil.example/cb"},
		"code_challenge": {"abc"}, "code_challenge_method": {"S256"}, "state": {"st"}}
	if code, loc, _ := b.do("GET", "/oauth/authorize?"+q.Encode(), nil); code != 400 || loc != "" {
		t.Errorf("unregistered redirect: %d %q", code, loc)
	}
	q.Set("redirect_uri", "https://claude.ai/api/mcp/auth_callback")
	q.Set("code_challenge_method", "plain")
	if code, loc, _ := b.do("GET", "/oauth/authorize?"+q.Encode(), nil); code != 302 || !strings.Contains(loc, "error=invalid_request") || !strings.Contains(loc, "state=st") {
		t.Errorf("plain PKCE: %d %q", code, loc)
	}
	q.Set("code_challenge_method", "S256")
	q.Set("action", "deny")
	if _, loc, _ := b.do("POST", "/oauth/authorize", q); !strings.Contains(loc, "error=access_denied") {
		t.Errorf("deny: %q", loc)
	}
	// Logged in already: one button, no letter.
	b.do("POST", "/login", url.Values{"email": {"owner@example.org"}})
	link := regexp.MustCompile(`/login/confirm\?t=([\w-]+)`).FindStringSubmatch(box.last())
	b.do("POST", "/login/confirm", url.Values{"t": {link[1]}})
	letters := len(box.letters)
	if _, _, page := b.do("GET", "/oauth/authorize?"+q.Encode(), nil); !strings.Contains(page, "Вы вошли как") {
		t.Errorf("logged-in page: %s", page)
	}
	q.Set("action", "allow")
	_, loc, _ := b.do("POST", "/oauth/authorize", q)
	if !strings.HasPrefix(loc, "https://claude.ai/api/mcp/auth_callback?") || !strings.Contains(loc, "code=") || len(box.letters) != letters {
		t.Errorf("allow: %q", loc)
	}
	// Metadata.
	resp, err := http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if m["issuer"] != ts.URL || m["registration_endpoint"] != ts.URL+"/oauth/register" {
		t.Errorf("metadata: %v", m)
	}
}
