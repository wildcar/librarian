// Package split cuts an original's pages into documents (ADR-0003): one model
// call over the whole file, with text rules as an independent check.
package split

import (
	"regexp"
	"strconv"
	"strings"
)

// The rules were measured in tools/ocrbench/split.py (precision 93%, recall 75%
// without handwritten dates). They never rely on marks an owner made on paper.
var (
	// A line that is only a date, as a stamp or a note puts at the top of a
	// page; it is skipped when letterheads are compared, never used as a signal.
	dateOnly = regexp.MustCompile(`^\s*\d{1,2}\s*[.,/ ]\s*\d{1,2}\s*[.,/ ]?\s*(\d{2}|\d{4})\s*$|^\s*\d{5,6}\s*$`)
	title    = regexp.MustCompile(`(?i)протокол обследования|рекомендации по лечению|детализация по документу|` +
		`дополнительное соглашение|консультативное заключение|выписной эпикриз|` +
		`результаты холтеровского|мониторирование|направление|справка|` +
		`областная клиническая больница|онкологический диспансер|лабораторно-диагностический`)
	ending = regexp.MustCompile(`(?im)подпись врача|^дата\s*:|\[штамп\]|проконсультироваться со специалистом|` +
		`от имени пациента|рекомендации врачей|следующее посещение|консультант|врач\s*:|` +
		`бланк разработан на основании|подпись\s*:\s*дата`)
	device    = regexp.MustCompile(`(?i)SIEMENS|SM-CLIN|SonoScape|Unknown|GEMS|Kenz|MI \d|TIS \d|\d+cm\b|\bD[=:]`)
	brand     = regexp.MustCompile(`(?i)SIEMENS|SonoScape|GEMS|Kenz|SM-CLIN`)
	cyrWord   = regexp.MustCompile(`(?i)[а-яё]{4,}`)
	codeToken = regexp.MustCompile(`\b[A-Z0-9]{6,}\b`)
	// A page that starts mid-document: a table row, a lowercase letter (case
	// matters here), or a known continuation block of a clinic's forms.
	continues = regexp.MustCompile(`^(\||[а-яё]|(?i:назначения, запись|подпись врача|данные рентгена|рт\.ст))`)
	running   = regexp.MustCompile(`(?i)(?:возраст\s*:?\s*\d+.{0,60}?|стр\.?\s*)(\d{1,3})(?:\D|$)`)
	caseNo    = regexp.MustCompile(`(?i)(случай\s*№\s*\d+|договор на оказание услуг\s*№\s*[\d ]+|номер обследования:\s*\S+)`)
)

// Lines returns the meaningful lines of a page text.
func Lines(t string) []string {
	var out []string
	for _, l := range strings.Split(t, "\n") {
		l = strings.TrimSpace(l)
		if strings.Trim(l, "|-_ ") != "" {
			out = append(out, l)
		}
	}
	return out
}

func isAttachment(t string) bool {
	cyr := len(cyrWord.FindAllString(t, -1))
	if len(device.FindAllString(t, -1)) >= 2 && cyr < 15 {
		return true // an ultrasound frame or an ECG tape
	}
	return cyr < 4 && len(codeToken.FindAllString(t, -1)) >= 5 // a sheet of specimen codes
}

func brandOf(t string) string {
	return strings.ToUpper(brand.FindString(t))
}

func pageNo(lines []string) (int, bool) {
	for i, l := range lines {
		if i == 3 {
			break
		}
		if m := running.FindStringSubmatch(l); m != nil {
			n, err := strconv.Atoi(m[1])
			return n, err == nil
		}
	}
	return 0, false
}

func sameHeader(a, b []string) bool {
	head := func(ls []string) []string {
		var h []string
		for i, l := range ls {
			if i == 4 || len(h) == 3 {
				break
			}
			if !dateOnly.MatchString(l) {
				h = append(h, l)
			}
		}
		return h
	}
	ha, hb := head(a), head(b)
	return len(ha) == 3 && len(hb) == 3 && ratio(strings.Join(ha, " "), strings.Join(hb, " ")) >= 0.90
}

// ratio is the normalised indel similarity 2*LCS/(len(a)+len(b)) over runes,
// the same measure as rapidfuzz.ratio used in the bake-off. OCR reads the same
// logo differently from page to page, so headers are compared fuzzily.
func ratio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra)+len(rb) == 0 {
		return 1
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			switch {
			case ra[i-1] == rb[j-1]:
				cur[j] = prev[j-1] + 1
			case prev[j] >= cur[j-1]:
				cur[j] = prev[j]
			default:
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	return 2 * float64(prev[len(rb)]) / float64(len(ra)+len(rb))
}

// Verdict is the rules' answer for the boundary between two pages.
type Verdict struct {
	New    bool   // page B starts a new document
	Reason string // which rule decided
	Strong bool   // strong verdicts are compared with the model; weak ones are not
}

// Boundary judges whether page b starts a new document after page a.
func Boundary(a, b string) Verdict {
	la, lb := Lines(a), Lines(b)
	if len(lb) == 0 {
		return Verdict{false, "empty page B", false}
	}
	headB := strings.Join(lb[:min(3, len(lb))], "\n")
	ended := ending.MatchString(strings.Join(la[max(0, len(la)-4):], "\n"))
	if na, ok := pageNo(la); ok {
		if nb, ok := pageNo(lb); ok && nb == na+1 {
			return Verdict{false, "running page number continues", true}
		}
	}
	// Printouts come before the protocol they belong to.
	if isAttachment(a) {
		if isAttachment(b) {
			ba, bb := brandOf(a), brandOf(b)
			if ba != "" && bb != "" && ba != bb {
				return Verdict{true, "printouts from another device", false}
			}
			return Verdict{false, "printout after printout", true}
		}
		return Verdict{false, "protocol after its printouts", true}
	}
	if isAttachment(b) {
		if ended {
			return Verdict{true, "printout after a finished document", false}
		}
		return Verdict{false, "printout attached", false}
	}
	// Weak on purpose (calibrated 2026-10-20): several payment details under one
	// contract, or several recommendations on one clinic's form, share both
	// the number and the letterhead and are still separate documents.
	ca, cb := caseNo.FindAllString(a, -1), caseNo.FindAllString(b, -1)
	for _, x := range ca {
		for _, y := range cb {
			if strings.EqualFold(x, y) {
				return Verdict{false, "same case or contract number", false}
			}
		}
	}
	if sameHeader(la, lb) {
		return Verdict{false, "same letterhead repeated", false}
	}
	first := lb[0]
	if dateOnly.MatchString(first) && len(lb) > 1 {
		first = lb[1] // a date line on top says nothing about continuation
	}
	if continues.MatchString(first) {
		return Verdict{false, "page B starts mid-document", true}
	}
	if title.MatchString(headB) {
		if ended {
			return Verdict{true, "title on page B after an ending", true}
		}
		return Verdict{true, "title on page B", false}
	}
	if ended {
		return Verdict{true, "page A ends a document", false}
	}
	return Verdict{false, "no signal", false}
}
