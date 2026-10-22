package web_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
)

// scanned adds a two-page PDF original from a real file, cut into two
// documents with disputed boundaries and no person; it returns the file and
// the second document.
func scanned(t *testing.T, home, id string) (string, string) {
	t.Helper()
	ctx, now := context.Background(), time.Now()
	a, err := account.Open(home, id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	file := filepath.Join(t.TempDir(), "скан.pdf")
	body := []byte("%PDF-1.4 two documents of " + id)
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	if _, err := c.RecordSeen(ctx, catalog.Seen{URI: "file://" + file, SHA256: sha, Size: int64(len(body)), MTime: now, MIME: "application/pdf", At: now}); err != nil {
		t.Fatal(err)
	}
	for p := 1; p <= 2; p++ {
		if err := c.SavePage(ctx, catalog.Page{SHA256: sha, Page: p, Method: "ocr", Engine: "t", Text: "Справка", Pipeline: 1}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SaveSplit(ctx, catalog.SplitResult{SHA256: sha, Status: catalog.SplitReview, Method: "model", Engine: "-", Pipeline: 1, Starts: []int{1, 2}, Pages: 2}, now); err != nil {
		t.Fatal(err)
	}
	for _, p := range []int{1, 2} {
		if err := c.SaveTexts(ctx, catalog.Texts{DocID: catalog.DocumentID(sha, p), Status: catalog.TextsDone,
			Summary: "Справка для бассейна.", Essential: "Справка", Model: "t", Pipeline: 1}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	return file, catalog.DocumentID(sha, 2)
}

func TestOriginalAndInbox(t *testing.T) {
	home := t.TempDir()
	makeAccount(t, home, "acc", "Пациент: Евтушенко Борис Васильевич. ЭКГ")
	makeAccount(t, home, "other", "Чужой документ")
	file, second := scanned(t, home, "acc")
	_, theirs := scanned(t, home, "other")
	box := &outbox{}
	ts, reg := service(t, home, box)
	reg.SetEmail(context.Background(), "owner@example.org", "acc", time.Now())
	b := newBrowser(t, ts.URL)
	b.do("POST", "/login", url.Values{"email": {"owner@example.org"}})
	link := regexp.MustCompile(`/login/confirm\?t=([\w-]+)`).FindStringSubmatch(box.last())
	b.do("POST", "/login/confirm", url.Values{"t": {link[1]}}, "Origin", ts.URL)

	// The document page links to the file at the document's first page.
	if code, _, page := b.do("GET", "/doc/"+second, nil); code != 200 ||
		!strings.Contains(page, `/doc/`+second+`/file#page=2"`) || !strings.Contains(page, "откроется на странице 2") {
		t.Errorf("document %d: %s", code, page)
	}
	resp, err := b.c.Get(ts.URL + "/doc/" + second + "/file")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/pdf" ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("file: %d %v", resp.StatusCode, resp.Header)
	}
	if code, _, _ := b.do("GET", "/doc/"+theirs+"/file", nil); code != http.StatusNotFound {
		t.Errorf("another account's file: %d", code)
	}
	// A file changed since intake is no longer the original.
	os.WriteFile(file, []byte("%PDF-1.4 something else"), 0o600)
	if code, _, page := b.do("GET", "/doc/"+second+"/file", nil); code != http.StatusNotFound || !strings.Contains(page, "Файл недоступен") {
		t.Errorf("changed file: %d", code)
	}

	// The inbox: both scanned documents (no person, disputed split), not the
	// linked one; a kind narrows the list.
	code, _, page := b.do("GET", "/inbox", nil)
	if code != 200 || !strings.Contains(page, `Входящие <span class="count">2</span>`) ||
		!strings.Contains(page, "человек: не указан") || !strings.Contains(page, "разбивка: границы документа не подтверждены") ||
		strings.Contains(page, "кардиолога") {
		t.Errorf("inbox %d: %s", code, page)
	}
	if _, _, page := b.do("GET", "/inbox?kind=разбивка", nil); strings.Count(page, `<a href="/doc/`) != 2 {
		t.Errorf("inbox by kind: %s", page)
	}
	if _, _, page := b.do("GET", "/inbox?kind=дата", nil); strings.Count(page, `<a href="/doc/`) != 2 {
		t.Errorf("unknown kind shows everything: %s", page)
	}
}
