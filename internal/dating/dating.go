// Package dating reads each document's creation date (SPEC 8): when it was
// written, issued or signed, not the date of what it refers to. One model call
// names the date and quotes the text it read it from; the date is used only
// when the quote occurs in the document and holds that very date. A date
// without a year or a day stays partial: the missing part is never guessed.
package dating

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/layers"
	"github.com/wildcar/librarian/internal/router"
)

// Pipeline is the version of this stage; a higher one rereads every document.
// 2: a quote's edge punctuation no longer decides whether it occurs.
// 3: a date at the end of a record or next to a signature is the creation date.
// 4: a year the model dropped is taken back from its quote.
const Pipeline = 4

// DefaultModel is the text-layer model (ADR-0005): it already reads these texts.
const DefaultModel = layers.DefaultModel

// Prompt is general on purpose: the library holds any documents, medicine is
// only the first example.
const Prompt = `Ниже текст документа. Найди дату создания документа: когда его составили, выдали, подписали, оформили или распечатали.
- Это не дата события, на которое документ ссылается (запись на приём, госпитализация, поездка, рейс, начало или конец срока действия, срок оплаты), не дата рождения, не дата лицензии, приказа или формы бланка.
- Если документ сам фиксирует событие (протокол исследования, осмотр, заключение, чек, акт) и отдельной даты оформления нет, дата этого события и есть дата создания.
- Дата, написанная в конце записи или рядом с подписью, — это дата создания документа, даже если печатное поле даты бланка пустое и даже если дата стоит не в своём поле. При распознавании подпись часто видна как «[подпись]», «[?]» или неразборчивый текст.
- Если дат оформления несколько (составлен и распечатан), бери дату составления.
- Если у даты нет года или дня, верни сколько есть: ГГГГ-ММ, ГГГГ или --ММ-ДД. Не угадывай недостающее, не бери год из других дат документа.
Ответь только JSON: {"date": "ГГГГ-ММ-ДД", "quote": "фрагмент текста ровно как в документе, где написана эта дата", "basis": "stated"}
basis: "stated" — дата названа датой документа или стоит в его реквизитах («от», «дата», в конце записи, рядом с подписью); "event" — это дата события, которое документ фиксирует.
Если даты создания в тексте нет, ответь {"date": null}.

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
	Dated     int // full date confirmed
	Partial   int // confirmed, without a day or a year
	None      int
	Rejected  int
	Failed    int
	CostUSD   float64
	Notes     []string
	Duration  time.Duration
}

// Stage holds the stage's dependencies.
type Stage struct {
	Catalog *catalog.Catalog
	Model   router.Chatter
	Now     func() time.Time
}

// Run reads the creation date of every pending document.
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
	docs, err := s.Catalog.PendingDates(ctx, Pipeline, o.Limit)
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
			r := s.one(ctx, o, d)
			err := s.Catalog.SaveDocDate(ctx, r, s.Now())
			mu.Lock()
			defer mu.Unlock()
			rep.CostUSD += r.CostUSD
			switch {
			case err != nil:
				rep.Failed++
				rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %v", d.DocID, err))
			case r.Status == catalog.DateError:
				rep.Failed++
				rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %s", d.DocID, r.Reason))
			case r.Status == catalog.DateRejected:
				rep.Rejected++
				rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %s", d.DocID, r.Reason))
			case r.Status == catalog.DateNone:
				rep.None++
			case len(r.Date) == len("2006-01-02") && r.Date[0] != '-':
				rep.Dated++
			default:
				rep.Partial++
			}
		}()
	}
	wg.Wait()
	rep.Duration = time.Since(start)
	return rep, ctx.Err()
}

type answer struct {
	Date  *string `json:"date"`
	Quote string  `json:"quote"`
	Basis string  `json:"basis"`
}

var jsonObject = regexp.MustCompile(`(?s)\{.*\}`)

func (s *Stage) one(ctx context.Context, o Options, d catalog.DateInput) catalog.DocDate {
	r := catalog.DocDate{DocID: d.DocID, Model: o.Model, Pipeline: Pipeline}
	if strings.TrimSpace(d.Essential) == "" {
		r.Status = catalog.DateNone
		return r
	}
	reply, err := s.Model.Chat(ctx, router.ChatRequest{Account: o.Account, Model: o.Model, Text: Prompt + d.Essential})
	r.CostUSD = reply.CostUSD
	if err != nil {
		r.Status, r.Reason = catalog.DateError, err.Error()
		return r
	}
	var a answer
	if err := json.Unmarshal([]byte(jsonObject.FindString(reply.Text)), &a); err != nil {
		r.Status, r.Reason = catalog.DateError, fmt.Sprintf("ответ не JSON: %.80q", reply.Text)
		return r
	}
	if a.Date == nil || strings.TrimSpace(*a.Date) == "" {
		r.Status = catalog.DateNone
		return r
	}
	r.Date, r.Quote, r.Basis = strings.TrimSpace(*a.Date), strings.TrimSpace(a.Quote), a.Basis
	if r.Basis != "stated" && r.Basis != "event" {
		r.Basis = ""
	}
	r.Date = Complete(r.Date, r.Quote)
	if err := Confirm(r.Date, r.Quote, d.Essential); err != nil {
		r.Status, r.Reason = catalog.DateRejected, err.Error()
		return r
	}
	r.Status = catalog.DateDone
	return r
}

// Complete fills in the year a model dropped from a --MM-DD date when its
// quote holds exactly one full date and that date has the same day and
// month («29. X. 19г.» answered as --10-29). The year comes from the quote,
// so nothing is guessed; any other date is returned unchanged.
func Complete(date, quote string) string {
	m := monthDay.FindStringSubmatch(date)
	if m == nil {
		return date
	}
	ds := layers.Dates(quote)
	if len(ds) != 1 || ds[0][0] != atoi(m[2]) || ds[0][1] != atoi(m[1]) {
		return date
	}
	year := 2000 + ds[0][2]
	if year > time.Now().Year()+1 {
		year -= 100
	}
	return fmt.Sprintf("%04d-%s-%s", year, m[1], m[2])
}

var (
	fullDate  = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	yearMonth = regexp.MustCompile(`^(\d{4})-(\d{2})$`)
	yearOnly  = regexp.MustCompile(`^(\d{4})$`)
	monthDay  = regexp.MustCompile(`^--(\d{2})-(\d{2})$`)
)

// Confirm checks a model's date against its quote and the text: the quote
// must occur in the text (spacing, case and edge punctuation aside) and must
// hold the date, with exactly the parts the date has.
func Confirm(date, quote, text string) error {
	quote = strings.TrimFunc(quote, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) })
	if quote == "" {
		return errors.New("нет цитаты")
	}
	if !strings.Contains(flat(text), flat(quote)) {
		return fmt.Errorf("цитаты «%s» нет в тексте", quote)
	}
	if !holds(date, quote) {
		return fmt.Errorf("в цитате «%s» нет даты %s", quote, date)
	}
	return nil
}

func holds(date, quote string) bool {
	tokens := strings.FieldsFunc(strings.ToLower(quote), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	has := func(ok func(string) bool) bool {
		for _, t := range tokens {
			if ok(t) {
				return true
			}
		}
		return false
	}
	num := func(n int) func(string) bool {
		return func(t string) bool { v, err := strconv.Atoi(t); return err == nil && v == n }
	}
	month := func(m int) func(string) bool {
		return func(t string) bool { return layers.MonthOf(t) == m }
	}
	// A year is written in full, or as two digits right before "г" ("19г.").
	year := func(y int) func(string) bool {
		return func(t string) bool {
			return t == strconv.Itoa(y) || t == fmt.Sprintf("%02dг", y%100)
		}
	}
	switch {
	case fullDate.MatchString(date):
		m := fullDate.FindStringSubmatch(date)
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return false // 2019-02-31 and the like
		}
		y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
		for _, k := range layers.Dates(quote) {
			if k[0] == d && k[1] == mo && k[2] == y%100 {
				return true
			}
		}
		return false
	case yearMonth.MatchString(date):
		m := yearMonth.FindStringSubmatch(date)
		return has(year(atoi(m[1]))) && has(month(atoi(m[2])))
	case yearOnly.MatchString(date):
		return has(year(atoi(date)))
	case monthDay.MatchString(date):
		m := monthDay.FindStringSubmatch(date)
		mo, d := atoi(m[1]), atoi(m[2])
		if mo < 1 || mo > 12 || d < 1 || d > 31 {
			return false
		}
		// A full date in the quote means the model dropped a year it had.
		return has(num(d)) && has(month(mo)) && len(layers.Dates(quote)) == 0
	}
	return false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func flat(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
