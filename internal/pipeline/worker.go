package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
)

// States of an account in the worker.
const (
	Idle     = "idle"     // nothing to do
	Held     = "waiting"  // the estimate is over the limit: the owner must allow it
	Running  = "running"  // a run is going
	Failed   = "failed"   // the last run could not start or broke off
	Finished = "finished" // the last run is over; Pending says what is left
)

// Status is what the web shows about an account's processing.
type Status struct {
	State       string
	Stage       string // while running
	Pending     Pending
	PagesOCR    int     // of the waiting originals, pages that need OCR
	EstimateUSD float64 // their expected cost
	Started     time.Time
	Ended       time.Time
	CostUSD     float64  // of the last run
	Errors      []string // of the last run
}

// Worker processes every account in the background: one account at a time,
// when kicked (an upload) and every Every. A run whose OCR estimate exceeds
// LimitUSD waits until the owner allows it.
type Worker struct {
	Home     string
	Tools    Tools
	Catalog  func(*account.Account) (*catalog.Catalog, error)
	LimitUSD float64
	Every    time.Duration
	Log      io.Writer

	mu       sync.Mutex
	status   map[string]Status
	approved map[string]float64 // an estimate the owner allowed
	kick     chan struct{}
	log      *log.Logger
}

func (w *Worker) init() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kick == nil {
		w.kick = make(chan struct{}, 1)
		w.status, w.approved = map[string]Status{}, map[string]float64{}
		out := w.Log
		if out == nil {
			out = io.Discard
		}
		w.log = log.New(out, "pipeline: ", 0)
	}
}

// Kick asks for a pass soon, e.g. after an upload.
func (w *Worker) Kick() {
	w.init()
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Status returns an account's state.
func (w *Worker) Status(accountID string) Status {
	w.init()
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status[accountID]
}

// Approve lets a waiting account run with its current estimate.
func (w *Worker) Approve(accountID string) {
	w.init()
	w.mu.Lock()
	w.approved[accountID] = w.status[accountID].EstimateUSD
	w.mu.Unlock()
	w.Kick()
}

func (w *Worker) set(id string, f func(*Status)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.status[id]
	f(&s)
	w.status[id] = s
}

// Start runs passes until ctx ends.
func (w *Worker) Start(ctx context.Context) {
	w.init()
	every := w.Every
	if every <= 0 {
		every = time.Hour
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		w.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.kick:
		}
	}
}

// Pass looks at every account once.
func (w *Worker) Pass(ctx context.Context) {
	w.init()
	ids, err := accounts(w.Home)
	if err != nil {
		w.log.Printf("accounts: %v", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if err := w.one(ctx, id); err != nil {
			w.log.Printf("%s: %v", id, err)
			w.set(id, func(s *Status) { s.State, s.Errors = Failed, []string{err.Error()} })
		}
	}
}

func (w *Worker) one(ctx context.Context, id string) error {
	a, err := account.Open(w.Home, id)
	if err != nil {
		return err
	}
	c, err := w.Catalog(a)
	if err != nil {
		return err
	}
	p, err := Waiting(ctx, c)
	if err != nil {
		return err
	}
	w.set(id, func(s *Status) {
		s.Pending = p
		if !p.Any() && s.State != Finished {
			s.State = Idle
		}
	})
	if !p.Any() {
		return nil
	}
	pagesOCR, est := 0, 0.0
	if p.Originals > 0 {
		if pagesOCR, est, err = Estimate(ctx, c, w.Tools, id); err != nil {
			return err
		}
	}
	w.mu.Lock()
	allowed := est <= w.LimitUSD || (w.approved[id] > 0 && est <= w.approved[id]*1.1)
	w.mu.Unlock()
	w.set(id, func(s *Status) { s.PagesOCR, s.EstimateUSD = pagesOCR, est })
	if !allowed {
		w.set(id, func(s *Status) { s.State = Held })
		return nil
	}

	w.log.Printf("%s: run (%d originals, %d OCR pages, ~$%.2f)", id, p.Originals, pagesOCR, est)
	w.set(id, func(s *Status) {
		s.State, s.Stage, s.Started, s.Ended, s.CostUSD, s.Errors = Running, "", time.Now(), time.Time{}, 0, nil
	})
	rep, err := Run(ctx, a, c, w.Tools, func(stage string) { w.set(id, func(s *Status) { s.Stage = stage }) })
	if errors.Is(err, ErrBusy) {
		w.set(id, func(s *Status) { s.State, s.Stage = Held, "занят другим запуском" })
		return nil
	}
	w.mu.Lock()
	delete(w.approved, id)
	w.mu.Unlock()
	left, _ := Waiting(ctx, c)
	w.set(id, func(s *Status) {
		s.State, s.Stage, s.Ended, s.Pending = Finished, "", time.Now(), left
		if rep != nil {
			s.CostUSD, s.Errors = rep.CostUSD, rep.Errors
		}
		if err != nil {
			s.State, s.Errors = Failed, append(s.Errors, err.Error())
		}
	})
	if rep != nil {
		w.log.Printf("%s: done, $%.3f, %d errors", id, rep.CostUSD, len(rep.Errors))
	}
	return nil
}

func accounts(home string) ([]string, error) {
	es, err := os.ReadDir(filepath.Join(home, "accounts"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	var out []string
	for _, e := range es {
		if e.IsDir() && account.ValidateID(e.Name()) == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
