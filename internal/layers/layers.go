package layers

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/router"
)

// Pipeline is the version of this stage.
const Pipeline = 1

// DefaultModel was chosen in ADR-0005 after measuring four models.
const DefaultModel = "openai/gpt-6-luna"

// EssentialPrompt asks for line numbers to drop, never for rewritten text:
// the essential text is the kept lines, so it cannot be reworded (ADR-0005).
const EssentialPrompt = `Ниже текст документа, распознанный со скана. Строки пронумерованы.
Найди строки, которые не несут смысла для читателя документа, и только их:
- служебные пометки бланка («Бланк разработан на основании формы…», лицензии, ОГРН, ИНН);
- реклама и акции, призывы записаться;
- контакты: сайты, телефоны для записи, почта, мессенджеры, адреса организации в колонтитулах;
- шапки и колонтитулы, повторённые на второй и следующих страницах;
- цифры штрихкодов, служебные строки аппаратов на распечатках (MI, TIS, частота, масштаб);
- пустые поля бланка (подчёркивания, «___»).
Не трогай строки с содержанием документа: данными, результатами, назначениями, суммами, датами, ФИО, первое упоминание организации. Сомневаешься — оставь.
Ответь только JSON: {"remove": [[номер строки, "категория"], ...]}

`

// SummaryPrompt asks for a de-identified summary (ADR-0005).
const SummaryPrompt = `Ниже текст документа. Напиши его резюме на русском, 3–8 предложений.
Первое предложение: какой это документ и от какой даты.
Дальше: суть документа — что делали, что нашли, диагнозы, назначения, суммы, ключевые значения с единицами, как в документе.
Строгие правила о персональных данных:
- человека, о котором документ, называй «пациент» или «клиент», без имени, фамилии, инициалов и даты рождения;
- врачей и сотрудников называй только по должности или специальности («кардиолог»), без фамилий;
- организацию называй только по типу («частная клиника», «областная больница», «банк»), без названия, сокращения, адреса, города и телефонов;
- никаких номеров карт, случаев, договоров, полисов, счетов, штрихкодов.
Не добавляй ничего, чего нет в тексте. Если даты документа в тексте нет, так и напиши. Ответь только текстом резюме.

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
	Documents          int
	Done               int
	Flagged            int // a summary check still failed after the retry
	Failed             int
	RestoredRepeats    int
	RestoredSuspicious int
	CharsBefore        int
	CharsAfter         int
	CostUSD            float64
	Notes              []string
	Duration           time.Duration
}

// Stage holds the stage's dependencies.
type Stage struct {
	Catalog *catalog.Catalog
	Model   router.Chatter
	Now     func() time.Time
}

// Run builds the text layers of pending documents.
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
	docs, err := s.Catalog.PendingTexts(ctx, o.Limit)
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
			t := s.one(ctx, o, d)
			err := s.Catalog.SaveTexts(ctx, t, s.Now())
			mu.Lock()
			defer mu.Unlock()
			rep.CostUSD += t.CostUSD
			rep.RestoredRepeats += t.RestoredRepeats
			rep.RestoredSuspicious += t.RestoredSuspicious
			switch {
			case err != nil:
				rep.Failed++
				rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %v", d.ID, err))
			case t.Status == catalog.TextsError:
				rep.Failed++
				rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %s", d.ID, t.Error))
			default:
				rep.Done++
				rep.CharsBefore += t.CharsBefore
				rep.CharsAfter += len([]rune(t.Essential))
				if t.Status == catalog.TextsFlagged {
					rep.Flagged++
					rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %s", d.ID, t.Flags))
				}
			}
		}()
	}
	wg.Wait()
	rep.Duration = time.Since(start)
	return rep, ctx.Err()
}

func (s *Stage) one(ctx context.Context, o Options, d catalog.Document) catalog.Texts {
	t := catalog.Texts{DocID: d.ID, Pipeline: Pipeline, Model: o.Model}
	fail := func(err error) catalog.Texts {
		t.Status, t.Error = catalog.TextsError, err.Error()
		return t
	}
	ps, err := s.Catalog.Pages(ctx, d.SHA256)
	if err != nil {
		return fail(err)
	}
	var b strings.Builder
	for _, p := range ps {
		if p.Page >= d.FirstPage && p.Page <= d.LastPage {
			b.WriteString(p.Text)
			b.WriteString("\n")
		}
	}
	verbatim := b.String()
	var lines []string
	for _, l := range strings.Split(verbatim, "\n") {
		if l = strings.TrimSpace(l); strings.Trim(l, "|-_ ") != "" {
			lines = append(lines, l)
		}
	}
	t.CharsBefore = len([]rune(strings.Join(lines, "\n")))
	cleaned := RulesClean(lines)
	if len(cleaned) == 0 {
		t.Status, t.Essential, t.Summary = catalog.TextsDone, "", "Документ без текста."
		return t
	}

	// Essential text: the model names lines to drop; the safeguards decide.
	var nb strings.Builder
	nb.WriteString(EssentialPrompt)
	for i, l := range cleaned {
		fmt.Fprintf(&nb, "%d: %s\n", i+1, l)
	}
	reply, err := s.Model.Chat(ctx, router.ChatRequest{Account: o.Account, Model: o.Model, Text: nb.String()})
	t.CostUSD += reply.CostUSD
	if err != nil {
		return fail(fmt.Errorf("essential: %w", err))
	}
	drop, err := parseRemove(reply.Text, len(cleaned))
	if err != nil {
		return fail(fmt.Errorf("essential: %w", err))
	}
	kept, rr, rs := Essential(cleaned, drop)
	t.Essential = strings.Join(kept, "\n")
	t.RestoredRepeats, t.RestoredSuspicious = rr, rs
	removed, _ := json.Marshal(removedLines(cleaned, drop, kept))
	t.Removed = string(removed)

	// Summary from the rule-cleaned text (the measured input), checked; one
	// retry, then the document is flagged for the owner.
	forbidden := Forbidden(verbatim)
	source := strings.Join(cleaned, "\n")
	var flags []string
	for attempt := 0; attempt < 2; attempt++ {
		prompt := SummaryPrompt + source
		if attempt == 1 {
			prompt = SummaryPrompt + "Предыдущее резюме нарушило правила: " + strings.Join(flags, "; ") +
				". Напиши заново, строго по правилам.\n\n" + source
		}
		reply, err := s.Model.Chat(ctx, router.ChatRequest{Account: o.Account, Model: o.Model, Text: prompt})
		t.CostUSD += reply.CostUSD
		if err != nil {
			return fail(fmt.Errorf("summary: %w", err))
		}
		t.Summary = strings.TrimSpace(reply.Text)
		flags = nil
		for _, l := range Leaks(t.Summary, forbidden) {
			flags = append(flags, "утечка «"+l+"»")
		}
		for _, n := range InventedNumbers(t.Summary, verbatim) {
			flags = append(flags, "нет в тексте «"+n+"»")
		}
		if len(flags) == 0 || t.Summary == "" {
			break
		}
	}
	t.Status = catalog.TextsDone
	if t.Summary == "" {
		flags = append(flags, "пустое резюме")
	}
	if len(flags) > 0 {
		t.Status, t.Flags = catalog.TextsFlagged, strings.Join(flags, "; ")
	}
	return t
}

var jsonObject = regexp.MustCompile(`(?s)\{.*\}`)

// parseRemove reads {"remove": [[n, "category"], ...]}; bare numbers are
// accepted too. Numbers outside the text are ignored.
func parseRemove(text string, lines int) (map[int]string, error) {
	m := jsonObject.FindString(text)
	if m == "" {
		return nil, fmt.Errorf("model reply has no JSON object")
	}
	var v struct {
		Remove []json.RawMessage `json:"remove"`
	}
	if err := json.Unmarshal([]byte(m), &v); err != nil {
		return nil, fmt.Errorf("model reply: %w", err)
	}
	out := map[int]string{}
	for _, raw := range v.Remove {
		var pair []json.RawMessage
		var n int
		cat := ""
		if json.Unmarshal(raw, &pair) == nil && len(pair) > 0 {
			if json.Unmarshal(pair[0], &n) != nil {
				continue
			}
			if len(pair) > 1 {
				_ = json.Unmarshal(pair[1], &cat)
			}
		} else if json.Unmarshal(raw, &n) != nil {
			continue
		}
		if n >= 1 && n <= lines {
			out[n] = cat
		}
	}
	return out, nil
}

type removed struct {
	Line     int    `json:"line"`
	Category string `json:"category"`
	Text     string `json:"text"`
}

// removedLines lists what the essential text left out, for the owner to audit.
func removedLines(lines []string, drop map[int]string, kept []string) []removed {
	keptCount := map[string]int{}
	for _, k := range kept {
		keptCount[k]++
	}
	var out []removed
	for i, l := range lines {
		if keptCount[l] > 0 {
			keptCount[l]--
			continue
		}
		out = append(out, removed{Line: i + 1, Category: drop[i+1], Text: l})
	}
	return out
}
