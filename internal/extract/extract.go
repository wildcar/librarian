// Package extract produces the verbatim text of every page of an account's
// originals (ADR-0002, ADR-0005): the PDF text layer where it is meaningful,
// otherwise OCR of the rendered page through the router.
package extract

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/intake"
	"github.com/wildcar/librarian/internal/pages"
	"github.com/wildcar/librarian/internal/router"
)

// Pipeline is the version of this stage; pages stored by an older version are
// redone.
const Pipeline = 1

// DefaultModel is the OCR model chosen in ADR-0002.
const DefaultModel = "google/gemini-4-argon"

// EstimateUSDPerPage is the measured OCR cost per page with DefaultModel
// (ADR-0002), used only for dry-run estimates.
const EstimateUSDPerPage = 0.0097

// LongSide is the page image size sent to OCR, as in the bake-off.
const LongSide = 2000

// Prompt is the OCR instruction measured in the bake-off (tools/ocrbench).
const Prompt = `Перепиши весь текст с этой страницы скана дословно, на языке оригинала.
Правила:
- Сохраняй опечатки и сокращения как есть, ничего не исправляй и не дописывай.
- Таблицы: каждая строка таблицы на одной строке, ячейки через " | ".
- Рукописный текст переписывай тоже; нечитаемое слово помечай [?].
- Текст штампов и печатей переписывай отдельной строкой с пометкой [штамп].
- Если страница повёрнута, читай её в правильной ориентации.
- Графики, ЭКГ, снимки УЗИ не описывай, только подписи и цифры на них.
Выведи только текст страницы, без комментариев.`

// Options control one run.
type Options struct {
	Account string
	Model   string
	Workers int  // parallel OCR calls
	Limit   int  // originals per run; 0 means all pending
	DryRun  bool // count and estimate only, no model calls
}

// Report summarises one run.
type Report struct {
	Originals      int
	Done           int
	Unsupported    int
	Failed         int
	PagesTextLayer int
	PagesOCR       int
	PagesSkipped   int // already stored by this pipeline version
	CostUSD        float64
	EstimateUSD    float64 // dry run only
	Errors         []string
	Duration       time.Duration
}

// Stage holds the stage's dependencies.
type Stage struct {
	Catalog *catalog.Catalog
	Poppler pages.Poppler
	OCR     router.Chatter // may be nil in a dry run
	Now     func() time.Time
}

// Run processes pending originals.
func (s *Stage) Run(ctx context.Context, o Options) (*Report, error) {
	start := time.Now()
	if o.Model == "" {
		o.Model = DefaultModel
	}
	if o.Workers < 1 {
		o.Workers = 1
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if !o.DryRun && s.OCR == nil {
		return nil, errors.New("extract: no router client")
	}
	pending, err := s.Catalog.PendingExtraction(ctx, o.Limit)
	if err != nil {
		return nil, err
	}
	rep := &Report{Originals: len(pending)}
	for _, p := range pending {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		status, n, errText := s.one(ctx, o, p, rep)
		if o.DryRun {
			continue
		}
		switch status {
		case catalog.ExtractDone:
			rep.Done++
		case catalog.ExtractUnsupported:
			rep.Unsupported++
		default:
			rep.Failed++
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %s", p.SHA256[:12], errText))
		}
		if err := s.Catalog.SetExtraction(ctx, p.SHA256, n, status, errText, s.Now()); err != nil {
			return rep, err
		}
	}
	rep.Duration = time.Since(start)
	return rep, nil
}

// readable finds a present source whose content still matches the hash.
func readable(p catalog.Pending) (string, error) {
	var problems []string
	for _, uri := range p.URIs {
		path, err := intake.PathFromURI(uri)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		sha, err := intake.HashFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if sha != p.SHA256 {
			problems = append(problems, fmt.Sprintf("%s: content changed since intake", path))
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("no readable source: %s", strings.Join(problems, "; "))
}

// one handles a single original and returns its extraction status.
func (s *Stage) one(ctx context.Context, o Options, p catalog.Pending, rep *Report) (status string, pageCount int, errText string) {
	isPDF := p.MIME == "application/pdf"
	if !isPDF && !pages.ImageTypes[p.MIME] {
		return catalog.ExtractUnsupported, 0, "type " + p.MIME + " is not handled yet"
	}
	path, err := readable(p)
	if err != nil {
		return catalog.ExtractError, 0, err.Error()
	}
	done, err := s.Catalog.DonePages(ctx, p.SHA256, Pipeline)
	if err != nil {
		return catalog.ExtractError, 0, err.Error()
	}

	type task struct {
		page  int
		text  string // text layer, if meaningful
		image []byte // OCR input otherwise
	}
	var tasks []task

	if isPDF {
		layers, err := s.Poppler.TextLayer(ctx, path)
		if err != nil {
			return catalog.ExtractError, 0, err.Error()
		}
		pageCount = len(layers)
		needOCR := false
		for i, t := range layers {
			if !done[i+1] && !pages.MeaningfulText(t) {
				needOCR = true
			}
		}
		var images []string
		if needOCR && !o.DryRun {
			dir, err := os.MkdirTemp("", "librarian-pages-")
			if err != nil {
				return catalog.ExtractError, 0, err.Error()
			}
			defer os.RemoveAll(dir)
			images, err = s.Poppler.Render(ctx, path, dir, LongSide)
			if err != nil {
				return catalog.ExtractError, 0, err.Error()
			}
			if len(images) != pageCount {
				return catalog.ExtractError, 0, fmt.Sprintf("text layer has %d pages, render has %d", pageCount, len(images))
			}
		}
		for i, t := range layers {
			n := i + 1
			switch {
			case done[n]:
				rep.PagesSkipped++
			case pages.MeaningfulText(t):
				tasks = append(tasks, task{page: n, text: t})
			case o.DryRun:
				rep.PagesOCR++
				rep.EstimateUSD += EstimateUSDPerPage
			default:
				img, err := os.ReadFile(images[i])
				if err != nil {
					return catalog.ExtractError, pageCount, err.Error()
				}
				tasks = append(tasks, task{page: n, image: img})
			}
		}
	} else {
		pageCount = 1
		switch {
		case done[1]:
			rep.PagesSkipped++
		case o.DryRun:
			rep.PagesOCR++
			rep.EstimateUSD += EstimateUSDPerPage
		default:
			img, err := pages.PrepareImage(path, LongSide)
			if err != nil {
				return catalog.ExtractError, 1, err.Error()
			}
			tasks = append(tasks, task{page: 1, image: img})
		}
	}
	if o.DryRun {
		for _, t := range tasks {
			if t.image == nil {
				rep.PagesTextLayer++
			}
		}
		return catalog.ExtractDone, pageCount, ""
	}

	// Pages run in parallel; each result is stored as soon as it arrives, so an
	// interrupted run keeps what it paid for.
	var (
		mu       sync.Mutex
		firstErr string
		wg       sync.WaitGroup
		sem      = make(chan struct{}, o.Workers)
	)
	for _, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			pg := catalog.Page{SHA256: p.SHA256, Page: t.page, Pipeline: Pipeline}
			if t.image == nil {
				pg.Method, pg.Engine, pg.Text = "text-layer", "pdftotext", t.text
			} else {
				reply, err := s.OCR.Chat(ctx, router.ChatRequest{
					Account: o.Account, Model: o.Model, Text: Prompt,
					ImagesB64: []string{base64.StdEncoding.EncodeToString(t.image)}, ImageMIME: "image/jpeg",
				})
				if err == nil && strings.TrimSpace(reply.Text) == "" {
					err = errors.New("empty OCR reply")
				}
				if err != nil {
					mu.Lock()
					if firstErr == "" {
						firstErr = fmt.Sprintf("page %d: %v", t.page, err)
					}
					mu.Unlock()
					return
				}
				engine := reply.Model
				if engine == "" {
					engine = o.Model
				}
				pg.Method, pg.Engine, pg.Text, pg.CostUSD = "ocr", engine, reply.Text, reply.CostUSD
			}
			err := s.Catalog.SavePage(ctx, pg, s.Now())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == "" {
					firstErr = err.Error()
				}
				return
			}
			if pg.Method == "ocr" {
				rep.PagesOCR++
				rep.CostUSD += pg.CostUSD
			} else {
				rep.PagesTextLayer++
			}
		}()
	}
	wg.Wait()
	if firstErr != "" {
		return catalog.ExtractError, pageCount, firstErr
	}
	return catalog.ExtractDone, pageCount, ""
}
