// Package describe names each document in a few words for lists and reads
// its characteristics — kind, the author's specialty, kind of organisation.
// The model reads the summary and the essential text, which names the author
// by position; the title and values are checked like a summary (names of the
// document and of the account's persons, organisations), so they carry no
// more personal data than the summary does. Index makes them characteristics
// with source "model", below the owner's, an assistant's and a batch's.
package describe

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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

// Keys are the characteristics the model reads, in the order asked.
var Keys = []string{"вид", "специальность", "организация"}

// maxValueRunes bounds a characteristic value: a longer one is a sentence,
// not a value.
const maxValueRunes = 40

// Prompt is general on purpose: the library holds any documents, medicine is
// only the first example.
const Prompt = `Ниже резюме документа и его суть (текст без служебных строк). Ответь только JSON:
{"title": "...", "вид": "...", "специальность": "..." или null, "специальность_догадка": true или false, "организация": "..." или null}
- title — короткое название для списка, 2–6 слов: вид документа и, если есть, кто его составил по специальности или о чём он: «Рекомендации уролога по лечению», «Суточное мониторирование ЭКГ», «Протокол гастроскопии», «Дополнительное соглашение к договору», «Выписка по счёту». Специальность в title — только если она написана в документе, не догадка. Без даты, без слов «это», «документ», «оформленный», «выданный», без организации и её типа, без имён и номеров. С большой буквы, без точки и кавычек.
- вид — вид документа, 1–3 слова с маленькой буквы, как раздел в картотеке: «рекомендации», «протокол исследования», «заключение», «анализ», «выписка», «договор», «дополнительное соглашение», «счёт», «чек», «справка», «направление», «рецепт».
- специальность — специальность врача или специалиста, который составил документ или провёл исследование, одним-двумя словами с маленькой буквы: «уролог», «гастроэнтеролог», «кардиолог»; для исследований — по их виду: «врач ультразвуковой диагностики» (УЗИ, УЗДС), «врач функциональной диагностики» (ЭКГ, холтер, СМАД), «эндоскопист», «рентгенолог» (рентген, КТ, МРТ), «патологоанатом» (гистология). Ищи её в подписи, должности автора, названии приёма или отделения («Приём уролога», «Врач-эндокринолог»). Врачи, к которым документ направляет («консультация гастроэнтеролога»), — не автор. Если автор назван — "специальность_догадка": false. Если специальность автора не написана, но по содержанию видно, какой специалист составил документ (назначения по диабету и повторная явка к эндокринологу — эндокринолог), дай её и поставь "специальность_догадка": true. Если и по содержанию не ясно — null.
- организация — только тип организации, откуда документ, с маленькой буквы: «частная клиника», «государственная больница», «лаборатория», «банк», «страховая компания»; null, если не назван.
Не додумывай: чего нет в тексте, то null. Никаких имён, фамилий и названий организаций ни в одном поле.
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

// Run describes every document that has a summary and no title. Documents go
// in groups, and each group sees the values the earlier ones were given, so
// the same kind of document gets the same value.
func (s *Stage) Run(ctx context.Context, o Options) (*Report, error) {
	start := time.Now()
	if o.Model == "" {
		o.Model = DefaultModel
	}
	if o.Workers < 1 {
		o.Workers = 1
	}
	docs, err := s.Catalog.PendingDescriptions(ctx, o.Limit)
	if err != nil {
		return nil, err
	}
	extra, err := layers.AccountForbidden(ctx, s.Catalog)
	if err != nil {
		return nil, err
	}
	rep := &Report{Documents: len(docs)}
	var mu sync.Mutex
	for len(docs) > 0 && ctx.Err() == nil {
		group := docs[:min(len(docs), o.Workers*2)]
		docs = docs[len(group):]
		known, err := s.Catalog.ModelAttrValues(ctx)
		if err != nil {
			return rep, err
		}
		prompt := Prompt + usedValues(known)
		var wg sync.WaitGroup
		sem := make(chan struct{}, o.Workers)
		for _, d := range group {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				desc, cost, err := s.one(ctx, o, prompt, d, layers.Forbidden(d.Essential, extra...))
				if err == nil {
					err = s.Catalog.SaveDescription(ctx, d.DocID, d.Summary, desc)
				}
				mu.Lock()
				defer mu.Unlock()
				rep.CostUSD += cost
				if err != nil {
					rep.Failed++
					rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %v", d.DocID, err))
					return
				}
				rep.Done++
			}()
		}
		wg.Wait()
	}
	rep.Duration = time.Since(start)
	return rep, ctx.Err()
}

// essentialRunes bounds the essential text sent along: the author's
// position is near the top or at the signature, and the rest only costs.
const essentialRunes = 6000

// one describes a document: one retry when the answer leaks a name, then
// whatever still leaks is dropped — the title falls back to the summary's
// first sentence, a value is left out.
func (s *Stage) one(ctx context.Context, o Options, prompt string, d catalog.DescribeInput, forbidden []string) (catalog.Description, float64, error) {
	ess := d.Essential
	if r := []rune(ess); len(r) > essentialRunes {
		ess = string(r[:essentialRunes/2]) + "\n…\n" + string(r[len(r)-essentialRunes/2:])
	}
	input := "\nРезюме:\n" + d.Summary + "\n\nСуть:\n" + ess
	var cost float64
	var desc catalog.Description
	var leaks []string
	for attempt := 0; attempt < 2; attempt++ {
		p := prompt
		if attempt == 1 {
			p += "\nПредыдущий ответ содержал имена или названия: «" + strings.Join(leaks, "», «") + "». Ответь заново без них.\n"
		}
		reply, err := s.Model.Chat(ctx, router.ChatRequest{Account: o.Account, Model: o.Model, Text: p + input})
		cost += reply.CostUSD
		if err != nil {
			return desc, cost, err
		}
		if desc, err = Parse(reply.Text); err != nil {
			return desc, cost, err
		}
		leaks = layers.Leaks(desc.Title+" "+joinValues(desc.Attrs), forbidden)
		if len(leaks) == 0 {
			return desc, cost, nil
		}
	}
	if len(layers.Leaks(desc.Title, forbidden)) > 0 {
		desc.Title = FromSummary(d.Summary)
	}
	for k, vs := range desc.Attrs {
		if len(layers.Leaks(strings.Join(vs, " "), forbidden)) > 0 {
			delete(desc.Attrs, k)
			delete(desc.Guessed, k)
		}
	}
	return desc, cost, nil
}

func joinValues(attrs map[string][]string) string {
	var all []string
	for _, vs := range attrs {
		all = append(all, vs...)
	}
	return strings.Join(all, " ")
}

// FromSummary stands in for a title: the first sentence of the summary,
// which names the document (ADR-0005), without its leading «Это».
func FromSummary(summary string) string {
	s := strings.TrimSpace(summary)
	for i := 0; i+1 < len(s); i++ {
		if (s[i] == '.' || s[i] == '\n') && (s[i+1] == ' ' || s[i+1] == '\n') {
			s = s[:i]
			break
		}
	}
	if rest, ok := strings.CutPrefix(s, "Это "); ok {
		s = rest
	}
	return Clean(s)
}

// usedValues lists the values given so far, for the model to reuse.
func usedValues(known map[string][]string) string {
	var b strings.Builder
	for _, k := range Keys {
		vs := known[k]
		if len(vs) == 0 {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("Значения, которые уже есть в библиотеке; если подходит, бери в точности такое же:\n")
		}
		fmt.Fprintf(&b, "- %s: %s\n", k, strings.Join(vs[:min(len(vs), 30)], "; "))
	}
	return b.String()
}

var jsonObject = regexp.MustCompile(`(?s)\{.*\}`)

// Parse reads the model's answer: a title, the characteristics it named and
// which of them are guesses.
// Guessable are the keys the model may give as a guess from the content
// («<key>_догадка»: true); the guess is stored apart and shown as one.
var Guessable = []string{"специальность"}

func Parse(answer string) (catalog.Description, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonObject.FindString(answer)), &raw); err != nil {
		return catalog.Description{}, fmt.Errorf("ответ не JSON: %.80q", answer)
	}
	t, _ := raw["title"].(string)
	title := Clean(t)
	if title == "" {
		return catalog.Description{}, fmt.Errorf("нет названия: %.80q", answer)
	}
	attrs := map[string][]string{}
	for _, k := range Keys {
		v, _ := raw[k].(string)
		if k != "вид" && properName(v) {
			continue
		}
		if v = value(v); v != "" {
			attrs[k] = []string{v}
		}
	}
	guessed := map[string]bool{}
	for _, k := range Guessable {
		if g, _ := raw[k+"_догадка"].(bool); g && attrs[k] != nil {
			guessed[k] = true
		}
	}
	return catalog.Description{Title: title, Attrs: attrs, Guessed: guessed}, nil
}

// properName says whether a value has a word after the first written like a
// name («клиника Медси»): a specialty or a kind of organisation never has
// one; abbreviations («орган ЗАГС») are not names. The first word may be
// capitalised by habit; a name standing alone is caught as a leak.
func properName(v string) bool {
	ws := strings.Fields(v)
	for _, w := range ws[min(1, len(ws)):] {
		r := []rune(strings.TrimFunc(w, func(c rune) bool { return !unicode.IsLetter(c) }))
		if len(r) > 1 && unicode.IsUpper(r[0]) && unicode.IsLower(r[1]) {
			return true
		}
	}
	return false
}

// value tidies a characteristic: one line, no quotes or final period, a
// lower-case first letter unless the word is an abbreviation («ЭКГ»).
func value(v string) string {
	v = strings.Trim(strings.TrimSpace(v), "«»\"'“”. ")
	switch strings.ToLower(v) {
	case "", "null", "нет", "не указано", "не указана", "неизвестно":
		return ""
	}
	if strings.ContainsAny(v, "\n;") || utf8.RuneCountInString(v) > maxValueRunes {
		return ""
	}
	r := []rune(v)
	if len(r) > 1 && unicode.IsUpper(r[0]) && unicode.IsLower(r[1]) {
		r[0] = unicode.ToLower(r[0])
	}
	return string(r)
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
