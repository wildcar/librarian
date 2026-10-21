package web_test

import (
	"bytes"
	"context"
	"mime/multipart"
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
)

// form builds a multipart upload the way the page sends it: labels first.
func form(t *testing.T, fields [][2]string, files map[string]string) (string, *bytes.Buffer) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, f := range fields {
		mw.WriteField(f[0], f[1])
	}
	for name, body := range files {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(body))
	}
	mw.Close()
	return mw.FormDataContentType(), &b
}

func (b *browser) post(path, ctype string, body *bytes.Buffer) (int, string) {
	b.t.Helper()
	req, _ := http.NewRequest("POST", b.u+path, body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Origin", b.u)
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String()
}

func TestUpload(t *testing.T) {
	home := t.TempDir()
	makeAccount(t, home, "acc", "Пациент: Евтушенко Борис Васильевич")
	box := &outbox{}
	ts, reg := service(t, home, box)
	ctx := context.Background()
	reg.SetEmail(ctx, "owner@example.org", "acc", time.Now())
	b := newBrowser(t, ts.URL)
	b.do("POST", "/login", url.Values{"email": {"owner@example.org"}})
	link := regexp.MustCompile(`/login/confirm\?t=([\w-]+)`).FindStringSubmatch(box.last())
	b.do("POST", "/login/confirm", url.Values{"t": {link[1]}}, "Origin", ts.URL)

	if code, _, page := b.do("GET", "/upload", nil); code != 200 || !strings.Contains(page, "Папа (ЕБВ)") {
		t.Fatalf("upload form %d: %s", code, page)
	}
	a, _ := account.Open(home, "acc")
	stored := func() int {
		n := 0
		filepath.WalkDir(filepath.Join(a.Root(), "files"), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				n++
			}
			return nil
		})
		return n
	}

	// Bad labels: nothing is kept.
	ct, body := form(t, [][2]string{{"person", "XXX"}}, map[string]string{"a.pdf": "%PDF-1.4 x"})
	if code, page := b.post("/upload", ct, body); code != 400 || !strings.Contains(page, "человека с кодом XXX нет") || stored() != 0 {
		t.Errorf("unknown person: %d, %d stored", code, stored())
	}
	ct, body = form(t, [][2]string{{"attrs", "без знака равенства"}}, map[string]string{"a.pdf": "%PDF-1.4 x"})
	if code, _ := b.post("/upload", ct, body); code != 400 || stored() != 0 {
		t.Errorf("bad characteristic: %d", code)
	}

	// Father and a new person, a characteristic; two files, one of them twice.
	ct, body = form(t, [][2]string{{"person", "ЕБВ"}, {"new_code", "ЕСБ"}, {"new_name", "Евтушенко Сергей Борисович"},
		{"new_label", "Я"}, {"attrs", "тема = отпуск 2026\n\n"}, {"label", "билеты"}},
		map[string]string{"билет.pdf": "%PDF-1.4 ticket", "../../etc/копия.pdf": "%PDF-1.4 ticket", "заметка.md": "# note"})
	code, page := b.post("/upload", ct, body)
	if code != 200 || !strings.Contains(page, "Пачка №") || !strings.Contains(page, "уже был в библиотеке") {
		t.Fatalf("upload %d: %s", code, page)
	}
	if stored() != 2 {
		t.Errorf("stored files: %d, want 2 (one content, one note)", stored())
	}
	c, _ := catalog.Open(a.CatalogPath())
	defer c.Close()
	bs, _ := c.Batches(ctx)
	if len(bs) != 1 || bs[0].Kind != "upload" || bs[0].Label != "билеты" || bs[0].Originals != 2 ||
		strings.Join(bs[0].Persons, ",") != "ЕБВ,ЕСБ" || len(bs[0].Attrs) != 1 {
		t.Errorf("batch: %+v", bs)
	}
	ps, _ := c.Persons(ctx)
	if len(ps) != 2 || ps[1].Code != "ЕСБ" || ps[1].Label != "Я" {
		t.Errorf("persons: %+v", ps)
	}

	if code, _, page := b.do("GET", "/batches", nil); code != 200 || !strings.Contains(page, "«билеты»") || !strings.Contains(page, "Я (ЕСБ)") || !strings.Contains(page, "файлов 2") {
		t.Errorf("batches %d: %s", code, page)
	}
}
