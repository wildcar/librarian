package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/pages"
	"github.com/wildcar/librarian/internal/pipeline"
	"github.com/wildcar/librarian/internal/router"
)

// A Nextcloud share with two notes, one in a subfolder.
func fakeNextcloud(t *testing.T, token string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, _, _ := r.BasicAuth(); u != token {
			w.WriteHeader(401)
			return
		}
		item := func(href string, dir bool) string {
			rt := ""
			if dir {
				rt = "<d:collection/>"
			}
			return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:resourcetype>` + rt +
				`</d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
		}
		switch {
		case r.Method == "PROPFIND" && r.URL.Path == "/public.php/webdav/":
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`+item("/public.php/webdav/", true)+item("/public.php/webdav/a.md", false)+
				item("/public.php/webdav/sub/", true)+`</d:multistatus>`)
		case r.Method == "PROPFIND" && r.URL.Path == "/public.php/webdav/sub":
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`+item("/public.php/webdav/sub/", true)+item("/public.php/webdav/sub/b.md", false)+`</d:multistatus>`)
		case r.URL.Path == "/public.php/webdav/a.md":
			fmt.Fprint(w, "# заметка a")
		case r.URL.Path == "/public.php/webdav/sub/b.md":
			fmt.Fprint(w, "# заметка b")
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCloudLink(t *testing.T) {
	pop := pages.Poppler{Dir: os.Getenv("LIBRARIAN_POPPLER_DIR")}
	home := t.TempDir()
	makeAccount(t, home, "acc", "x")
	nc := fakeNextcloud(t, "aBcD1234eFgH")
	box := &outbox{}
	worker := &pipeline.Worker{Home: home, LimitUSD: 1, Tools: pipeline.Tools{Poppler: pop,
		Model: func(context.Context) (router.Chatter, func(), error) { return nil, func() {}, context.Canceled }}}
	ts, reg, _ := serviceWith(t, home, box, worker)
	ctx := context.Background()
	reg.SetEmail(ctx, "owner@example.org", "acc", time.Now())
	b := newBrowser(t, ts.URL)
	b.do("POST", "/login", url.Values{"email": {"owner@example.org"}})
	link := regexp.MustCompile(`/login/confirm\?t=([\w-]+)`).FindStringSubmatch(box.last())
	b.do("POST", "/login/confirm", url.Values{"t": {link[1]}}, "Origin", ts.URL)

	// Not a share; files and a link at once.
	ct, body := form(t, [][2]string{{"link", "https://example.org/file.pdf"}}, nil)
	if code, page := b.post("/upload", ct, body); code != 400 || !strings.Contains(page, "такие ссылки библиотека не читает") {
		t.Errorf("unsupported link: %d", code)
	}
	ct, body = form(t, [][2]string{{"link", nc.URL + "/s/aBcD1234eFgH"}}, map[string]string{"a.pdf": "%PDF-1.4 x"})
	if code, page := b.post("/upload", ct, body); code != 400 || !strings.Contains(page, "Либо файлы, либо ссылка") {
		t.Errorf("both: %d", code)
	}

	// A labelled share: queued, downloaded into the account, one batch.
	ct, body = form(t, [][2]string{{"person", "ЕБВ"}, {"attrs", "тема = заметки"}, {"link", nc.URL + "/s/aBcD1234eFgH"}}, nil)
	if code, _ := b.post("/upload", ct, body); code != 303 {
		t.Fatalf("link: %d", code)
	}
	if _, _, page := b.do("GET", "/batches", nil); !strings.Contains(page, "В очереди на приём: ссылка") {
		t.Errorf("queued: %s", page)
	}
	worker.Pass(ctx)
	if s := worker.Status("acc"); !strings.Contains(s.LastIntake, "(Nextcloud): пачка №1, файлов 2, новых 2") {
		t.Errorf("intake: %+v", s)
	}
	a, _ := account.Open(home, "acc")
	c, _ := catalog.Open(a.CatalogPath())
	defer c.Close()
	bs, _ := c.Batches(ctx)
	if len(bs) != 1 || bs[0].Kind != "link" || bs[0].Originals != 2 || strings.Join(bs[0].Persons, ",") != "ЕБВ" {
		t.Errorf("batch: %+v", bs)
	}
	if _, _, page := b.do("GET", "/batches", nil); !strings.Contains(page, "ссылка") || !strings.Contains(page, "Последний приём: ссылка") {
		t.Errorf("batches: %s", page)
	}

	// A closed share says so.
	ct, body = form(t, [][2]string{{"link", nc.URL + "/s/WRONGtoken1"}}, nil)
	b.post("/upload", ct, body)
	worker.Pass(ctx)
	if s := worker.Status("acc"); !strings.Contains(s.LastIntake, "ссылка не открывается") {
		t.Errorf("closed: %+v", s)
	}
}
