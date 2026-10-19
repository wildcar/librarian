// Package layers builds the essential text and the de-identified summary of
// every document (ADR-0005), with the safeguards and checks measured in
// tools/ocrbench/layers.py.
package layers

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// word wraps a pattern in letter boundaries. RE2's \b is ASCII-only, so it
// never fires next to Cyrillic letters.
func word(p string) string { return `(?:^|[^\p{L}])(?:` + p + `)(?:[^\p{L}]|$)` }

// boilerplate is text no reader needs: form notes, licences, ads, contacts.
// Lines matching it are removed by rules before the model sees the text.
var boilerplate = regexp.MustCompile(`(?i)бланк разработан|приказом мз|лицензи|огрн|инн\s*/?\s*кпп|скидк|онлайн.?запис|` +
	`подробности по телефону|имеются противопоказания|проконсультироваться со специалистом|` +
	`www\.|https?://|e-?mail|telegram|почта\s*:|звонок\s*:|штрихкод|` +
	`система менеджмента качества|круглосуточн\S* запис|многопрофильный медицинский холдинг|` +
	`тел\.?\s*/\s*факс|@\w|для справок|документ подписан электронной подписью|сертификат\s*:`)

// Boilerplate reports whether a line is known boilerplate.
func Boilerplate(line string) bool { return boilerplate.MatchString(line) }

// RulesClean drops known boilerplate lines.
func RulesClean(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !Boilerplate(l) {
			out = append(out, l)
		}
	}
	return out
}

var (
	medical = regexp.MustCompile(`(?i)диагноз|заключени|рекоменд|назнач|принимать|результат|анализ|норм|стеноз|кровото|` +
		`экг|узи|холестерин|гемоглобин|давлени|таб\.|капс|терапи|операци|осмотр|жалоб|` +
		word(`мг|мл|ммоль|мкмоль|мм|см|приём|прием`) + `|уд/мин`)
	numDate = regexp.MustCompile(`(\d{1,2})\s*[./ ]\s*(\d{1,2})\s*[./]\s*(\d{4}|\d{2})(?:\D|$)`)
	digit   = regexp.MustCompile(`\d`)
	// contactOrAd: a removed line with digits that is plainly a contact or an
	// advert is not a lost fact.
	contactOrAd = regexp.MustCompile(`(?i)(?:\+7|8)[\s(-]*\d{3,4}|тел\.|телефон|факс|скидк|акци|запис|адрес|ул\.|` +
		`д\.\s*\d|стр\.\s*\d|бульвар|проспект|режим работы|часы работы|ежедневно|почт|@|www|\.ru`)
)

func shape(l string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(l) {
		if unicode.IsDigit(r) || unicode.IsSpace(r) {
			if !space {
				b.WriteRune(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	return strings.TrimSpace(b.String())
}

// Essential applies the model's removals with both safeguards and returns the
// kept lines, plus counts of lines brought back.
//
//   - Repeated lines: when every copy of a repeated line was dropped, the first
//     comes back — a running header may be the only place a name is.
//   - Suspicious removals: a dropped line with a medical word, a date or a long
//     run of digits, that is not a contact or an advert and has no copy left,
//     comes back.
func Essential(lines []string, drop map[int]string) (kept []string, restoredRepeats, restoredSuspicious int) {
	drop = cloneDrop(drop)
	groups := map[string][]int{}
	for i, l := range lines {
		groups[shape(l)] = append(groups[shape(l)], i+1)
	}
	for _, idx := range groups {
		if len(idx) < 2 || Boilerplate(lines[idx[0]-1]) {
			continue
		}
		all := true
		for _, n := range idx {
			if _, ok := drop[n]; !ok {
				all = false
				break
			}
		}
		if all {
			delete(drop, idx[0])
			restoredRepeats++
		}
	}
	keptShapes := map[string]bool{}
	keptDates := map[ymd]bool{}
	for i, l := range lines {
		if _, ok := drop[i+1]; !ok {
			keptShapes[shape(l)] = true
			for d := range dates(l) {
				keptDates[d] = true
			}
		}
	}
	order := make([]int, 0, len(drop))
	for n := range drop {
		order = append(order, n)
	}
	sort.Ints(order) // deterministic: the first line carrying a date comes back
	for _, n := range order {
		l := lines[n-1]
		if contactOrAd.MatchString(l) {
			continue
		}
		restore := false
		for d := range dates(l) {
			if !keptDates[d] { // a date the essential text would no longer have
				restore = true
			}
		}
		if !keptShapes[shape(l)] && (medical.MatchString(l) || (digit.MatchString(l) && len([]rune(l)) >= 30)) {
			restore = true
		}
		if restore {
			delete(drop, n)
			restoredSuspicious++
			keptShapes[shape(l)] = true
			for d := range dates(l) {
				keptDates[d] = true
			}
		}
	}
	for i, l := range lines {
		if _, ok := drop[i+1]; !ok {
			kept = append(kept, l)
		}
	}
	return kept, restoredRepeats, restoredSuspicious
}

func cloneDrop(m map[int]string) map[int]string {
	out := make(map[int]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// --- leaks -------------------------------------------------------------------

var (
	fio      = regexp.MustCompile(`([А-ЯЁ][а-яё]+(?:-[А-ЯЁ][а-яё]+)?)\s+([А-ЯЁ][а-яё]+)\s+([А-ЯЁ][а-яё]+(?:вич|вна|ична|чна|ич))(?:[^\p{L}]|$)`)
	initials = regexp.MustCompile(`([А-ЯЁ][а-яё]{3,})\s+[А-ЯЁ]\.\s?[А-ЯЁ]\.|[А-ЯЁ]\.\s?[А-ЯЁ]\.\s?([А-ЯЁ][а-яё]{3,})`)
	org      = regexp.MustCompile(`(?:ООО|ГБУЗ|ГБУ(?:\s+РО)?|АО|ЗАО|ФГБУ)\s*[«"„]+\s*([^»"“]{3,80})[»"“]`)
	brandRe  = regexp.MustCompile(`[А-ЯЁA-Z]{2,}-[А-ЯЁA-Z][а-яёa-z]+`)
	patterns = map[string]*regexp.Regexp{
		"телефон":       regexp.MustCompile(`(?:\+7|8)[\s(-]*\d{3,4}[\s)-]*\d{1,3}[\s-]*\d{2}[\s-]*\d{2}`),
		"почта":         regexp.MustCompile(`[\w.-]+@[\w-]+\.\w+`),
		"номер":         regexp.MustCompile(`[А-ЯA-Z]{1,3}\d{6,}|\d{2}[МM]/\d{3,4}|№\s*\d[\d\s-]{3,}`),
		"адрес":         regexp.MustCompile(`ул\.?\s+[А-ЯЁ][а-яё]+|улица\s+[А-ЯЁ]`),
		"дата рождения": regexp.MustCompile(`(?i)\d{1,2}[./]\d{1,2}[./]\d{2,4}\s*г\.?\s*р\.?`),
	}
)

func stem(w string) string {
	r := []rune(strings.ToLower(w))
	if len(r) > 6 {
		return string(r[:len(r)-2])
	}
	return string(r)
}

// acronym of an organisation name: "Рязанский областной онкологический
// диспансер" -> "роод". Short names give nothing.
func acronym(name string) string {
	var b strings.Builder
	n := 0
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) }) {
		r := []rune(w)
		if len(r) < 3 {
			continue
		}
		b.WriteRune(unicode.ToLower(r[0]))
		n++
	}
	if n < 3 {
		return ""
	}
	return b.String()
}

// Forbidden collects strings that must not appear in a summary: names found in
// the document, organisations and their acronyms, and extra strings (the
// account's persons, when a registry exists).
func Forbidden(source string, extra ...string) []string {
	set := map[string]bool{}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if len([]rune(s)) >= 4 && !common[s] {
			set[s] = true
		}
	}
	for _, m := range fio.FindAllStringSubmatch(source, -1) {
		add(stem(m[1]))
		add(m[2] + " " + m[3])
	}
	for _, m := range initials.FindAllStringSubmatch(source, -1) {
		add(stem(m[1] + m[2]))
	}
	for _, m := range org.FindAllStringSubmatch(source, -1) {
		add(m[1])
		if a := acronym(m[1]); a != "" {
			set[a] = true // acronyms are short; checked as whole words below
		}
	}
	for _, m := range brandRe.FindAllString(source, -1) {
		add(m)
	}
	for _, e := range extra {
		add(e)
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

var common = map[string]bool{"прием": true, "приема": true, "протокол": true, "клиника": true, "врача": true, "пациент": true}

// Leaks lists forbidden strings and identifying patterns found in a summary.
func Leaks(summary string, forbidden []string) []string {
	low := strings.ToLower(summary)
	var out []string
	for _, f := range forbidden {
		if len([]rune(f)) <= 5 { // acronyms and short stems: whole words only
			if regexp.MustCompile(word(regexp.QuoteMeta(f))).MatchString(low) {
				out = append(out, f)
			}
			continue
		}
		if strings.Contains(low, f) {
			out = append(out, f)
		}
	}
	kinds := make([]string, 0, len(patterns))
	for k := range patterns {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		for _, m := range patterns[k].FindAllString(summary, -1) {
			out = append(out, k+": "+m)
		}
	}
	return out
}

// --- numbers -----------------------------------------------------------------

var (
	superscript = strings.NewReplacer("⁰", "0", "¹", "1", "²", "2", "³", "3", "⁴", "4", "⁵", "5", "⁶", "6", "⁷", "7", "⁸", "8", "⁹", "9")
	number      = regexp.MustCompile(`\d+(?:[.,:]\d+)*`)
	// A date in any of the shapes documents use: 2.04.22, 2 апреля 2022,
	// «29» X 2019г., 21.Янв.1970 (day, month, year), and ДЕК 4, 2019 (month,
	// day, year). The month is a number, a word or its abbreviation, or a
	// Roman numeral, Cyrillic Х included.
	month = `\d{1,2}|[A-Za-zА-Яа-яЁё]{1,9}`
	// Separators inside a date: spaces, dots, slashes, quotes, dashes. Not
	// "×" or commas, so a size like "5 × 9,12" is not read as a date.
	sep     = `[\s./«»"'\-]{1,4}`
	dmyDate = regexp.MustCompile(`(\d{1,2})` + sep + `(` + month + `)` + sep + `(\d{4}|\d{2})(?:\D|$)`)
	// "ДЕК 4, 2019": the month first, a comma allowed before the year.
	mdyDate  = regexp.MustCompile(`([A-Za-zА-Яа-яЁё]{3,9})` + sep + `(\d{1,2}),?` + sep + `(\d{4})(?:\D|$)`)
	monthPfx = []string{"янв", "фев", "мар", "апр", "мая", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}
	monthNum = []int{1, 2, 3, 4, 5, 5, 6, 7, 8, 9, 10, 11, 12}
	roman    = map[string]int{"i": 1, "ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7, "viii": 8, "ix": 9, "x": 10, "xi": 11, "xii": 12}
)

type ymd [3]int

// monthOf reads a month written as a number, a word or a Roman numeral.
func monthOf(s string) int {
	if s[0] >= '0' && s[0] <= '9' {
		if n := atoi(s); n >= 1 && n <= 12 {
			return n
		}
		return 0
	}
	low := strings.ToLower(strings.NewReplacer("Х", "x", "х", "x", "І", "i").Replace(s))
	if n, ok := roman[low]; ok {
		return n
	}
	for i, p := range monthPfx {
		if strings.HasPrefix(low, p) {
			return monthNum[i]
		}
	}
	return 0
}

// datesIn finds dates and where they are, so they can be cut out of the text.
// A rejected candidate ("0% Скидка 09" is no date) must not swallow the real
// date that starts inside it, so the scan restarts one byte after the start
// of every rejected match.
func datesIn(text string) (map[ymd]bool, [][]int) {
	out := map[ymd]bool{}
	var spans [][]int
	scan := func(re *regexp.Regexp, dayFirst bool) {
		for pos := 0; pos < len(text); {
			m := re.FindStringSubmatchIndex(text[pos:])
			if m == nil {
				return
			}
			for i := range m {
				m[i] += pos
			}
			var d, mo int
			if dayFirst {
				d, mo = atoi(text[m[2]:m[3]]), monthOf(text[m[4]:m[5]])
			} else {
				mo, d = monthOf(text[m[2]:m[3]]), atoi(text[m[4]:m[5]])
			}
			y := atoi(text[m[6]:m[7]])
			// A date does not start in the middle of a number ("1,5.9.12") or a
			// time ("12:50:13 05.12.2019" holds no date 13.05.12), and does not
			// stop in the middle of one ("13 05.12" followed by ".2019").
			isDigit := func(i int) bool { return i >= 0 && i < len(text) && text[i] >= '0' && text[i] <= '9' }
			prev := byte(' ')
			if m[2] > 0 {
				prev = text[m[2]-1]
			}
			glued := isDigit(m[2]-1) || prev == ':' ||
				((prev == ',' || prev == '.') && isDigit(m[2]-2)) ||
				((m[7] < len(text) && (text[m[7]] == '.' || text[m[7]] == ',')) && isDigit(m[7]+1))
			if d >= 1 && d <= 31 && mo > 0 && !glued {
				out[ymd{d, mo, y % 100}] = true
				spans = append(spans, []int{m[0], m[7]})
				pos = m[7]
			} else {
				pos = m[2] + 1
			}
		}
	}
	scan(dmyDate, true)
	scan(mdyDate, false)
	return out, spans
}

func dates(text string) map[ymd]bool {
	d, _ := datesIn(text)
	return d
}

// Dates returns every date in text as day, month and two-digit year, in any
// of the shapes datesIn understands, ordered by position.
func Dates(text string) [][3]int {
	_, spans := datesIn(text)
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	var out [][3]int
	for _, sp := range spans {
		for k := range dates(text[sp[0]:sp[1]]) {
			out = append(out, [3]int(k))
		}
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// InventedNumbers lists dates and numbers of two or more digits in a summary
// that do not occur in the source. Dates compare by meaning.
func InventedNumbers(summary, source string) []string {
	source, summary = superscript.Replace(source), superscript.Replace(summary)
	srcDates := dates(source)
	srcDigits := digitsOnly(source)
	var out []string
	_, spans := datesIn(summary)
	rest := []byte(summary)
	for _, sp := range spans {
		frag := summary[sp[0]:sp[1]]
		for k := range dates(frag) {
			if !srcDates[k] {
				out = append(out, strings.TrimSpace(strings.TrimRight(frag, ".,;: )")))
			}
		}
		for i := sp[0]; i < sp[1]; i++ {
			rest[i] = ' ' // dates are judged as dates, not as loose numbers
		}
	}
	for _, n := range number.FindAllString(string(rest), -1) {
		d := digitsOnly(n)
		if len(d) < 2 || strings.Contains(srcDigits, d) {
			continue
		}
		out = append(out, n)
	}
	return out
}
