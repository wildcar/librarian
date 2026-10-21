package dating_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/dating"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/router"
)

func TestConfirm(t *testing.T) {
	text := `КОНСУЛЬТАТИВНОЕ ЗАКЛЮЧЕНИЕ
Обследование по м/ж
29. X. 19г.
Отчёт оформлен 07.12.2019, обследование 06.12.2019
09/06/2026 11:48:47
В ПРИЕМНЫЙ ПОКОЙ ( 1 ЭТАЖ ) К 11.00
8 октября
Счёт за декабрь 2023 г.`
	for _, tc := range []struct {
		date, quote string
		ok          bool
	}{
		{"2019-10-29", "29. X. 19г.", true},              // Roman month, two-digit year
		{"2019-12-07", "оформлен 07.12.2019", true},      // the creation date, not the exam
		{"2019-12-07", "обследование 06.12.2019", false}, // the quote holds another date
		{"2019-12-07", "оформлен 07.12.2020", false},     // not in the text
		{"--10-08", "8 октября", true},                   // no year: stays partial
		{"2026-10-08", "8 октября", false},               // a guessed year is refused
		{"--12-07", "оформлен 07.12.2019", false},        // a dropped year is refused
		{"2023-12", "Счёт за декабрь 2023 г.", true},
		{"2023", "декабрь 2023", true},
		{"2019-02-31", "31.02.2019", false},
		{"2019-12-07", "", false},
		{"2019-12-07", "оформлен 07.12.2019.", true}, // a period the text does not have
		{"2026-09-06", "09/06/2026", false},          // read month first: the text means 9 June
	} {
		err := dating.Confirm(tc.date, tc.quote, text)
		if (err == nil) != tc.ok {
			t.Errorf("Confirm(%q, %q) = %v, want ok=%v", tc.date, tc.quote, err, tc.ok)
		}
	}
}

type scripted map[string]string // a word of the text → the model's reply

func (s scripted) Chat(_ context.Context, r router.ChatRequest) (router.ChatReply, error) {
	for k, v := range s {
		if strings.Contains(r.Text, k) {
			return router.ChatReply{Text: v, CostUSD: 0.0001}, nil
		}
	}
	return router.ChatReply{Text: `{"date": null}`}, nil
}

func TestStageAndIndex(t *testing.T) {
	ctx := context.Background()
	a, err := account.Create(t.TempDir(), "acc")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	now := time.Now()
	docs := map[string][2]string{ // name → summary, essential
		"referral": {"Направление от 12.09.2026 на приём 08.10.2026.", "Направление\nВыдано 12.09.2026\nПриём 08.10.2026"},
		"memo":     {"Памятка от 8 октября; год не указан.", "Памятка\n8 октября"},
		"photo":    {"Фотография инструкции.", "Чайник"},
		"wrong":    {"Справка от 01.02.2020.", "Справка\nДата 01.02.2020"},
	}
	ids := map[string]string{}
	for i, name := range []string{"referral", "memo", "photo", "wrong"} {
		sha := strings.Repeat(string(rune('a'+i)), 64)
		if _, err := c.RecordSeen(ctx, catalog.Seen{URI: "file:///" + name, SHA256: sha, Size: 1, MTime: now, MIME: "application/pdf", At: now}); err != nil {
			t.Fatal(err)
		}
		if err := c.SavePage(ctx, catalog.Page{SHA256: sha, Page: 1, Method: "ocr", Engine: "t", Text: docs[name][1], Pipeline: 1}, now); err != nil {
			t.Fatal(err)
		}
		if err := c.SaveSplit(ctx, catalog.SplitResult{SHA256: sha, Status: catalog.SplitDone, Method: "single", Engine: "-", Pipeline: 1, Starts: []int{1}, Pages: 1}, now); err != nil {
			t.Fatal(err)
		}
		ids[name] = catalog.DocumentID(sha, 1)
		if err := c.SaveTexts(ctx, catalog.Texts{DocID: ids[name], Status: catalog.TextsDone, Summary: docs[name][0], Essential: docs[name][1], Model: "t", Pipeline: 1}, now); err != nil {
			t.Fatal(err)
		}
	}
	// Before the date stage, the summary stands in, and its two dates leave the referral undated.
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	if d, _, _, _ := c.DocDateOf(ctx, ids["referral"]); d != "" {
		t.Errorf("referral before the stage: %q", d)
	}

	st := &dating.Stage{Catalog: c, Model: scripted{
		"Направление": `{"date": "2026-09-12", "quote": "Выдано 12.09.2026", "basis": "stated"}`,
		"Памятка":     "Ответ: {\"date\": \"--10-08\", \"quote\": \"8 октября\", \"basis\": \"stated\"}",
		"Справка":     `{"date": "2020-02-02", "quote": "Дата 01.02.2020", "basis": "stated"}`,
	}}
	rep, err := st.Run(ctx, dating.Options{Model: "m", Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Dated != 1 || rep.Partial != 1 || rep.None != 1 || rep.Rejected != 1 {
		t.Errorf("report: %+v", rep)
	}
	if again, _ := c.PendingDates(ctx, dating.Pipeline, 0); len(again) != 0 {
		t.Errorf("still pending after the run: %v", again)
	}
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][3]string{
		"referral": {"2026-09-12", "", "model:stated"},
		"memo":     {"", "--10-08", "model:stated"},
		"photo":    {"", "", ""},
		"wrong":    {"2020-02-01", "", "summary"}, // a rejected answer falls back to the summary
	} {
		d, p, s, err := c.DocDateOf(ctx, ids[name])
		if err != nil || [3]string{d, p, s} != want {
			t.Errorf("%s: %q %q %q %v, want %v", name, d, p, s, err, want)
		}
	}
	if r, _ := c.DocReview(ctx, ids["wrong"]); len(r) != 1 || !strings.HasPrefix(r[0], "дата:") {
		t.Errorf("rejected date review: %v", r)
	}
	// A partial date is no date for date filters.
	hits, _, err := c.Search(ctx, catalog.SearchQuery{Missing: []string{catalog.MissingDate}})
	if err != nil || len(hits) != 2 {
		t.Errorf("undated: %v %v", hits, err)
	}
}
