// Package pipeline takes an account's new originals all the way to search
// (ADR-0007): extract → split → digest → date → index, the stages the CLI
// also runs one by one. It runs from `librarian run` and from the worker in
// `librarian serve`; a lock file keeps two runs off one account.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/dating"
	"github.com/wildcar/librarian/internal/extract"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/layers"
	"github.com/wildcar/librarian/internal/pages"
	"github.com/wildcar/librarian/internal/router"
	"github.com/wildcar/librarian/internal/split"
)

// ErrBusy is returned when another run holds the account.
var ErrBusy = errors.New("the account is being processed by another run")

// Tools are what the stages need besides the catalog.
type Tools struct {
	Poppler pages.Poppler
	Checker extract.Reader // tesseract second reading; nil skips it
	// Model opens the model router; the returned function closes it.
	Model   func(ctx context.Context) (router.Chatter, func(), error)
	Workers int
}

// Pending counts the work waiting in each stage.
type Pending struct {
	Originals, Split, Texts, Dates int
}

// Any says whether there is anything to do.
func (p Pending) Any() bool { return p.Originals+p.Split+p.Texts+p.Dates > 0 }

// Waiting counts what the stages have not done yet.
func Waiting(ctx context.Context, c *catalog.Catalog) (Pending, error) {
	var p Pending
	o, err := c.PendingExtraction(ctx, 0)
	if err != nil {
		return p, err
	}
	s, err := c.PendingSplit(ctx, 0)
	if err != nil {
		return p, err
	}
	t, err := c.PendingTexts(ctx, 0)
	if err != nil {
		return p, err
	}
	d, err := c.PendingDates(ctx, dating.Pipeline, 0)
	if err != nil {
		return p, err
	}
	return Pending{Originals: len(o), Split: len(s), Texts: len(t), Dates: len(d)}, nil
}

// Estimate is the expected OCR cost of the waiting originals, the bulk of
// what a run spends (extract's dry run: pages without a text layer).
func Estimate(ctx context.Context, c *catalog.Catalog, t Tools, accountID string) (pagesOCR int, usd float64, err error) {
	st := &extract.Stage{Catalog: c, Poppler: t.Poppler}
	rep, err := st.Run(ctx, extract.Options{Account: accountID, DryRun: true})
	if err != nil {
		return 0, 0, err
	}
	return rep.PagesOCR, rep.EstimateUSD, nil
}

// Report is one run.
type Report struct {
	Extract *extract.Report
	Split   *split.Report
	Digest  *layers.Report
	Date    *dating.Report
	Index   *index.Report
	CostUSD float64
	Errors  []string
}

// Progress is told which stage starts.
type Progress func(stage string)

// Run takes the account through every stage. Each stage does only what is
// pending, so a run after an interruption continues where it stopped. A
// failing stage does not stop the next ones: they work on what is ready.
func Run(ctx context.Context, a *account.Account, c *catalog.Catalog, t Tools, progress Progress) (*Report, error) {
	unlock, err := Lock(a)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if progress == nil {
		progress = func(string) {}
	}
	model, closeModel, err := t.Model(ctx)
	if err != nil {
		return nil, err
	}
	defer closeModel()
	workers := max(t.Workers, 1)
	rep := &Report{}
	note := func(stage string, err error, errs []string) {
		if err != nil {
			rep.Errors = append(rep.Errors, stage+": "+err.Error())
		}
		for _, e := range errs {
			rep.Errors = append(rep.Errors, stage+": "+e)
		}
	}

	progress("extract")
	ex := &extract.Stage{Catalog: c, Poppler: t.Poppler, OCR: model, Checker: t.Checker}
	rep.Extract, err = ex.Run(ctx, extract.Options{Account: a.ID, Workers: workers})
	if rep.Extract != nil {
		rep.CostUSD += rep.Extract.CostUSD
		note("распознавание", nil, rep.Extract.Errors)
	}
	note("распознавание", err, nil)
	if ctx.Err() != nil {
		return rep, ctx.Err()
	}

	progress("split")
	sp := &split.Stage{Catalog: c, Model: model}
	rep.Split, err = sp.Run(ctx, split.Options{Account: a.ID})
	if rep.Split != nil {
		rep.CostUSD += rep.Split.CostUSD
	}
	note("разбивка", err, nil)
	if ctx.Err() != nil {
		return rep, ctx.Err()
	}

	progress("digest")
	dg := &layers.Stage{Catalog: c, Model: model}
	rep.Digest, err = dg.Run(ctx, layers.Options{Account: a.ID, Workers: workers})
	if rep.Digest != nil {
		rep.CostUSD += rep.Digest.CostUSD
	}
	note("резюме", err, nil)
	if ctx.Err() != nil {
		return rep, ctx.Err()
	}

	progress("date")
	dt := &dating.Stage{Catalog: c, Model: model}
	rep.Date, err = dt.Run(ctx, dating.Options{Account: a.ID, Workers: workers})
	if rep.Date != nil {
		rep.CostUSD += rep.Date.CostUSD
	}
	note("дата", err, nil)
	if ctx.Err() != nil {
		return rep, ctx.Err()
	}

	progress("index")
	rep.Index, err = index.Run(ctx, c, time.Now())
	note("индекс", err, nil)
	return rep, nil
}

// Lock takes the account's pipeline lock (flock, so it is released when the
// process dies) and returns its release.
func Lock(a *account.Account) (func(), error) {
	f, err := os.OpenFile(filepath.Join(a.Root(), "pipeline.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("pipeline lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("pipeline lock: %w", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
