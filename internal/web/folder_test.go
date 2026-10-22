package web_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
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

func TestServerFolder(t *testing.T) {
	pop := pages.Poppler{Dir: os.Getenv("LIBRARIAN_POPPLER_DIR")}
	if err := pop.Check(); err != nil {
		t.Skipf("poppler not available: %v", err)
	}
	home := t.TempDir()
	makeAccount(t, home, "acc", "x")
	src := t.TempDir()
	docs := filepath.Join(src, "Документы")
	for _, d := range []string{filepath.Join(docs, "Здоровье", "2026"), filepath.Join(src, "Чужое")} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(docs, "Здоровье", "заметка.md"), []byte("# анализы"), 0o644)
	os.WriteFile(filepath.Join(docs, "Здоровье", "2026", "план.md"), []byte("# план"), 0o644)
	os.WriteFile(filepath.Join(src, "Чужое", "секрет.md"), []byte("# нет"), 0o644)

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

	if _, _, page := b.do("GET", "/folder", nil); !strings.Contains(page, "не разрешил ни одной папки") {
		t.Errorf("no roots: %s", page)
	}
	a, _ := account.Open(home, "acc")
	if _, err := a.AddRoot(docs); err != nil {
		t.Fatal(err)
	}
	if _, _, page := b.do("GET", "/folder?path="+url.QueryEscape(docs), nil); !strings.Contains(page, "Здоровье/") || strings.Contains(page, "Чужое") {
		t.Errorf("root listing: %s", page)
	}
	for _, out := range []string{filepath.Join(src, "Чужое"), docs + "/../Чужое", "/etc"} {
		if code, _, _ := b.do("GET", "/folder?path="+url.QueryEscape(out), nil); code != 403 {
			t.Errorf("%s: %d", out, code)
		}
		if code, _, _ := b.do("POST", "/folder", url.Values{"path": {out}}, "Origin", ts.URL); code != 403 {
			t.Errorf("take %s: %d", out, code)
		}
	}

	health := filepath.Join(docs, "Здоровье")
	form := url.Values{"path": {health}, "new_code": {"ЕСБ"}, "new_name": {"Евтушенко Сергей Борисович"}, "new_label": {"Я"},
		"attrs": {"тема = здоровье"}}
	if code, loc, _ := b.do("POST", "/folder", form, "Origin", ts.URL); code != 303 || loc != "/batches" {
		t.Fatalf("take: %d %q", code, loc)
	}
	if _, _, page := b.do("GET", "/batches", nil); !strings.Contains(page, "В очереди на приём") {
		t.Errorf("queued: %s", page)
	}
	worker.Pass(ctx)
	if s := worker.Status("acc"); !strings.Contains(s.LastIntake, "пачка №1, файлов 2, новых 2") {
		t.Errorf("intake: %+v", s)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	bs, _ := c.Batches(ctx)
	if len(bs) != 1 || bs[0].Kind != "folder" || strings.Join(bs[0].Persons, ",") != "ЕСБ" || len(bs[0].Attrs) != 1 {
		t.Errorf("batch: %+v", bs)
	}
	// The same folder again without labels: nothing new, no batch.
	b.do("POST", "/folder", url.Values{"path": {health}}, "Origin", ts.URL)
	worker.Pass(ctx)
	if s := worker.Status("acc"); !strings.Contains(s.LastIntake, "новых файлов нет") {
		t.Errorf("again: %+v", s)
	}
}
