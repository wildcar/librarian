package index_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
)

// doc adds a one-page document whose page text is text.
func doc(t *testing.T, c *catalog.Catalog, sha, text string) string {
	t.Helper()
	ctx, now := context.Background(), time.Now()
	if _, err := c.RecordSeen(ctx, catalog.Seen{URI: "file:///in/" + sha[:4], SHA256: sha, Size: 1, MTime: now, MIME: "application/pdf", At: now}); err != nil {
		t.Fatal(err)
	}
	if err := c.SavePage(ctx, catalog.Page{SHA256: sha, Page: 1, Method: "ocr", Engine: "t", Text: text, Pipeline: 1}, now); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveSplit(ctx, catalog.SplitResult{SHA256: sha, Status: catalog.SplitDone, Method: "single", Engine: "-", Pipeline: 1, Starts: []int{1}, Pages: 1}, now); err != nil {
		t.Fatal(err)
	}
	id := catalog.DocumentID(sha, 1)
	if err := c.SaveTexts(ctx, catalog.Texts{DocID: id, Status: catalog.TextsDone, Summary: "Документ.", Essential: text, Model: "t", Pipeline: 1}, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBatchInheritance(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	a, err := account.Create(t.TempDir(), "acc")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, p := range []catalog.Person{
		{Code: "ЕБВ", FullName: "Евтушенко Борис Васильевич", Label: "Папа"},
		{Code: "ЕАИ", FullName: "Евтушенко Анна Ивановна", Label: "Мама"},
	} {
		if err := c.AddPerson(ctx, p, now); err != nil {
			t.Fatal(err)
		}
	}
	sha := func(ch string) string { return strings.Repeat(ch, 64) }
	misread := doc(t, c, sha("1"), "У гр. Евтушенко П. В. 1949")       // the batch links it
	nameless := doc(t, c, sha("2"), "Памятка: приёмный покой к 11.00") // the batch links it
	stray := doc(t, c, sha("3"), "Пациент: Евтушенко Анна Ивановна")   // another person's paper in the batch
	outside := doc(t, c, sha("4"), "Чек")                              // not in the batch

	tema := catalog.Attr{Key: "тема", Value: "грыжа 2019"}
	id, err := c.CreateBatch(ctx, catalog.Batch{Kind: "folder", Persons: []string{"ЕБВ"}, Attrs: []catalog.Attr{tema}},
		[]string{sha("1"), sha("2"), sha("3")}, now)
	if err != nil {
		t.Fatal(err)
	}
	// A manual characteristic outlives the batch's.
	if err := c.SetDocAttr(ctx, nameless, catalog.Attr{Key: "тема", Value: "памятка"}, "manual"); err != nil {
		t.Fatal(err)
	}
	rep, err := index.Run(ctx, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Linked != 3 || rep.ByBatch["ЕБВ"] != 3 {
		t.Errorf("report: %+v", rep)
	}
	persons := func(id string) string {
		p, err := c.DocPersons(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(p, ",")
	}
	for id, want := range map[string]string{misread: "ЕБВ", nameless: "ЕБВ", stray: "ЕАИ,ЕБВ", outside: ""} {
		if got := persons(id); got != want {
			t.Errorf("%s persons %q, want %q", id, got, want)
		}
	}
	if r, _ := c.DocReview(ctx, stray); len(r) != 1 || !strings.HasPrefix(r[0], "человек:") {
		t.Errorf("stray review: %v", r)
	}
	if r, _ := c.DocReview(ctx, misread); len(r) != 0 {
		t.Errorf("misread review: %v", r)
	}
	at, _ := c.DocAttrs(ctx, nameless)
	if strings.Join(at["тема"], "|") != "грыжа 2019|памятка" {
		t.Errorf("nameless attrs: %v", at)
	}

	// Search by characteristic, by its absence, by batch.
	count := func(q catalog.SearchQuery) int {
		_, n, err := c.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(catalog.SearchQuery{Attrs: []catalog.Attr{tema}}); n != 3 {
		t.Errorf("by attr: %d", n)
	}
	if n := count(catalog.SearchQuery{Missing: []string{"Тема"}}); n != 1 {
		t.Errorf("without тема: %d", n)
	}
	if n := count(catalog.SearchQuery{Attrs: []catalog.Attr{{Key: "тема", Value: "памятка"}}, Missing: []string{"тема"}}); n != 2 {
		t.Errorf("тема=памятка or none: %d", n)
	}
	if n := count(catalog.SearchQuery{Batch: id}); n != 3 {
		t.Errorf("by batch: %d", n)
	}

	// Relabelling the batch reaches its documents on the next index.
	none := []string{}
	if err := c.ChangeBatch(ctx, id, catalog.BatchChange{Persons: &none, Remove: []catalog.Attr{{Key: "тема"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	if got := persons(nameless) + "/" + persons(stray); got != "/ЕАИ" {
		t.Errorf("after relabel: %q", got)
	}
	if at, _ := c.DocAttrs(ctx, nameless); strings.Join(at["тема"], "|") != "памятка" {
		t.Errorf("after relabel attrs: %v", at)
	}
	if err := c.ChangeBatch(ctx, id+1, catalog.BatchChange{}); err == nil {
		t.Error("unknown batch changed")
	}
	if _, err := c.CreateBatch(ctx, catalog.Batch{Kind: "folder", Persons: []string{"XXX"}}, nil, now); err == nil {
		t.Error("batch with an unknown person")
	}
}

func TestParseAttr(t *testing.T) {
	for in, ok := range map[string]bool{"Тема = отпуск 2026": true, "тег=": false, "=x": false, "date=2020": false, "тег": false} {
		a, err := catalog.ParseAttr(in)
		if (err == nil) != ok {
			t.Errorf("%q: %v %v", in, a, err)
		}
	}
	if a, _ := catalog.ParseAttr(" Тема = отпуск 2026 "); a.Key != "тема" || a.Value != "отпуск 2026" {
		t.Errorf("normalised: %+v", a)
	}
}
