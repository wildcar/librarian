package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/mail"
	"github.com/wildcar/librarian/internal/mcpserver"
	"github.com/wildcar/librarian/internal/users"
	"github.com/wildcar/librarian/internal/web"
)

type outbox struct {
	mu      sync.Mutex
	letters []string
}

func (o *outbox) Send(_ context.Context, to, subject, body string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.letters = append(o.letters, to+"\n"+subject+"\n"+body)
	return nil
}

func (o *outbox) last() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.letters) == 0 {
		return ""
	}
	return o.letters[len(o.letters)-1]
}

// account makes an account with one person and one dated document.
func makeAccount(t *testing.T, home, id, text string) string {
	t.Helper()
	ctx, now := context.Background(), time.Now()
	a, err := account.Create(home, id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.AddPerson(ctx, catalog.Person{Code: "ЕБВ", FullName: "Евтушенко Борис Васильевич", Label: "Папа"}, now); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat(id[:1], 64)
	if _, err := c.RecordSeen(ctx, catalog.Seen{URI: "file:///in/" + id, SHA256: sha, Size: 1, MTime: now, MIME: "application/pdf", At: now}); err != nil {
		t.Fatal(err)
	}
	if err := c.SavePage(ctx, catalog.Page{SHA256: sha, Page: 1, Method: "ocr", Engine: "t", Text: text, Pipeline: 1}, now); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveSplit(ctx, catalog.SplitResult{SHA256: sha, Status: catalog.SplitDone, Method: "single", Engine: "-", Pipeline: 1, Starts: []int{1}, Pages: 1}, now); err != nil {
		t.Fatal(err)
	}
	doc := catalog.DocumentID(sha, 1)
	if err := c.SaveTexts(ctx, catalog.Texts{DocID: doc, Status: catalog.TextsDone, Summary: "Заключение кардиолога от 02.04.2022. Жалоб нет.",
		Essential: "Суть: " + text, Model: "t", Pipeline: 1}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	return doc
}

type browser struct {
	t *testing.T
	c *http.Client
	u string
}

func newBrowser(t *testing.T, base string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, u: base, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

func (b *browser) do(method, path string, form url.Values, header ...string) (int, string, string) {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, b.u+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(out)
}

func (b *browser) wait() string {
	b.t.Helper()
	_, _, body := b.do("GET", "/login/wait", nil)
	var j struct{ State string }
	if err := json.Unmarshal([]byte(body), &j); err != nil {
		b.t.Fatalf("wait: %q", body)
	}
	return j.State
}

func TestLoginTreeAndIsolation(t *testing.T) {
	home := t.TempDir()
	ours := makeAccount(t, home, "acc", "Пациент: Евтушенко Борис Васильевич. ЭКГ")
	theirs := makeAccount(t, home, "other", "Чужой документ")

	reg, err := users.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	ctx := context.Background()
	if err := reg.SetEmail(ctx, "owner@example.org", "acc", time.Now()); err != nil {
		t.Fatal(err)
	}
	mcp := mcpserver.New(home)
	defer mcp.Close()
	box := &outbox{}
	ts := httptest.NewUnstartedServer(nil)
	ts.Start()
	defer ts.Close()
	w, err := web.New(web.Config{Registry: reg, Mail: box, Public: ts.URL, Catalog: mcp.Catalog})
	if err != nil {
		t.Fatal(err)
	}
	mcp.SetWeb(w.Handler())
	ts.Config.Handler = mcp.Handler()

	desk, phone := newBrowser(t, ts.URL), newBrowser(t, ts.URL)
	if code, loc, _ := desk.do("GET", "/", nil); code != http.StatusSeeOther || loc != "/login" {
		t.Fatalf("anonymous /: %d %q", code, loc)
	}
	// MCP stays behind its token.
	if code, _, _ := desk.do("POST", "/mcp", url.Values{}); code != http.StatusUnauthorized {
		t.Errorf("/mcp without a token: %d", code)
	}

	// An unknown address: the same page, no letter.
	_, _, page := desk.do("POST", "/login", url.Values{"email": {"stranger@example.org"}})
	if !strings.Contains(page, "Проверьте почту") || box.last() != "" {
		t.Errorf("stranger: letter %q", box.last())
	}
	// A form from another site.
	if code, _, _ := desk.do("POST", "/login", url.Values{"email": {"owner@example.org"}}, "Origin", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("foreign origin: %d", code)
	}

	desk.do("POST", "/login", url.Values{"email": {"Owner@Example.org"}}, "User-Agent", "Mozilla/5.0 (Windows NT 10.0) Firefox/140.0")
	letter := box.last()
	if !strings.Contains(letter, "owner@example.org") || !strings.Contains(letter, "Firefox, Windows") {
		t.Errorf("letter: %q", letter)
	}
	link := regexp.MustCompile(`/login/confirm\?t=[\w-]+`).FindString(letter)
	if link == "" {
		t.Fatalf("no link in %q", letter)
	}
	if st := desk.wait(); st != "waiting" {
		t.Errorf("before the button: %s", st)
	}
	// Opening the link (a mail scanner, say) grants nothing.
	if _, _, page := phone.do("GET", link, nil); !strings.Contains(page, "Войти в библиотеку?") {
		t.Errorf("confirm page: %q", page)
	}
	if st := desk.wait(); st != "waiting" {
		t.Errorf("after opening the link: %s", st)
	}
	tok := strings.TrimPrefix(link, "/login/confirm?t=")
	// The button is posted the way a browser does it: with the site's own
	// Origin, which the page's referrer policy must let through.
	resp, err := http.Get(ts.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if p := resp.Header.Get("Referrer-Policy"); p == "no-referrer" || p == "" {
		t.Errorf("referrer policy %q makes browsers send Origin: null", p)
	}
	if code, _, _ := phone.do("POST", "/login/confirm", url.Values{"t": {"x"}}, "Origin", "null"); code != http.StatusForbidden {
		t.Errorf("Origin null: %d", code)
	}
	if code, loc, _ := phone.do("POST", "/login/confirm", url.Values{"t": {tok}}, "Origin", ts.URL); code != http.StatusSeeOther || loc != "/" {
		t.Fatalf("button: %d %q", code, loc)
	}
	if code, _, _ := phone.do("POST", "/login/confirm", url.Values{"t": {tok}}); code != http.StatusGone {
		t.Errorf("button twice: %d", code)
	}
	if st := desk.wait(); st != "ok" {
		t.Fatalf("after the button: %s", st)
	}
	if st := desk.wait(); st != "expired" {
		t.Errorf("wait after login: %s", st)
	}

	// Both browsers see the tree; a level that does not exist is ignored.
	for _, b := range []*browser{desk, phone} {
		code, _, page := b.do("GET", "/?by=person&by=year&by=nonsense", nil)
		if code != 200 || !strings.Contains(page, "Папа (ЕБВ)") || !strings.Contains(page, "2022") ||
			!strings.Contains(page, "Заключение кардиолога от 02.04.2022") || strings.Contains(page, "Чужой") {
			t.Errorf("tree %d: %s", code, page)
		}
	}
	// Each chosen chip links to the grouping without it, so a click removes it.
	if _, _, page := desk.do("GET", "/", nil); !strings.Contains(page, `href="/?by=year" data-v="person"`) ||
		!strings.Contains(page, `href="/?by=person" data-v="year"`) {
		t.Errorf("default grouping chips: %s", page)
	}
	if _, _, page := desk.do("GET", "/?by=", nil); !strings.Contains(page, `href="/?by=person" data-v="person"`) {
		t.Errorf("empty grouping: %s", page)
	}
	code, _, page := desk.do("GET", "/doc/"+ours, nil)
	if code != 200 || !strings.Contains(page, "Суть: Пациент") || !strings.Contains(page, "02.04.2022") || !strings.Contains(page, "— /in/") {
		t.Errorf("document %d: %s", code, page)
	}
	if code, _, _ := desk.do("GET", "/doc/"+theirs, nil); code != http.StatusNotFound {
		t.Errorf("another account's document: %d", code)
	}

	// Logging out ends the session.
	desk.do("POST", "/logout", url.Values{})
	if code, _, _ := desk.do("GET", "/", nil); code != http.StatusSeeOther {
		t.Errorf("after logout: %d", code)
	}
}

func TestCompose(t *testing.T) {
	msg, err := mail.Compose("library@wildcar.ru", "owner@example.org", "Вход в библиотеку", "Ссылка:\nhttps://x/login", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	for _, want := range []string{"Subject: =?utf-8?q?", "Content-Type: text/plain; charset=utf-8", "Message-ID: <", "@wildcar.ru>", "\r\n\r\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in %s", want, s)
		}
	}
	if _, err := mail.Compose("a@b", "x@y\r\nBcc: z@w", "s", "b", time.Now()); err == nil {
		t.Error("header injection accepted")
	}
}
