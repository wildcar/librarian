package mcpserver_test

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/mcpserver"
	"github.com/wildcar/librarian/internal/pipeline"
)

type fakeIntake struct {
	mu     sync.Mutex
	kicks  int
	links  []string
	labels []pipeline.Labels
}

func (f *fakeIntake) Kick() { f.mu.Lock(); f.kicks++; f.mu.Unlock() }

func (f *fakeIntake) IngestLink(accountID, link string, l pipeline.Labels) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links = append(f.links, accountID+" "+link)
	f.labels = append(f.labels, l)
}

func TestAddDocument(t *testing.T) {
	home := t.TempDir()
	tok, _ := seed(t, home, "alpha", "Иванов Петр Сергеевич", "Протокол.", "Эхокардиография.")
	srv := mcpserver.New(home)
	defer srv.Close()
	in := &fakeIntake{}
	srv.SetIntake(in)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	s := connect(t, ts.URL+"/mcp", tok)

	pdf := "%PDF-1.4\nанализ крови\n%%EOF\n"
	out, isErr := call(t, s, "add_document", map[string]any{"reason": "сохрани мамин анализ",
		"name": `C:\Users\x\анализ.pdf`, "content": base64.StdEncoding.EncodeToString([]byte(pdf)),
		"persons": []string{"Pa"}, "attrs": []string{"вид=анализ"}, "label": "октябрь"})
	if isErr || out["batch"] == nil || out["known"] == true {
		t.Fatalf("file: %v %v", out, isErr)
	}
	// The same file again is known; a text note is stored as .txt.
	out, _ = call(t, s, "add_document", map[string]any{"reason": "ещё раз",
		"name": "a.pdf", "content": base64.URLEncoding.EncodeToString([]byte(pdf))})
	if out["known"] != true {
		t.Errorf("same file: %v", out)
	}
	if out, isErr = call(t, s, "add_document", map[string]any{"reason": "запиши", "name": "заметка", "text": "Давление 120/80"}); isErr {
		t.Errorf("text: %v", out)
	}
	if in.kicks != 2 {
		t.Errorf("kicks: %d", in.kicks)
	}

	out, isErr = call(t, s, "add_document", map[string]any{"reason": "сохрани папку", "link": "https://disk.yandex.ru/d/abc", "persons": []string{"Pa"}})
	if isErr || out["queued"] != true || len(in.links) != 1 || in.links[0] != "alpha https://disk.yandex.ru/d/abc" ||
		in.labels[0].Actor != "постоянный токен" || in.labels[0].Reason != "сохрани папку" {
		t.Errorf("link: %v %v %v", out, in.links, in.labels)
	}

	for name, args := range map[string]map[string]any{
		"no reason":      {"text": "x"},
		"two sources":    {"reason": "r", "text": "x", "link": "https://disk.yandex.ru/d/abc"},
		"nothing":        {"reason": "r"},
		"unknown person": {"reason": "r", "text": "x", "persons": []string{"ZZ"}},
		"bad attr":       {"reason": "r", "text": "x", "attrs": []string{"вид"}},
		"no name":        {"reason": "r", "content": "QUJD"},
		"not base64":     {"reason": "r", "name": "a.pdf", "content": "!!!"},
		"bad link":       {"reason": "r", "link": "https://example.com/x"},
	} {
		if out, isErr := call(t, s, "add_document", args); !isErr {
			t.Errorf("%s: accepted %v", name, out)
		}
	}

	a, err := account.Open(home, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.Catalog(a)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := c.Batches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var first *catalog.Batch
	for i := range bs {
		if bs[i].Label == "октябрь" {
			first = &bs[i]
		}
	}
	if len(bs) != 3 || first == nil || first.Kind != "upload" || first.Actor != "постоянный токен" ||
		first.Reason != "сохрани мамин анализ" || strings.Join(first.Persons, ",") != "Pa" || len(first.Attrs) != 1 {
		t.Errorf("batches: %+v", bs)
	}
}
