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

func TestManualRefinement(t *testing.T) {
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
		{Code: "ЕБВ", FullName: "Евтушенко Борис Васильевич"},
		{Code: "ЕАИ", FullName: "Евтушенко Анна Ивановна"},
	} {
		if err := c.AddPerson(ctx, p, now); err != nil {
			t.Fatal(err)
		}
	}
	sha := strings.Repeat("5", 64)
	id := doc(t, c, sha, "Пациент: Евтушенко Борис Васильевич. Направление от 12.09.2026")
	if _, err := c.CreateBatch(ctx, catalog.Batch{Kind: "folder", Persons: []string{"ЕБВ"},
		Attrs: []catalog.Attr{{Key: "тема", Value: "сердце"}, {Key: "тег", Value: "старое"}}}, []string{sha}, now); err != nil {
		t.Fatal(err)
	}
	reindex := func() {
		t.Helper()
		if _, err := index.Run(ctx, c, now); err != nil {
			t.Fatal(err)
		}
	}
	reindex()

	// The owner: this is mother's paper, about the kidneys, with no "тег", dated 2026-09.
	month := "2026-09"
	err = c.ChangeDocument(ctx, id, catalog.DocChange{
		Link: []string{"ЕАИ"}, Unlink: []string{"ЕБВ"},
		Attrs: []catalog.Attr{{Key: "тема", Value: "почки"}},
		Unset: []catalog.Attr{{Key: "тема", Value: "сердце"}, {Key: "тег"}},
		Date:  &month,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	reindex()
	reindex() // index runs any number of times; the refinement holds
	if p, _ := c.DocPersons(ctx, id); strings.Join(p, ",") != "ЕАИ" {
		t.Errorf("persons %v: the name in the text and the batch must not bring ЕБВ back", p)
	}
	if at, _ := c.DocAttrs(ctx, id); len(at) != 1 || strings.Join(at["тема"], ",") != "почки" {
		t.Errorf("attrs %v", at)
	}
	if d, p, s, _ := c.DocDateOf(ctx, id); d != "" || p != "2026-09" || s != "manual" {
		t.Errorf("date %q %q %q", d, p, s)
	}
	if r, _ := c.DocReview(ctx, id); len(r) != 0 {
		t.Errorf("review %v", r)
	}

	// Taking it back: ЕБВ linked again, the manual date forgotten, no date at all on request.
	if err := c.ChangeDocument(ctx, id, catalog.DocChange{Link: []string{"ЕБВ"}, ClearDate: true}, now); err != nil {
		t.Fatal(err)
	}
	reindex()
	if p, _ := c.DocPersons(ctx, id); strings.Join(p, ",") != "ЕАИ,ЕБВ" {
		t.Errorf("persons after relink %v", p)
	}
	if d, _, s, _ := c.DocDateOf(ctx, id); d != "" || s != "" {
		t.Errorf("date after clear %q %q (the summary has none)", d, s)
	}
	none := ""
	if err := c.ChangeDocument(ctx, id, catalog.DocChange{Date: &none}, now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"2026-13", "12.09.2026", "--02-30"} {
		if err := c.ChangeDocument(ctx, id, catalog.DocChange{Date: &bad}, now); err == nil {
			t.Errorf("date %q accepted", bad)
		}
	}
	if err := c.ChangeDocument(ctx, "nope-1", catalog.DocChange{}, now); err == nil {
		t.Error("unknown document changed")
	}
	if err := c.ChangeDocument(ctx, id, catalog.DocChange{Link: []string{"XXX"}}, now); err == nil {
		t.Error("unknown person linked")
	}
}
