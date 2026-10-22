// Package titling names each document in a few words for lists: the kind of
// document and what it is about, made from the de-identified summary, so the
// title carries no more personal data than the summary does.
package titling

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/layers"
	"github.com/wildcar/librarian/internal/router"
)

// DefaultModel is the text-layer model (ADR-0005): it wrote the summary.
const DefaultModel = layers.DefaultModel

// MaxRunes bounds a title; a longer answer is cut at a word.
const MaxRunes = 80

// Prompt is general on purpose: the library holds any documents, medicine is
// only the first example.
const Prompt = `Ниже резюме документа. Дай документу короткое название для списка, 2–6 слов.
- Название — вид документа и, если есть, кто его составил по специальности или о чём он: «Рекомендации уролога по лечению», «Суточное мониторирование ЭКГ», «Протокол гастроскопии», «Дополнительное соглашение к договору», «Выписка по счёту», «Счёт за электроэнергию».
- Без даты, без слов «это», «документ», «оформленный», «выданный», без организации и её типа, без имён и номеров.
- С большой буквы, без точки и кавычек.
Ответь только названием.

`

// Options control one run.
type Options struct {
	Account string
	Model   string
	Workers int
	Limit   int
}

// Report summarises one run.
type Report struct {
	Documents int
	Done      int
	Failed    int
	CostUSD   float64
	Notes     []string
	Duration  time.Duration
}

// Stage holds the stage's dependencies.
type Stage struct {
	Catalog *catalog.Catalog
	Model   router.Chatter
}

// Run titles every document that has a summary and no title.
func (s *Stage) Run(ctx context.Context, o Options) (*Report, error) {
	start := time.Now()
	if o.Model == "" {
		o.Model = DefaultModel
	}
	if o.Workers < 1 {
		o.Workers = 1
	}
	docs, err := s.Catalog.PendingTitles(ctx, o.Limit)
	if err != nil {
		return nil, err
	}
	rep := &Report{Documents: len(docs)}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, o.Workers)
	)
	for _, d := range docs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			reply, err := s.Model.Chat(ctx, router.ChatRequest{Account: o.Account, Model: o.Model, Text: Prompt + d.Summary})
			title := Clean(reply.Text)
			if err == nil && title == "" {
				err = fmt.Errorf("пустой ответ")
			}
			if err == nil {
				err = s.Catalog.SaveTitle(ctx, d.DocID, d.Summary, title)
			}
			mu.Lock()
			defer mu.Unlock()
			rep.CostUSD += reply.CostUSD
			if err != nil {
				rep.Failed++
				rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %v", d.DocID, err))
				return
			}
			rep.Done++
		}()
	}
	wg.Wait()
	rep.Duration = time.Since(start)
	return rep, ctx.Err()
}

// Clean takes a model's answer to a title: the first line, without quotes,
// a leading «Это» or «Название:», and a final period; capitalised and cut to
// MaxRunes at a word.
func Clean(answer string) string {
	t := strings.TrimSpace(answer)
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSpace(strings.TrimPrefix(t, "Название:"))
	t = strings.Trim(t, "«»\"'“”*. ")
	for _, p := range []string{"Это ", "это "} {
		t = strings.TrimPrefix(t, p)
	}
	if utf8.RuneCountInString(t) > MaxRunes {
		r := []rune(t)[:MaxRunes]
		if i := strings.LastIndexByte(string(r), ' '); i > 0 {
			t = string(r)[:i]
		} else {
			t = string(r)
		}
		t = strings.TrimRight(t, " ,;:—-")
	}
	if r, n := utf8.DecodeRuneInString(t); n > 0 {
		t = string(unicode.ToUpper(r)) + t[n:]
	}
	return t
}
