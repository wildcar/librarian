// Package report renders catalog facts for a human. User-facing text is Russian.
package report

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/dating"
	"github.com/wildcar/librarian/internal/extract"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/intake"
	"github.com/wildcar/librarian/internal/layers"
	"github.com/wildcar/librarian/internal/split"
)

// ExtractRun prints the result of one extraction run.
func ExtractRun(w io.Writer, r *extract.Report, dryRun bool) {
	if dryRun {
		fmt.Fprintf(w, "Оригиналов без текста: %d. Страниц: текстовый слой %d, нужен OCR %d, уже готово %d.\n",
			r.Originals, r.PagesTextLayer, r.PagesOCR, r.PagesSkipped)
		fmt.Fprintf(w, "Оценка стоимости OCR: $%.2f (по $%.4f за страницу).\n", r.EstimateUSD, extract.EstimateUSDPerPage)
		return
	}
	fmt.Fprintf(w, "Оригиналов: %d, готово %d, неподдерживаемых типов %d, с ошибкой %d\n",
		r.Originals, r.Done, r.Unsupported, r.Failed)
	fmt.Fprintf(w, "Страниц: текстовый слой %d, OCR %d, уже были готовы %d. OCR стоил $%.3f. Время: %s\n",
		r.PagesTextLayer, r.PagesOCR, r.PagesSkipped, r.CostUSD, r.Duration.Round(1e9))
	for _, e := range r.Errors {
		fmt.Fprintf(w, "  ошибка %s\n", e)
	}
	checks(w, r)
}

// VerifyRun prints the result of a second-reading backfill.
func VerifyRun(w io.Writer, r *extract.Report) {
	fmt.Fprintf(w, "Оригиналов: %d. Время: %s\n", r.Originals, r.Duration.Round(1e9))
	checks(w, r)
}

func checks(w io.Writer, r *extract.Report) {
	if r.PagesChecked > 0 || len(r.CheckErrors) > 0 {
		fmt.Fprintf(w, "Вторая читка (tesseract): проверено страниц %d, расхождений %d\n", r.PagesChecked, r.PagesFlagged)
	}
	for _, e := range r.CheckErrors {
		fmt.Fprintf(w, "  ошибка сверки %s\n", e)
	}
}

// FlaggedPages lists pages where the two readings disagree.
func FlaggedPages(w io.Writer, fl []catalog.Flagged) {
	if len(fl) == 0 {
		return
	}
	fmt.Fprintf(w, "Страницы на проверку, чтения расходятся: %d\n", len(fl))
	for _, f := range fl {
		missing := f.Missing
		if r := []rune(missing); len(r) > 120 {
			missing = string(r[:120]) + "…"
		}
		fmt.Fprintf(w, "  %s стр. %d: найдено %d из %d уверенных слов tesseract, подряд пропало до %d; нет в тексте: %s\n",
			f.SHA256[:12], f.Page, f.Matched, f.Words, f.Run, missing)
	}
}

// DigestRun prints the result of one text-layers run.
func DigestRun(w io.Writer, r *layers.Report) {
	share := 0.0
	if r.CharsBefore > 0 {
		share = 1 - float64(r.CharsAfter)/float64(r.CharsBefore)
	}
	fmt.Fprintf(w, "Документов: %d, готово %d (из них на проверку %d), с ошибкой %d. Из текста убрано %.0f%%. "+
		"Страховка вернула строк: повторов %d, подозрительных %d. Модель стоила $%.3f. Время: %s\n",
		r.Documents, r.Done, r.Flagged, r.Failed, share*100, r.RestoredRepeats, r.RestoredSuspicious,
		r.CostUSD, r.Duration.Round(1e9))
	for _, n := range r.Notes {
		fmt.Fprintf(w, "  %s\n", n)
	}
}

// TextsState prints text-layer totals for the catalog.
func TextsState(w io.Writer, by map[string]int, cost float64, notes []string) {
	if len(by) == 0 {
		return
	}
	fmt.Fprintf(w, "Суть и резюме: готово %d, на проверку %d, с ошибкой %d, не обработано %d; модель стоила $%.3f\n",
		by["done"], by["flagged"], by["error"], by["pending"], cost)
	for _, n := range notes {
		fmt.Fprintf(w, "  %s\n", n)
	}
}

// ShowLayer prints one text layer of a document.
func ShowLayer(w io.Writer, d catalog.Document, t catalog.Texts, layer string) {
	if t.Status == "" {
		fmt.Fprintf(w, "У документа %s ещё нет сути и резюме.\n", d.ID)
		return
	}
	fmt.Fprintf(w, "Документ %s, стр. %d–%d (%s, %s)\n", d.ID, d.FirstPage, d.LastPage, t.Status, t.Model)
	if t.Flags != "" {
		fmt.Fprintf(w, "Проверки: %s\n", t.Flags)
	}
	if layer == "essential" {
		fmt.Fprintf(w, "\n%s\n", t.Essential)
		return
	}
	fmt.Fprintf(w, "\n%s\n", t.Summary)
}

// Persons lists the account's persons.
func Persons(w io.Writer, ps []catalog.Person) {
	if len(ps) == 0 {
		fmt.Fprintln(w, "Людей в аккаунте пока нет: person add --code ... --name ...")
		return
	}
	for _, p := range ps {
		extra := ""
		if p.BirthDate != "" {
			extra = ", род. " + p.BirthDate
		}
		if len(p.Aliases) > 0 {
			extra += ", варианты: " + strings.Join(p.Aliases, "; ")
		}
		fmt.Fprintf(w, "%s  %s — %s%s; документов %d\n", p.Code, p.Label, p.FullName, extra, p.Documents)
	}
}

// DateRun prints the result of one creation-date run.
func DateRun(w io.Writer, r *dating.Report) {
	fmt.Fprintf(w, "Документов: %d, дата %d, неполная дата %d, даты нет %d, не подтвердилось %d, с ошибкой %d. "+
		"Модель стоила $%.4f. Время: %s\n",
		r.Documents, r.Dated, r.Partial, r.None, r.Rejected, r.Failed, r.CostUSD, r.Duration.Round(1e9))
	for _, n := range r.Notes {
		fmt.Fprintf(w, "  %s\n", n)
	}
}

// DocState is what the catalog knows about one document, with provenance.
type DocState struct {
	ID                        string
	Pages                     int
	Date, Partial, DateSource string
	Links                     []catalog.PersonLink
	Attrs                     []catalog.SourcedAttr
	Batches                   []int64
	Rejected                  catalog.Rejected
	Review                    []string
	Changes                   []catalog.DocChangeEntry
	Summary                   string
}

// Doc prints one document's facts and where each came from.
func Doc(w io.Writer, d DocState) {
	fmt.Fprintf(w, "Документ %s, страниц %d\n", d.ID, d.Pages)
	date := d.Date
	if date == "" {
		date = d.Partial
	}
	if date == "" {
		date = "нет"
	}
	if d.DateSource != "" {
		date += " (" + sourceLabel(d.DateSource) + ")"
	}
	fmt.Fprintf(w, "Дата: %s\n", date)
	if len(d.Links) == 0 {
		fmt.Fprintln(w, "Люди: нет")
	}
	for _, l := range d.Links {
		fmt.Fprintf(w, "Человек: %s (%s)\n", l.Code, linkLabel(l))
	}
	for _, a := range d.Attrs {
		fmt.Fprintf(w, "Характеристика: %s (%s)\n", a.Attr, sourceLabel(a.Source))
	}
	var not []string
	for code := range d.Rejected.Persons {
		not = append(not, code)
	}
	for a := range d.Rejected.Attrs {
		if a.Value == "" {
			not = append(not, a.Key+"=*")
		} else {
			not = append(not, a.String())
		}
	}
	if len(not) > 0 {
		sort.Strings(not)
		fmt.Fprintf(w, "Исключено владельцем (пачки и правила это не вернут): %s\n", strings.Join(not, "; "))
	}
	if len(d.Batches) > 0 {
		ids := make([]string, len(d.Batches))
		for i, b := range d.Batches {
			ids[i] = fmt.Sprint(b)
		}
		fmt.Fprintf(w, "Пачки: %s\n", strings.Join(ids, ", "))
	}
	for _, r := range d.Review {
		fmt.Fprintf(w, "На проверку: %s\n", r)
	}
	for _, ch := range d.Changes {
		who := "владелец"
		if ch.Origin == catalog.OriginAssistant {
			who = "ассистент"
			if ch.Actor != "" {
				who += " " + ch.Actor
			}
		}
		line := fmt.Sprintf("Правка %s, %s: %s", ch.At.Local().Format("2006-01-02 15:04"), who, ch.Change)
		if ch.Reason != "" {
			line += " — " + ch.Reason
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "Резюме: %s\n", d.Summary)
}

func linkLabel(l catalog.PersonLink) string {
	switch l.Method {
	case "manual":
		return "вручную"
	case "assistant":
		return "ассистент, не подтверждено: " + l.Reason
	case "batch":
		return l.Reason
	}
	return "по тексту: " + l.Reason
}

func sourceLabel(s string) string {
	switch {
	case s == "manual":
		return "вручную"
	case s == "assistant":
		return "ассистент, не подтверждено"
	case strings.HasPrefix(s, "batch:"):
		return "пачка " + strings.TrimPrefix(s, "batch:")
	case s == "model:stated":
		return "указана в документе"
	case s == "model:event":
		return "дата события"
	case s == "summary":
		return "из резюме"
	}
	return s
}

// BatchCreated prints a new batch.
func BatchCreated(w io.Writer, b catalog.Batch) {
	fmt.Fprintf(w, "Пачка %d: оригиналов %d%s\n", b.ID, b.Originals, batchLabels(b))
}

// Batches prints batches with what they hold and pass on.
func Batches(w io.Writer, bs []catalog.Batch) {
	if len(bs) == 0 {
		fmt.Fprintln(w, "Пачек пока нет: они появляются при ingest.")
		return
	}
	for _, b := range bs {
		name := b.Label
		if name == "" {
			name = b.Root
		}
		fmt.Fprintf(w, "Пачка %d (%s, %s): оригиналов %d, документов %d%s\n",
			b.ID, b.Created.Local().Format("2006-01-02 15:04"), name, b.Originals, b.Documents, batchLabels(b))
	}
}

func batchLabels(b catalog.Batch) string {
	var parts []string
	if len(b.Persons) > 0 {
		parts = append(parts, "человек: "+strings.Join(b.Persons, ", "))
	}
	if len(b.Attrs) > 0 {
		as := make([]string, len(b.Attrs))
		for i, a := range b.Attrs {
			as[i] = a.String()
		}
		parts = append(parts, "характеристики: "+strings.Join(as, "; "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; " + strings.Join(parts, "; ")
}

// IndexRun prints the result of reindexing.
func IndexRun(w io.Writer, r *index.Report, unlinked int) {
	fmt.Fprintf(w, "Документов: %d, с датой %d, с неполной датой %d, привязано к людям %d, без человека %d\n",
		r.Documents, r.Dated, r.Partial, r.Linked, unlinked)
	for _, k := range sortedKeys(r.Links) {
		fmt.Fprintf(w, "  %s: %d (по пачке %d)\n", k, r.Links[k], r.ByBatch[k])
	}
}

// SplitRun prints the result of one split run.
func SplitRun(w io.Writer, r *split.Report) {
	fmt.Fprintf(w, "Оригиналов: %d, документов в них: %d (одностраничных оригиналов %d). На проверку: %d, с ошибкой: %d. Модель стоила $%.3f. Время: %s\n",
		r.Originals, r.Documents, r.Single, r.Review, r.Failed, r.CostUSD, r.Duration.Round(1e9))
	for _, n := range r.Notes {
		fmt.Fprintf(w, "  %s\n", n)
	}
}

// SplitState prints split totals for the catalog.
func SplitState(w io.Writer, byStatus map[string]int, docs int, notes []string) {
	if len(byStatus) == 0 {
		return
	}
	fmt.Fprintf(w, "Разбивка на документы: документов %d; оригиналов готово %d, на проверку %d, с ошибкой %d\n",
		docs, byStatus["done"], byStatus["review"], byStatus["error"])
	for _, n := range notes {
		fmt.Fprintf(w, "  %s\n", n)
	}
}

// Documents lists documents with their pages and first line.
func Documents(w io.Writer, docs []catalog.DocumentRow) {
	if len(docs) == 0 {
		fmt.Fprintln(w, "Документов пока нет.")
		return
	}
	last := ""
	for _, d := range docs {
		if d.Source != last {
			fmt.Fprintf(w, "%s\n", Path(d.Source))
			last = d.Source
		}
		pages := fmt.Sprintf("стр. %d", d.FirstPage)
		if d.LastPage > d.FirstPage {
			pages = fmt.Sprintf("стр. %d–%d", d.FirstPage, d.LastPage)
		}
		mark := ""
		if d.Status == "review" {
			mark = " [на проверку]"
		}
		fmt.Fprintf(w, "  %s  %-11s %s%s\n", d.ID, pages, d.FirstLine, mark)
	}
}

var statusLabels = map[string]string{
	"done": "готово", "error": "с ошибкой", "unsupported": "тип пока не поддерживается", "pending": "ещё не обработано",
}

// Extraction prints the state of page text across the catalog.
func Extraction(w io.Writer, x catalog.ExtractionStats) {
	parts := []string{}
	for _, k := range sortedKeys(x.ByStatus) {
		l := statusLabels[k]
		if l == "" {
			l = k
		}
		parts = append(parts, fmt.Sprintf("%s %d", l, x.ByStatus[k]))
	}
	fmt.Fprintf(w, "Текст страниц: %s\n", strings.Join(parts, ", "))
	if len(x.ByMethod) > 0 {
		fmt.Fprintf(w, "  страниц из текстового слоя %d, из OCR %d, OCR стоил $%.3f\n",
			x.ByMethod["text-layer"], x.ByMethod["ocr"], x.CostUSD)
	}
	for _, e := range x.Errors {
		fmt.Fprintf(w, "  ошибка %s\n", e)
	}
}

// PageText prints the verbatim text of an original, page by page.
func PageText(w io.Writer, sha string, ps []catalog.Page) {
	if len(ps) == 0 {
		fmt.Fprintf(w, "У оригинала %s ещё нет текста.\n", sha[:12])
		return
	}
	for _, p := range ps {
		fmt.Fprintf(w, "===== %s, страница %d (%s, %s) =====\n%s\n", sha[:12], p.Page, p.Method, p.Engine, strings.TrimRight(p.Text, "\n"))
	}
}

// Intake prints the result of one intake run.
func Intake(w io.Writer, r *intake.Report) {
	fmt.Fprintf(w, "Папка: %s\n", r.Root)
	fmt.Fprintf(w, "Файлов: %d, из них без изменений с прошлого раза: %d, прочитано: %d, новых оригиналов: %d\n",
		r.Files, r.Unchanged, r.Hashed, r.NewFiles)
	if len(r.Skipped) > 0 {
		keys := sortedKeys(r.Skipped)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s — %d", skipLabel(k), r.Skipped[k]))
		}
		fmt.Fprintf(w, "Пропущено: %s\n", strings.Join(parts, "; "))
	}
	if r.Missing > 0 {
		fmt.Fprintf(w, "Пропали с прошлого раза: %d (в каталоге помечены, не удалены)\n", r.Missing)
	}
	if len(r.Errors) > 0 {
		fmt.Fprintf(w, "Ошибки: %d\n", len(r.Errors))
		for _, e := range r.Errors {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	fmt.Fprintf(w, "Время: %s\n", r.Duration.Round(1e7))
}

var skipLabels = map[string]string{
	"os metadata":          "служебные файлы ОС",
	"temporary file":       "временные файлы",
	"sync client metadata": "служебные файлы синхронизации",
	"not a regular file":   "не обычные файлы (ссылки, устройства)",
}

func skipLabel(k string) string {
	if l, ok := skipLabels[k]; ok {
		return l
	}
	return k
}

// Duplicates prints originals seen in more than one place.
func Duplicates(w io.Writer, dups []catalog.Duplicate) {
	if len(dups) == 0 {
		fmt.Fprintln(w, "Дубликатов нет.")
		return
	}
	var wasted int64
	for _, d := range dups {
		wasted += d.Size * int64(len(d.URIs)-1)
	}
	fmt.Fprintf(w, "Дубликатов: %d групп, лишних копий занимают %s\n\n", len(dups), Bytes(wasted))
	for i, d := range dups {
		fmt.Fprintf(w, "%d. %s, %s, sha256 %s…\n", i+1, Bytes(d.Size), d.MIME, d.SHA256[:12])
		for _, u := range d.URIs {
			fmt.Fprintf(w, "   %s\n", Path(u))
		}
	}
}

// Stats prints catalog totals.
func Stats(w io.Writer, s catalog.Stats, mismatches [][3]string) {
	fmt.Fprintf(w, "Оригиналов: %d (%s), мест, где они встречались: %d, из них пропавших: %d\n",
		s.Originals, Bytes(s.Bytes), s.Sources, s.Missing)
	for _, k := range sortedKeys(s.ByMIME) {
		fmt.Fprintf(w, "  %-40s %d\n", k, s.ByMIME[k])
	}
	if len(mismatches) > 0 {
		fmt.Fprintf(w, "Расширение не совпадает с содержимым: %d\n", len(mismatches))
		for _, m := range mismatches {
			fmt.Fprintf(w, "  %s: по расширению %s, по содержимому %s\n", Path(m[0]), m[1], m[2])
		}
	}
}

// Path turns a file:// URI back into a readable path.
func Path(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if len(p) > 2 && p[0] == '/' && p[2] == ':' { // /C:/... on Windows
		p = p[1:]
	}
	if u, err := url.PathUnescape(p); err == nil {
		return u
	}
	return p
}

// Bytes formats a size for humans.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), []string{"КБ", "МБ", "ГБ", "ТБ"}[exp])
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
