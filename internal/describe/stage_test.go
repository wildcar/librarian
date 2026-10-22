package describe_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/describe"
	"github.com/wildcar/librarian/internal/router"
)

// fakeModel answers in turn; it records the prompts.
type fakeModel struct {
	mu      sync.Mutex
	answers []string
	prompts []string
}

func (f *fakeModel) Chat(_ context.Context, r router.ChatRequest) (router.ChatReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, r.Text)
	a := f.answers[0]
	if len(f.answers) > 1 {
		f.answers = f.answers[1:]
	}
	return router.ChatReply{Text: a, CostUSD: 0.001}, nil
}

func TestDescribeChecksLeaks(t *testing.T) {
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
	sha := strings.Repeat("a", 64)
	if _, err := c.RecordSeen(ctx, catalog.Seen{URI: "file:///in/r.pdf", SHA256: sha, Size: 1, MTime: now, MIME: "application/pdf", At: now}); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveSplit(ctx, catalog.SplitResult{SHA256: sha, Status: catalog.SplitDone, Method: "single", Engine: "-", Pipeline: 1, Starts: []int{1}, Pages: 1}, now); err != nil {
		t.Fatal(err)
	}
	id := catalog.DocumentID(sha, 1)
	summary := "Это рекомендации по лечению от 04.04.2022. Рекомендована консультация гастроэнтеролога."
	essential := "Приём уролога\nРекомендации\nКонсультация гастроэнтеролога\nВрач-уролог Петрищев А. В."
	if err := c.SaveTexts(ctx, catalog.Texts{DocID: id, Status: catalog.TextsDone, Summary: summary, Essential: essential, Model: "t", Pipeline: 1}, now); err != nil {
		t.Fatal(err)
	}
	// The title names the doctor twice; the values are clean.
	leaky := `{"title": "Рекомендации уролога Петрищева", "вид": "рекомендации", "специальность": "уролог", "организация": "клиника Медси"}`
	m := &fakeModel{answers: []string{leaky}}
	rep, err := (&describe.Stage{Catalog: c, Model: m}).Run(ctx, describe.Options{Account: "acc"})
	if err != nil || rep.Done != 1 || len(m.prompts) != 2 {
		t.Fatalf("run: %+v, %v, %d calls", rep, err, len(m.prompts))
	}
	if !strings.Contains(m.prompts[0], "Врач-уролог") || !strings.Contains(m.prompts[1], "петрищ") {
		t.Errorf("prompts: the essential text goes along, the retry names the leak:\n%s", m.prompts[1])
	}
	_, tx, err := c.GetDocument(ctx, id)
	if err != nil || tx.Title != "Рекомендации по лечению от 04.04.2022" {
		t.Errorf("title fell back to the summary: %q, %v", tx.Title, err)
	}
	e, _ := c.IndexEntryOf(ctx, id)
	var got []string
	for _, a := range e.ModelAttrs {
		got = append(got, a.String())
	}
	if strings.Join(got, " ") != "вид=рекомендации специальность=уролог" {
		t.Errorf("attrs: %v (no organisation by name)", got)
	}
}
