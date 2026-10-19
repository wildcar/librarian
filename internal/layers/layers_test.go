package layers_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/layers"
	"github.com/wildcar/librarian/internal/router"
)

// fakeModel answers the essential prompt and the summary prompt from scripts.
type fakeModel struct {
	mu        sync.Mutex
	essential []string
	summary   []string
	prompts   []string
}

func (f *fakeModel) Chat(ctx context.Context, r router.ChatRequest) (router.ChatReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, r.Text)
	next := &f.summary
	if strings.HasPrefix(r.Text, layers.EssentialPrompt) {
		next = &f.essential
	}
	if len(*next) == 0 {
		return router.ChatReply{}, errors.New("no reply scripted")
	}
	rep := (*next)[0]
	*next = (*next)[1:]
	return router.ChatReply{Text: rep, Model: r.Model, CostUSD: 0.001}, nil
}

const page = `СМ-Клиника
многопрофильный медицинский холдинг
Протокол обследования
Пациент: Иванов Петр Сергеевич
Врач: Сидорова Анна Борисовна
Дата выполнения: 2 апреля 2022 г.
Заключение: эрозивный рефлюкс-эзофагит.
Скидка 20% на эндоскопию после 15:00
Бланк разработан на основании формы № 014-1/у`

func setup(t *testing.T) (*catalog.Catalog, string) {
	t.Helper()
	ctx := context.Background()
	a, err := account.Create(t.TempDir(), "family")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	sha := strings.Repeat("ab", 32)
	now := time.Now()
	if _, err := c.RecordSeen(ctx, catalog.Seen{URI: "file:///scan.pdf", SHA256: sha, Size: 1, MTime: now, MIME: "application/pdf", At: now}); err != nil {
		t.Fatal(err)
	}
	if err := c.SavePage(ctx, catalog.Page{SHA256: sha, Page: 1, Method: "ocr", Engine: "t", Text: page, Pipeline: 1}, now); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveSplit(ctx, catalog.SplitResult{SHA256: sha, Status: catalog.SplitDone, Method: "single", Engine: "-", Pipeline: 1, Starts: []int{1}, Pages: 1}, now); err != nil {
		t.Fatal(err)
	}
	return c, catalog.DocumentID(sha, 1)
}

func TestDigestCleanAndFlagged(t *testing.T) {
	ctx := context.Background()
	c, id := setup(t)
	m := &fakeModel{
		// rules drop the ad, the holding line and the form note; the model drops the brand line (1)
		essential: []string{`{"remove": [[1, "шапка"]]}`},
		// the first summary leaks a surname; the retry is clean
		summary: []string{
			"Протокол эндоскопии от 02.04.2022, пациент Иванов: эрозивный рефлюкс-эзофагит.",
			"Протокол эндоскопии от 02.04.2022: эрозивный рефлюкс-эзофагит.",
		},
	}
	st := &layers.Stage{Catalog: c, Model: m}
	rep, err := st.Run(ctx, layers.Options{Account: "family"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Flagged != 0 || rep.Failed != 0 {
		t.Fatalf("report %+v", rep)
	}
	_, tx, err := c.GetDocument(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tx.Essential, "Скидка") || strings.Contains(tx.Essential, "Бланк") || strings.Contains(tx.Essential, "холдинг") {
		t.Errorf("boilerplate left in essential:\n%s", tx.Essential)
	}
	if !strings.Contains(tx.Essential, "Иванов Петр Сергеевич") || !strings.Contains(tx.Essential, "эзофагит") {
		t.Errorf("content lost from essential:\n%s", tx.Essential)
	}
	if strings.Contains(tx.Summary, "Иванов") {
		t.Errorf("leaky summary stored: %s", tx.Summary)
	}
	if !strings.Contains(m.prompts[len(m.prompts)-1], "нарушило правила") {
		t.Error("retry prompt does not name the violation")
	}
	if !strings.Contains(m.prompts[0], "1: Протокол обследования") && !strings.Contains(m.prompts[0], "1: СМ-Клиника") {
		t.Error("essential prompt does not number lines")
	}
}

func TestDigestFlagsPersistentLeakAndRetriesErrors(t *testing.T) {
	ctx := context.Background()
	c, id := setup(t)
	m := &fakeModel{
		essential: []string{"не JSON", `{"remove": []}`},
		summary:   []string{"Документ Сидоровой.", "Документ Сидоровой снова."},
	}
	st := &layers.Stage{Catalog: c, Model: m}
	rep, _ := st.Run(ctx, layers.Options{Account: "family"})
	if rep.Failed != 1 {
		t.Fatalf("bad essential reply: %+v", rep)
	}
	rep, _ = st.Run(ctx, layers.Options{Account: "family"})
	if rep.Flagged != 1 {
		t.Fatalf("persistent leak not flagged: %+v", rep)
	}
	_, tx, _ := c.GetDocument(ctx, id)
	if tx.Status != catalog.TextsFlagged || !strings.Contains(tx.Flags, "сидоро") {
		t.Errorf("stored %s flags %q", tx.Status, tx.Flags)
	}
	if tx.CostUSD < 0.0039 { // costs of the failed run add up
		t.Errorf("cost %v does not include the failed attempt", tx.CostUSD)
	}
	// A new split of the original removes the derived layers with the documents.
	sha := strings.Repeat("ab", 32)
	if err := c.SaveSplit(ctx, catalog.SplitResult{SHA256: sha, Status: catalog.SplitDone, Method: "single", Engine: "-", Pipeline: 1, Starts: []int{1}, Pages: 1}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, tx, _ := c.GetDocument(ctx, id); tx.Status != "" {
		t.Errorf("layers survived a re-split: %s", tx.Status)
	}
}
