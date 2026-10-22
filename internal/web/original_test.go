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
	// The second reading doubts page 2; an assistant refined the second document.
	if err := c.SaveCheck(ctx, sha, 2, catalog.PageCheck{Engine: "tesseract", Words: 10, Matched: 5, Flagged: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	doc := catalog.DocumentID(sha, 2)
	if err := c.ChangeDocument(ctx, doc, catalog.DocChange{Attrs: []catalog.Attr{{Key: "тема", Value: "бассейн"}}, Actor: "Claude"},
		catalog.OriginAssistant, "это для бассейна", now); err != nil {
		t.Fatal(err)
	}
	if err := index.Doc(ctx, c, doc, now); err != nil {
		t.Fatal(err)
	}
	return file, doc
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

	// The document page links to each file at the document's first page and
	// offers a button for every check the owner can settle.
	code, _, page := b.do("GET", "/doc/"+second, nil)
	for _, want := range []string{`/doc/` + second + `/file?n=0#page=2"`, "файл откроется на странице 2", "Границы верны",
		"Прочитано верно", `/file#page=2"`, "Принять", "Отменить", "правка: Claude: «это для бассейна»", "тема: бассейн"} {
		if code != 200 || !strings.Contains(page, want) {
			t.Errorf("document %d: no %q in %s", code, want, page)
		}
	}
	resp, err := b.c.Get(ts.URL + "/doc/" + second + "/file?n=0")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/pdf" ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("file: %d %v", resp.StatusCode, resp.Header)
	}
	if code, _, _ := b.do("GET", "/doc/"+second+"/file?n=3", nil); code != http.StatusNotFound {
		t.Errorf("a file that is not there: %d", code)
	}
	if code, _, _ := b.do("GET", "/doc/"+theirs+"/file", nil); code != http.StatusNotFound {
		t.Errorf("another account's file: %d", code)
	}

	// The inbox: both scanned documents (no person, disputed split), not the
	// linked one; a kind narrows the list.
	code, _, page = b.do("GET", "/inbox", nil)
	if code != 200 || !strings.Contains(page, `Входящие <span class="count">2</span>`) ||
		!strings.Contains(page, "человек: не указан") || !strings.Contains(page, "разбивка: границы документа не подтверждены") ||
		!strings.Contains(page, "правка: Claude") || strings.Contains(page, "кардиолога") {
		t.Errorf("inbox %d: %s", code, page)
	}
	if _, _, page := b.do("GET", "/inbox?kind=разбивка", nil); strings.Count(page, `<span class="d">`) != 2 {
		t.Errorf("inbox by kind: %s", page)
	}
	if _, _, page := b.do("GET", "/inbox?kind=дата", nil); strings.Count(page, `<span class="d">`) != 2 {
		t.Errorf("unknown kind shows everything: %s", page)
	}

	// The buttons: the split holds for both documents of the file, the page
	// was read right, the assistant's refinement is reverted.
	settle := func(form url.Values, origin string) (int, string) {
		code, loc, _ := b.do("POST", "/doc/"+second+"/settle", form, "Origin", origin)
		return code, loc
	}
	if code, _ := settle(url.Values{"fix": {"split"}}, "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("foreign origin: %d", code)
	}
	if code, loc := settle(url.Values{"fix": {"split"}, "back": {"/inbox?kind=разбивка"}}, ts.URL); code != http.StatusSeeOther || !strings.HasPrefix(loc, "/inbox?kind=%d1%80") {
		t.Errorf("split: %d %q", code, loc)
	}
	if code, loc := settle(url.Values{"fix": {"page"}, "page": {"1"}, "back": {"//evil.example/"}}, ts.URL); code != http.StatusSeeOther || loc != "/doc/"+second {
		t.Errorf("page: %d %q", code, loc)
	}
	settle(url.Values{"fix": {"assistant"}, "revert": {"1"}}, ts.URL)
	if code, _ := settle(url.Values{"fix": {"nonsense"}}, ts.URL); code != http.StatusBadRequest {
		t.Errorf("unknown fix: %d", code)
	}
	if code, _, _ := b.do("POST", "/doc/"+theirs+"/settle", url.Values{"fix": {"split"}}, "Origin", ts.URL); code != http.StatusNotFound {
		t.Errorf("another account's document: %d", code)
	}
	_, _, page = b.do("GET", "/inbox", nil)
	for _, gone := range []string{"разбивка", "распознавание", "правка ассистента"} {
		if strings.Contains(page, gone) {
			t.Errorf("%s still in the inbox: %s", gone, page)
		}
	}
	if _, _, page := b.do("GET", "/doc/"+second, nil); strings.Contains(page, "тема: бассейн") {
		t.Errorf("reverted characteristic stays: %s", page)
	}
	// A new split is a new decision: the confirmation does not carry over.
	a, _ := account.Open(home, "acc")
	c, _ := catalog.Open(a.CatalogPath())
	defer c.Close()
	sha := strings.SplitN(second, "-", 2)[0]
	body, _ := os.ReadFile(file)
	sum := sha256.Sum256(body)
	full := hex.EncodeToString(sum[:])
	if !strings.HasPrefix(full, sha) {
		t.Fatalf("sha %s for %s", full, sha)
	}
	c.SaveSplit(context.Background(), catalog.SplitResult{SHA256: full, Status: catalog.SplitReview, Method: "model", Engine: "-", Pipeline: 2, Starts: []int{1, 2}, Pages: 2}, time.Now())
	if r, _ := c.DocReview(context.Background(), second); !strings.Contains(strings.Join(r, ";"), "разбивка") {
		t.Errorf("after a new split: %v", r)
	}

	// A file changed since intake is no longer the original.
	os.WriteFile(file, []byte("%PDF-1.4 something else"), 0o600)
	if code, _, page := b.do("GET", "/doc/"+second+"/file", nil); code != http.StatusNotFound || !strings.Contains(page, "Файл недоступен") {
		t.Errorf("changed file: %d", code)
	}
}

func TestEditFromPage(t *testing.T) {
	home := t.TempDir()
	makeAccount(t, home, "acc", "Пациент: Евтушенко Борис Васильевич. ЭКГ")
	_, second := scanned(t, home, "acc")
	first := strings.TrimSuffix(second, "-2") + "-1"
	box := &outbox{}
	ts, reg := service(t, home, box)
	reg.SetEmail(context.Background(), "owner@example.org", "acc", time.Now())
	b := newBrowser(t, ts.URL)
	b.do("POST", "/login", url.Values{"email": {"owner@example.org"}})
	link := regexp.MustCompile(`/login/confirm\?t=([\w-]+)`).FindStringSubmatch(box.last())
	b.do("POST", "/login/confirm", url.Values{"t": {link[1]}}, "Origin", ts.URL)
	post := func(path string, form url.Values) (int, string, string) {
		return b.do("POST", path, form, "Origin", ts.URL)
	}

	// The inbox leads to the open form.
	if _, _, page := b.do("GET", "/inbox", nil); !strings.Contains(page, `href="/doc/`+second+`?edit=person#person"`) {
		t.Errorf("no person link in the inbox: %s", page)
	}
	if _, _, page := b.do("GET", "/doc/"+second+"?edit=person", nil); !strings.Contains(page, `id="person" open`) {
		t.Errorf("person form closed: %s", page)
	}

	// Persons: one ticked; nobody for the other document; a stranger refused.
	if code, loc, _ := post("/doc/"+second+"/persons", url.Values{"person": {"ЕБВ"}, "reason": {"папин"}}); code != http.StatusSeeOther || loc != "/doc/"+second {
		t.Fatalf("persons: %d %q", code, loc)
	}
	if _, _, page := b.do("GET", "/doc/"+second, nil); !strings.Contains(page, "<dd>Папа (ЕБВ)") {
		t.Errorf("person not shown: %s", page)
	}
	post("/doc/"+first+"/persons", url.Values{})
	if _, _, page := b.do("GET", "/doc/"+first, nil); !strings.Contains(page, "ни к кому не относится") {
		t.Errorf("nobody's document: %s", page)
	}
	if _, _, page := b.do("GET", "/inbox", nil); strings.Contains(page, "человек: не указан") {
		t.Errorf("decided persons still in the inbox: %s", page)
	}
	if code, _, page := post("/doc/"+second+"/persons", url.Values{"person": {"XXX"}}); code != http.StatusBadRequest || !strings.Contains(page, "Такого человека нет") {
		t.Errorf("stranger: %d", code)
	}

	// Date: a wrong one keeps what was typed; a right one is the owner's;
	// none; back to the library's.
	code, _, page := post("/doc/"+second+"/date", url.Values{"date": {"31.02.2022"}})
	if code != http.StatusBadRequest || !strings.Contains(page, "Такой даты нет") || !strings.Contains(page, `value="31.02.2022"`) || !strings.Contains(page, `id="date" open`) {
		t.Errorf("wrong date: %d %s", code, page)
	}
	post("/doc/"+second+"/date", url.Values{"date": {"12.09.2021"}})
	if _, _, page := b.do("GET", "/doc/"+second, nil); !strings.Contains(page, "12.09.2021") || !strings.Contains(page, "уточнена владельцем") {
		t.Errorf("date not set: %s", page)
	}
	post("/doc/"+second+"/date", url.Values{"date": {"12.09.2021"}, "none": {"1"}})
	if _, _, page := b.do("GET", "/doc/"+second, nil); !strings.Contains(page, "без даты") || strings.Contains(page, "12.09.2021") || !strings.Contains(page, "Вернуть дату") {
		t.Errorf("no date: %s", page)
	}
	post("/doc/"+second+"/date", url.Values{"clear": {"1"}})
	if _, _, page := b.do("GET", "/doc/"+second, nil); strings.Contains(page, "уточнена владельцем") || strings.Contains(page, "Вернуть дату") {
		t.Errorf("date not given back: %s", page)
	}
}
