package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
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
	Queue       []string // folders and links waiting for intake, as shown
	Progress    string   // of the intake going on
	LastIntake  string   // what the last folder or link intake did
}

// job is a folder or a link the owner gave in the web, with the batch's labels.
type job struct {
	path, link string
	labels     Labels
}

// shown names a job for the owner.
func (j job) shown() string {
	if j.link != "" {
		return "ссылка " + j.link
	}
	return "папка " + j.path
}

// Worker processes every account in the background: one account at a time,
// when kicked (an upload) and every Every. A run whose OCR estimate exceeds
// LimitUSD waits until the owner allows it.
type Worker struct {
	Home     string
	Tools    Tools
	Catalog  func(*account.Account) (*catalog.Catalog, error)
	LimitUSD float64
	MaxFile  int64 // a link's file over this is skipped; 0 = 100 MB
	Every    time.Duration
	Log      io.Writer

	mu       sync.Mutex
	status   map[string]Status
	approved map[string]float64 // an estimate the owner allowed
	jobs     map[string][]job
	kick     chan struct{}
	log      *log.Logger
}

func (w *Worker) init() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kick == nil {
		w.kick = make(chan struct{}, 1)
		w.status, w.approved, w.jobs = map[string]Status{}, map[string]float64{}, map[string][]job{}
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

// IngestFolder queues a folder intake for an account: the next pass records
// its files, makes the batch with the labels, and processes them.
func (w *Worker) IngestFolder(accountID, path string, l Labels) {
	w.queue(accountID, job{path: path, labels: l})
}

// IngestLink queues a public cloud link the same way: its files are
// downloaded into the account's store.
func (w *Worker) IngestLink(accountID, link string, l Labels) {
	w.queue(accountID, job{link: link, labels: l})
}

func (w *Worker) queue(accountID string, j job) {
	w.init()
	w.mu.Lock()
	w.jobs[accountID] = append(w.jobs[accountID], j)
	s := w.status[accountID]
	s.Queue = append(s.Queue, j.shown())
	w.status[accountID] = s
	w.mu.Unlock()
	w.Kick()
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
	w.mu.Lock()
	for id := range w.jobs { // an account created since the listing
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	w.mu.Unlock()
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
	if err := w.intakes(ctx, id, a, c); err != nil {
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

// intakes takes the account's queued folders and links, one by one.
func (w *Worker) intakes(ctx context.Context, id string, a *account.Account, c *catalog.Catalog) error {
	for {
		w.mu.Lock()
		q := w.jobs[id]
		if len(q) == 0 {
			w.mu.Unlock()
			return nil
		}
		j := q[0]
		w.jobs[id] = q[1:]
		w.mu.Unlock()
		var note, logged string
		if j.link != "" {
			note, logged = w.link(ctx, id, a, c, j)
		} else {
			note = w.folder(ctx, id, c, j)
			logged = note
		}
		w.log.Printf("%s: %s", id, logged)
		w.set(id, func(s *Status) {
			s.State, s.Stage, s.Progress, s.LastIntake = Finished, "", "", note
			if len(s.Queue) > 0 {
				s.Queue = s.Queue[1:]
			}
		})
	}
}

func (w *Worker) folder(ctx context.Context, id string, c *catalog.Catalog, j job) string {
	w.set(id, func(s *Status) { s.State, s.Stage = Running, "folder" })
	f, err := IngestFolder(ctx, c, j.path, max(w.Tools.Workers, 1), j.labels)
	note := ""
	switch {
	case err != nil:
		note = fmt.Sprintf("папка %s: %v", j.path, err)
	case f.Batch.ID == 0:
		note = fmt.Sprintf("папка %s: новых файлов нет, пачка не создана", j.path)
	default:
		note = fmt.Sprintf("папка %s: пачка №%d, файлов %d, новых %d", j.path, f.Batch.ID, f.Batch.Originals, f.Intake.NewFiles)
	}
	if f != nil && f.Intake != nil && len(f.Intake.Errors) > 0 {
		note += fmt.Sprintf(", не прочитано %d (нет доступа?)", len(f.Intake.Errors))
	}
	return note
}

// link downloads a link's files; the log names the service, not the link,
// which is the key to the share.
func (w *Worker) link(ctx context.Context, id string, a *account.Account, c *catalog.Catalog, j job) (note, logged string) {
	w.set(id, func(s *Status) { s.State, s.Stage, s.Progress = Running, "link", "список файлов" })
	maxFile := w.MaxFile
	if maxFile <= 0 {
		maxFile = 100 << 20
	}
	l, err := IngestLink(ctx, c, a.Root(), j.link, maxFile, j.labels, func(done, total int) {
		w.set(id, func(s *Status) { s.Progress = fmt.Sprintf("скачано %d из %d", done, total) })
	})
	switch {
	case err != nil && l == nil:
		note = fmt.Sprintf("ссылка %s: %v", j.link, err)
	case err != nil:
		note = fmt.Sprintf("ссылка %s (%s): %v", j.link, l.Service, err)
	case l.Batch.ID == 0:
		note = fmt.Sprintf("ссылка %s (%s): файлов %d, новых нет, пачка не создана", j.link, l.Service, l.Files)
	default:
		note = fmt.Sprintf("ссылка %s (%s): пачка №%d, файлов %d, новых %d", j.link, l.Service, l.Batch.ID, l.Files, l.New)
	}
	if l != nil && len(l.Skipped) > 0 {
		note += fmt.Sprintf("; не взято %d: %s", len(l.Skipped), strings.Join(l.Skipped, "; "))
	}
	logged = "link intake"
	if l != nil {
		logged = fmt.Sprintf("link intake (%s): batch %d, files %d, new %d, skipped %d", l.Service, l.Batch.ID, l.Files, l.New, len(l.Skipped))
	}
	if err != nil {
		logged += ": " + strings.ReplaceAll(err.Error(), j.link, "<link>")
	}
	return note, logged
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
