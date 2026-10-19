package split_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/router"
	"github.com/wildcar/librarian/internal/split"
)

// fakeModel answers each call with the next reply in line.
type fakeModel struct {
	mu      sync.Mutex
	replies []string
	calls   []string // prompts seen
}

func (f *fakeModel) Chat(ctx context.Context, r router.ChatRequest) (router.ChatReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Text)
	if len(f.replies) == 0 {
		return router.ChatReply{}, errors.New("no reply scripted")
	}
	rep := f.replies[0]
	f.replies = f.replies[1:]
	return router.ChatReply{Text: rep, Model: r.Model, CostUSD: 0.01}, nil
}

func newCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	a, err := account.Create(t.TempDir(), "family")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// addOriginal stores an original whose page text is complete.
func addOriginal(t *testing.T, c *catalog.Catalog, name string, pages ...string) string {
	t.Helper()
	ctx := context.Background()
	sha := fmt.Sprintf("%064x", len(name)*7919+len(pages))
	sha = strings.Repeat(string(name[0]), 8) + sha[8:]
	now := time.Now()
	if _, err := c.RecordSeen(ctx, catalog.Seen{URI: "file:///" + name, SHA256: sha, Size: 1, MTime: now, MIME: "application/pdf", At: now}); err != nil {
		t.Fatal(err)
	}
	for i, p := range pages {
		if err := c.SavePage(ctx, catalog.Page{SHA256: sha, Page: i + 1, Method: "ocr", Engine: "test", Text: p, Pipeline: 1}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SetExtraction(ctx, sha, len(pages), catalog.ExtractDone, "", now); err != nil {
		t.Fatal(err)
	}
	return sha
}

const (
	protocol    = "СМ-Клиника\nПротокол обследования\nПациент: пациент\nЗаключение: норма"
	protocolEnd = "Заключение: без патологии.\nПодпись врача: врач\nДата: 02.04.2022"
	recommend   = "СМ-Клиника\nРЕКОМЕНДАЦИИ ПО ЛЕЧЕНИЮ\nПациент: пациент"
	middle      = "продолжение текста протокола без шапки"
)

func docs(t *testing.T, c *catalog.Catalog, sha string) string {
	t.Helper()
	ds, err := c.Documents(context.Background(), sha)
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, d := range ds {
		parts = append(parts, fmt.Sprintf("%d-%d", d.FirstPage, d.LastPage))
	}
	return strings.Join(parts, " ")
}

func TestSplitAgreeDisagreeSingle(t *testing.T) {
	ctx := context.Background()
	c := newCatalog(t)
	single := addOriginal(t, c, "a-single.pdf", protocol)
	agree := addOriginal(t, c, "b-agree.pdf", protocol, protocolEnd, recommend, middle)
	disagree := addOriginal(t, c, "c-disagree.pdf", protocol, protocolEnd, recommend)
	m := &fakeModel{replies: []string{`{"starts": [1, 3]}`, `{"starts": [1]}`}}
	st := &split.Stage{Catalog: c, Model: m}
	rep, err := st.Run(ctx, split.Options{Account: "family"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Originals != 3 || rep.Single != 1 || rep.Review != 1 || rep.Failed != 0 || len(m.calls) != 2 {
		t.Fatalf("report %+v, calls %d", rep, len(m.calls))
	}
	if got := docs(t, c, single); got != "1-1" {
		t.Errorf("single: %s", got)
	}
	if got := docs(t, c, agree); got != "1-2 3-4" {
		t.Errorf("agree: %s", got)
	}
	// The model's cut is kept, the disagreement goes to the owner.
	if got := docs(t, c, disagree); got != "1-3" {
		t.Errorf("disagree: %s", got)
	}
	if !strings.Contains(strings.Join(rep.Notes, " "), "p2|p3") {
		t.Errorf("notes do not name the boundary: %v", rep.Notes)
	}
	if !strings.Contains(m.calls[0], "===== Страница 4 =====") {
		t.Error("prompt does not number pages")
	}
	again, _ := st.Run(ctx, split.Options{Account: "family"})
	if again.Originals != 0 {
		t.Errorf("second run found %d originals", again.Originals)
	}
}

func TestBadReplyIsRetried(t *testing.T) {
	ctx := context.Background()
	c := newCatalog(t)
	sha := addOriginal(t, c, "scan.pdf", protocol, protocolEnd, recommend)
	m := &fakeModel{replies: []string{"Не могу разобрать", `{"starts": [1, 3]}`}}
	st := &split.Stage{Catalog: c, Model: m}
	rep, _ := st.Run(ctx, split.Options{Account: "family"})
	if rep.Failed != 1 || docs(t, c, sha) != "" {
		t.Fatalf("bad reply: %+v, docs %q", rep, docs(t, c, sha))
	}
	rep, _ = st.Run(ctx, split.Options{Account: "family"})
	if rep.Failed != 0 || docs(t, c, sha) != "1-2 3-3" {
		t.Errorf("retry: %+v, docs %q", rep, docs(t, c, sha))
	}
	ds, _ := c.Documents(ctx, sha)
	if ds[0].ID != catalog.DocumentID(sha, 1) || ds[1].ID != catalog.DocumentID(sha, 3) {
		t.Errorf("ids: %s %s", ds[0].ID, ds[1].ID)
	}
}

func TestWindows(t *testing.T) {
	ctx := context.Background()
	c := newCatalog(t)
	// 7 pages, window 3: calls for 1-3 and 4-6, page 7 is a one-page tail.
	// Seams 3|4 and 6|7 are decided by the rules: 3|4 a title after an
	// ending (new), 6|7 a lowercase continuation (same).
	sha := addOriginal(t, c, "long.pdf", protocol, middle, protocolEnd, recommend, middle, middle, middle)
	m := &fakeModel{replies: []string{`{"starts": [1]}`, `{"starts": [1]}`}}
	st := &split.Stage{Catalog: c, Model: m}
	rep, err := st.Run(ctx, split.Options{Account: "family", Window: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.calls) != 2 || rep.Failed != 0 {
		t.Fatalf("calls %d, report %+v", len(m.calls), rep)
	}
	if got := docs(t, c, sha); got != "1-3 4-7" {
		t.Errorf("windows: %s", got)
	}
	if strings.Contains(m.calls[1], "===== Страница 4 =====") {
		t.Error("windows should number pages from 1")
	}
}
