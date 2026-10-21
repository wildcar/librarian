package pipeline_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/intake"
	"github.com/wildcar/librarian/internal/pages"
	"github.com/wildcar/librarian/internal/pipeline"
	"github.com/wildcar/librarian/internal/router"
)

// scan builds a PDF of empty pages: no text layer, every page needs OCR.
func scan(n int) []byte {
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>"}
	var kids []string
	for i := range n {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+i))
	}
	objs = append(objs, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	for range n {
		objs = append(objs, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] >>")
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	x := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, x)
	return b.Bytes()
}

type deadModel struct{ calls int }

func (d *deadModel) Chat(context.Context, router.ChatRequest) (router.ChatReply, error) {
	d.calls++
	return router.ChatReply{}, errors.New("router unavailable")
}

func TestWorkerWaitsForTheOwner(t *testing.T) {
	pop := pages.Poppler{Dir: os.Getenv("LIBRARIAN_POPPLER_DIR")}
	if err := pop.Check(); err != nil {
		t.Skipf("poppler not available: %v", err)
	}
	ctx := context.Background()
	home := t.TempDir()
	a, err := account.Create(home, "acc")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, err := intake.Store(ctx, c, a.Root(), "скан.pdf", bytes.NewReader(scan(3)), 1<<20)
	if err != nil || !st.New {
		t.Fatalf("store: %+v %v", st, err)
	}
	if again, _ := intake.Store(ctx, c, a.Root(), "копия.pdf", bytes.NewReader(scan(3)), 1<<20); again.New {
		t.Error("the same content stored as a new original")
	}
	if _, err := intake.Store(ctx, c, a.Root(), "big.pdf", bytes.NewReader(scan(3)), 10); err == nil {
		t.Error("oversized upload accepted")
	}

	model := &deadModel{}
	w := &pipeline.Worker{Home: home, LimitUSD: 0.01,
		Catalog: func(*account.Account) (*catalog.Catalog, error) { return c, nil },
		Tools: pipeline.Tools{Poppler: pop, Model: func(context.Context) (router.Chatter, func(), error) {
			return model, func() {}, nil
		}}}
	w.Pass(ctx)
	s := w.Status("acc")
	if s.State != pipeline.Held || s.PagesOCR != 3 || s.EstimateUSD <= 0.01 || model.calls != 0 {
		t.Fatalf("over the limit: %+v, %d model calls", s, model.calls)
	}

	// Another run holds the account: the worker waits, it does not double the work.
	unlock, err := pipeline.Lock(a)
	if err != nil {
		t.Fatal(err)
	}
	w.Approve("acc")
	w.Pass(ctx)
	if s := w.Status("acc"); s.State != pipeline.Held || model.calls != 0 {
		t.Errorf("busy account: %+v", s)
	}
	unlock()

	w.Pass(ctx) // still approved
	s = w.Status("acc")
	if s.State != pipeline.Finished || model.calls != 3 || len(s.Errors) == 0 || s.Pending.Originals != 1 {
		t.Errorf("approved run: %+v, %d model calls", s, model.calls)
	}
	// The approval was for that run only.
	w.Pass(ctx)
	if s := w.Status("acc"); s.State != pipeline.Held || model.calls != 3 {
		t.Errorf("after the run: %+v", s)
	}
}
