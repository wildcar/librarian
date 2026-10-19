package extract

import (
	"strings"
	"testing"

	"github.com/wildcar/librarian/internal/pages"
)

func reading(conf float64, text string) pages.Reading {
	var r pages.Reading
	for _, w := range strings.Fields(text) {
		r.Words = append(r.Words, pages.Word{Text: w, Conf: conf})
	}
	return r
}

const printed = `Протокол обследования Пациент Врач Организация Дата выполнения Видеоэзофагогастродуоденоскопия
Пищевод вход свободный проходимость полная просвет изменен перистальтика активная слизистая
серо-розовая переходной зоны отечная умеренно гиперемирована рыхлая утолщена продольными эрозиями
фибрином заключение эрозивный рефлюкс-эзофагит грыжа пищеводного отверстия диафрагмы`

func TestCompareAgreeingPrintedPage(t *testing.T) {
	k := Compare(reading(95, printed), strings.ToUpper(printed)) // case and layout do not matter
	if k.Words < CheckMinWords || k.Matched != k.Words || k.Flagged {
		t.Errorf("agreeing page: %+v", k)
	}
}

func TestCompareFlagsDroppedText(t *testing.T) {
	lines := strings.Split(printed, "\n")
	primary := lines[0] + "\n" + lines[3] // the model dropped two lines
	k := Compare(reading(95, printed), primary)
	if !k.Flagged || k.Share() >= CheckMinShare || len(k.Missing) == 0 {
		t.Errorf("dropped lines not flagged: share %.2f %+v", k.Share(), k)
	}
}

func TestCompareFlagsOneDroppedLine(t *testing.T) {
	// A long page of unique lines, so one line is a small share of its words.
	letter := func(n int) string { // 0 -> "а", 12 -> "бв": unique letter-only tags
		s := ""
		for {
			s = string([]rune("абвгдежзик")[n%10]) + s
			if n /= 10; n == 0 {
				return s
			}
		}
	}
	var lines []string
	for i := range 40 {
		var ws []string
		for j := range 5 {
			ws = append(ws, "показатель"+letter(i*5+j)+"ъ")
		}
		lines = append(lines, strings.Join(ws, " "))
	}
	long := strings.Join(lines, "\n")
	primary := strings.Join(append(lines[:5:5], lines[6:]...), "\n") // one line gone
	k := Compare(reading(95, long), primary)
	if k.Share() < CheckMinShare {
		t.Fatalf("test setup: share %.2f should stay above the threshold", k.Share())
	}
	if !k.Flagged || k.MaxRun < CheckMaxRun {
		t.Errorf("one dropped line not flagged: run %d %+v", k.MaxRun, k)
	}
	// Scattered single misreads do not form a run.
	words := strings.Fields(printed)
	for i := 0; i < len(words); i += 6 {
		words[i] = "Ошибочнослово"
	}
	k = Compare(reading(95, strings.Join(words, " ")), printed)
	if k.MaxRun >= CheckMaxRun {
		t.Errorf("scattered misreads made a run of %d", k.MaxRun)
	}
}

func TestCompareIgnoresUnsureWords(t *testing.T) {
	// Handwriting: tesseract produces words, but is not sure of them.
	k := Compare(reading(40, printed), "Совсем другой текст")
	if k.Words != 0 || k.Flagged {
		t.Errorf("unsure words judged: %+v", k)
	}
	// Few confident words: not enough to call the page printed.
	k = Compare(reading(95, "Протокол обследования пациента"), "иное")
	if k.Flagged {
		t.Errorf("short page flagged: %+v", k)
	}
}

func TestNormalizeLookalikes(t *testing.T) {
	// Latin "o", "e", "p", "c" in an OCR'd Cyrillic word still match.
	if normalize("Пpoтoкoл") != normalize("Протокол") || normalize("ёлка, 2026!") != "елка2026" {
		t.Error("normalize does not fold look-alikes or punctuation")
	}
}
