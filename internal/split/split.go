package split

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/router"
)

// Pipeline is the version of this stage.
const Pipeline = 1

// DefaultModel is the splitter chosen in ADR-0003.
const DefaultModel = "google/gemini-4-argon"

// PageChars is how much of each page the model sees, as in the experiment.
const PageChars = 2500

// DefaultWindow is the most pages one model call sees. Longer files are cut
// into windows; the seam between two windows is decided by the rules.
const DefaultWindow = 30

// Prompt is the instruction measured in tools/ocrbench/split.py without the
// handwritten-date hint (ADR-0003: 100% on 27 files), with "medical archive"
// generalised to "archive of documents".
const Prompt = `Ниже текст страниц одного PDF из семейного архива документов, страницы по порядку.
Раздели их на отдельные бумажные документы. Правила:
- Снимки УЗИ и ленты ЭКГ, листы наклеек с номерами образцов и дневник пациента относятся к протоколу того же обследования.
- Документ, продолженный на следующей странице (таблица, текст, подпись), остаётся одним документом.
Ответь только JSON вида {"starts": [номера первых страниц документов]}, нумерация с 1.

`

// Options control one run.
type Options struct {
	Account string
	Model   string
	Limit   int // originals per run; 0 means all pending
	Window  int // pages per model call; 0 means DefaultWindow
}

// Report summarises one run.
type Report struct {
	Originals int
	Documents int
	Single    int // one-page originals, no model call
	Review    int // the model and a strong rule disagree
	Failed    int
	CostUSD   float64
	Notes     []string // disagreements and errors
	Duration  time.Duration
}

// Stage holds the stage's dependencies.
type Stage struct {
	Catalog *catalog.Catalog
	Model   router.Chatter
	Now     func() time.Time
}

// Run splits pending originals.
func (s *Stage) Run(ctx context.Context, o Options) (*Report, error) {
	start := time.Now()
	if o.Model == "" {
		o.Model = DefaultModel
	}
	if o.Window <= 1 {
		o.Window = DefaultWindow
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	pending, err := s.Catalog.PendingSplit(ctx, o.Limit)
	if err != nil {
		return nil, err
	}
	rep := &Report{Originals: len(pending)}
	for _, sha := range pending {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		r := s.one(ctx, o, sha)
		rep.CostUSD += r.CostUSD
		switch {
		case r.Status == catalog.SplitError:
			rep.Failed++
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %s", sha[:12], r.Error))
		case r.Method == "single":
			rep.Single++
		case r.Status == catalog.SplitReview:
			rep.Review++
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %s", sha[:12], r.Disagreements))
		}
		if r.Status != catalog.SplitError {
			rep.Documents += len(r.Starts)
		}
		if err := s.Catalog.SaveSplit(ctx, r, s.Now()); err != nil {
			return rep, err
		}
	}
	rep.Duration = time.Since(start)
	return rep, nil
}

func (s *Stage) one(ctx context.Context, o Options, sha string) catalog.SplitResult {
	r := catalog.SplitResult{SHA256: sha, Pipeline: Pipeline, Engine: "-"}
	fail := func(format string, args ...any) catalog.SplitResult {
		r.Status, r.Error, r.Starts = catalog.SplitError, fmt.Sprintf(format, args...), nil
		return r
	}
	ps, err := s.Catalog.Pages(ctx, sha)
	if err != nil {
		return fail("%v", err)
	}
	texts := make([]string, len(ps))
	for i, p := range ps {
		if p.Page != i+1 {
			return fail("page text incomplete: page %d missing", i+1)
		}
		texts[i] = p.Text
	}
	r.Pages = len(texts)
	if r.Pages == 0 {
		return fail("no page text")
	}
	if r.Pages == 1 {
		r.Status, r.Method, r.Starts = catalog.SplitDone, "single", []int{1}
		return r
	}
	if s.Model == nil {
		return fail("no model client")
	}

	r.Method = "model"
	starts := map[int]bool{1: true}
	for from := 1; from <= r.Pages; from += o.Window {
		to := min(from+o.Window-1, r.Pages)
		if to == from { // a one-page tail needs no model: its seam decides it
			break
		}
		got, reply, err := s.ask(ctx, o, texts[from-1:to])
		r.CostUSD += reply.CostUSD
		if err != nil {
			return fail("pages %d-%d: %v", from, to, err)
		}
		if reply.Model != "" {
			r.Engine = reply.Model
		}
		for _, k := range got {
			if k > 1 { // the window's first page is a seam, not the model's call
				starts[from+k-1] = true
			}
		}
	}

	var notes []string
	for k := 1; k < r.Pages; k++ {
		v := Boundary(texts[k-1], texts[k])
		next := k + 1
		if (next-1)%o.Window == 0 { // window seam: the rules decide
			if v.New {
				starts[next] = true
			}
			continue
		}
		if v.Strong && v.New != starts[next] {
			notes = append(notes, fmt.Sprintf("p%d|p%d: правило «%s» — %s, модель — %s",
				k, next, v.Reason, word(v.New), word(starts[next])))
		}
	}
	for k := range starts {
		r.Starts = append(r.Starts, k)
	}
	sort.Ints(r.Starts)
	r.Status = catalog.SplitDone
	if len(notes) > 0 {
		r.Status, r.Disagreements = catalog.SplitReview, strings.Join(notes, "; ")
	}
	return r
}

func word(isNew bool) string {
	if isNew {
		return "новый документ"
	}
	return "продолжение"
}

var jsonObject = regexp.MustCompile(`(?s)\{.*\}`)

// ask sends one window and returns the window-relative first pages.
func (s *Stage) ask(ctx context.Context, o Options, texts []string) ([]int, router.ChatReply, error) {
	var b strings.Builder
	b.WriteString(Prompt)
	for i, t := range texts {
		if r := []rune(t); len(r) > PageChars {
			t = string(r[:PageChars])
		}
		fmt.Fprintf(&b, "===== Страница %d =====\n%s\n\n", i+1, t)
	}
	reply, err := s.Model.Chat(ctx, router.ChatRequest{Account: o.Account, Model: o.Model, Text: b.String()})
	if err != nil {
		return nil, reply, err
	}
	got, err := parseStarts(reply.Text, len(texts))
	return got, reply, err
}

// parseStarts reads {"starts": [...]} and checks it: integers within the
// window, ascending, unique, page 1 included.
func parseStarts(text string, pages int) ([]int, error) {
	m := jsonObject.FindString(text)
	if m == "" {
		return nil, errors.New("model reply has no JSON object")
	}
	var v struct {
		Starts []int `json:"starts"`
	}
	if err := json.Unmarshal([]byte(m), &v); err != nil {
		return nil, fmt.Errorf("model reply: %w", err)
	}
	seen := map[int]bool{1: true}
	out := []int{1}
	for _, k := range v.Starts {
		if k < 1 || k > pages {
			return nil, fmt.Errorf("model reply: page %d outside 1-%d", k, pages)
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Ints(out)
	return out, nil
}
