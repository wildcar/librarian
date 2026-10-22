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
	"path/filepath"
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
	PagesChecked   int     // pages that got the second reading
	PagesFlagged   int     // of them, readings disagree
	CheckErrors    []string
	Errors         []string
	Duration       time.Duration
}

// Stage holds the stage's dependencies.
type Stage struct {
	Catalog *catalog.Catalog
	Poppler pages.Poppler
	OCR     router.Chatter // may be nil in a dry run
	Checker Reader         // second reading; nil skips it
	Now     func() time.Time
}

// Reader is the second, independent reading of a page image (tesseract).
type Reader interface {
	Read(ctx context.Context, image string) (pages.Reading, error)
}

// CheckLongSide is the page image size for the second reading: an A4 page
// at 300 dpi, as in the bake-off where tesseract read 96% of printed facts.
const CheckLongSide = 3508

func (s *Stage) renderForCheck(ctx context.Context, pdf string, pageCount int) ([]string, error) {
	dir, err := os.MkdirTemp("", "librarian-check-")
	if err != nil {
		return nil, err
	}
	imgs, err := s.Poppler.RenderPNG(ctx, pdf, dir, CheckLongSide)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if len(imgs) != pageCount {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("check render has %d pages, want %d", len(imgs), pageCount)
	}
	return imgs, nil
}

// check reads a page a second time, compares and stores the result.
func (s *Stage) check(ctx context.Context, sha string, page int, image, primary string) (Check, error) {
	r, err := s.Checker.Read(ctx, image)
	if err != nil {
		return Check{}, err
	}
	k := Compare(r, primary)
	err = s.Catalog.SaveCheck(ctx, sha, page, catalog.PageCheck{
		Engine: CheckEngine, Text: r.Text, Words: k.Words, Matched: k.Matched,
		Missing: k.Missing, Run: k.MaxRun, Flagged: k.Flagged,
	})
	return k, err
}

// count adds one check outcome to the report; the caller holds the lock.
func (s *Stage) count(rep *Report, sha string, page int, k Check, err error) {
	if err != nil {
		rep.CheckErrors = append(rep.CheckErrors, fmt.Sprintf("%s/%d: %v", sha[:12], page, err))
		return
	}
	rep.PagesChecked++
	if k.Flagged {
		rep.PagesFlagged++
	}
}

// Verify gives the second reading to OCR pages that do not have it yet.
func (s *Stage) Verify(ctx context.Context, o Options) (*Report, error) {
	start := time.Now()
	if s.Checker == nil {
		return nil, errors.New("verify: no second reader")
	}
	if o.Workers < 1 {
		o.Workers = 1
	}
	origs, byOrig, err := s.Catalog.Unchecked(ctx, o.Limit)
	if err != nil {
		return nil, err
	}
	rep := &Report{Originals: len(origs)}
	for _, p := range origs {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		path, err := readable(p)
		if err != nil {
			rep.CheckErrors = append(rep.CheckErrors, fmt.Sprintf("%s: %v", p.SHA256[:12], err))
			continue
		}
		stored, err := s.Catalog.Pages(ctx, p.SHA256)
		if err != nil {
			return rep, err
		}
		text := map[int]string{}
		for _, pg := range stored {
			text[pg.Page] = pg.Text
		}
		images := map[int]string{}
		tmp := "" // rendered pages of this original, removed after its checks
		if p.MIME == "application/pdf" {
			imgs, err := s.renderForCheck(ctx, path, len(stored))
			if err != nil {
				rep.CheckErrors = append(rep.CheckErrors, fmt.Sprintf("%s: %v", p.SHA256[:12], err))
				continue
			}
			for i, im := range imgs {
				images[i+1] = im
			}
			tmp = filepath.Dir(imgs[0])
		} else {
			images[1] = path
		}
		var (
			mu  sync.Mutex
			wg  sync.WaitGroup
			sem = make(chan struct{}, o.Workers)
		)
		for _, n := range byOrig[p.SHA256] {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				k, err := s.check(ctx, p.SHA256, n, images[n], text[n])
				mu.Lock()
				s.count(rep, p.SHA256, n, k, err)
				mu.Unlock()
			}()
		}
		wg.Wait()
		if tmp != "" {
			os.RemoveAll(tmp)
		}
		rep.Done++
	}
	rep.Duration = time.Since(start)
	return rep, nil
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
	if pages.TextTypes[p.MIME] || pages.OfficeTypes[p.MIME] != nil {
		return s.text(ctx, o, p, rep)
	}
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
		page       int
		text       string // text layer, if meaningful
		image      []byte // OCR input otherwise
		checkImage string // full-resolution image for the second reading
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
		var images, checkImages []string
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
			if s.Checker != nil {
				checkImages, err = s.renderForCheck(ctx, path, pageCount)
				if err != nil {
					rep.CheckErrors = append(rep.CheckErrors, fmt.Sprintf("%s: %v", p.SHA256[:12], err))
					checkImages = nil // OCR goes on; verify retries the check later
				} else {
					defer os.RemoveAll(filepath.Dir(checkImages[0]))
				}
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
				tk := task{page: n, image: img}
				if checkImages != nil {
					tk.checkImage = checkImages[i]
				}
				tasks = append(tasks, tk)
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
			tasks = append(tasks, task{page: 1, image: img, checkImage: path}) // tesseract reads the original
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
			if pg.Method == "ocr" && s.Checker != nil && t.checkImage != "" {
				mu.Unlock() // tesseract takes seconds; do not hold the lock
				k, err := s.check(ctx, p.SHA256, t.page, t.checkImage, pg.Text)
				mu.Lock()
				s.count(rep, p.SHA256, t.page, k, err)
			}
		}()
	}
	wg.Wait()
	if firstErr != "" {
		return catalog.ExtractError, pageCount, firstErr
	}
	return catalog.ExtractDone, pageCount, ""
}

// text stores a plain text file or an office document as pages: the file
// holds its text, so there is nothing to recognise and nothing to pay.
func (s *Stage) text(ctx context.Context, o Options, p catalog.Pending, rep *Report) (status string, pageCount int, errText string) {
	path, err := readable(p)
	if err != nil {
		return catalog.ExtractError, 0, err.Error()
	}
	read, engine := pages.ReadText, "file"
	if r := pages.OfficeTypes[p.MIME]; r != nil {
		read, engine = r, p.MIME
	}
	texts, err := read(path)
	if errors.Is(err, pages.ErrNoCatdoc) || errors.Is(err, pages.ErrNoXlrd) {
		return catalog.ExtractError, 0, err.Error() // retried once the tools are installed
	}
	if err != nil {
		return catalog.ExtractUnsupported, 0, err.Error()
	}
	if o.DryRun {
		rep.PagesTextLayer += len(texts)
		return catalog.ExtractDone, len(texts), ""
	}
	for i, t := range texts {
		pg := catalog.Page{SHA256: p.SHA256, Page: i + 1, Pipeline: Pipeline, Method: "text-layer", Engine: engine, Text: t}
		if err := s.Catalog.SavePage(ctx, pg, s.Now()); err != nil {
			return catalog.ExtractError, len(texts), err.Error()
		}
		rep.PagesTextLayer++
	}
	return catalog.ExtractDone, len(texts), ""
}
