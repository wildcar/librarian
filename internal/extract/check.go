package extract

import (
	"strings"
	"unicode"

	"github.com/wildcar/librarian/internal/pages"
)

// The second reading (ADR-0002): tesseract is weak on handwriting but rarely
// wrong about printed words it is sure of. A printed page whose confident
// tesseract words are largely absent from the Argon text means Argon dropped
// or rewrote something, and the page is flagged for the owner.
const (
	CheckEngine = "tesseract"
	// A tesseract word counts when it is this sure and this long.
	ConfidentConf    = 80
	ConfidentLetters = 4
	// A page is "printed" when it has at least this many confident words;
	// handwriting pages have few and are not judged.
	CheckMinWords = 20
	// Printed pages with a smaller share of confident words found in the
	// Argon text are flagged: a large omission. Calibrated on the sample
	// (ADR-0002): the lowest real share there was 0.92.
	CheckMinShare = 0.90
	// So is a run of this many consecutive confident words missing from the
	// Argon text: a dropped line. Tesseract's own errors are scattered single
	// words, so they rarely form a run.
	CheckMaxRun = 3
)

// Check is the outcome of comparing the two readings of one page.
type Check struct {
	Words   int      // confident tesseract words
	Matched int      // of them, found in the Argon text
	Missing []string // confident words not found, in page order
	MaxRun  int      // longest run of consecutive confident words not found
	Flagged bool
}

// Share is Matched/Words, or 1 when there is nothing to compare.
func (c Check) Share() float64 {
	if c.Words == 0 {
		return 1
	}
	return float64(c.Matched) / float64(c.Words)
}

// Compare checks a tesseract reading against the primary (Argon) text.
func Compare(t pages.Reading, primary string) Check {
	hay := normalize(primary)
	var c Check
	run := 0
	for _, w := range t.Words {
		n := normalize(w.Text)
		if w.Conf < ConfidentConf || letters(n) < ConfidentLetters {
			continue
		}
		c.Words++
		if strings.Contains(hay, n) {
			c.Matched++
			run = 0
		} else {
			c.Missing = append(c.Missing, w.Text)
			run++
			c.MaxRun = max(c.MaxRun, run)
		}
	}
	c.Flagged = c.Words >= CheckMinWords && (c.Share() < CheckMinShare || c.MaxRun >= CheckMaxRun)
	return c
}

// lookalike maps Latin letters that OCR confuses with Cyrillic ones.
var lookalike = strings.NewReplacer(
	"a", "а", "b", "в", "c", "с", "e", "е", "h", "н", "k", "к", "m", "м",
	"o", "о", "p", "р", "t", "т", "x", "х", "y", "у", "ё", "е",
)

// normalize keeps letters and digits only, lowercase, with look-alikes folded,
// so "Forest III", "Forest Ш" and a split line compare by their letters.
func normalize(s string) string {
	s = lookalike.Replace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func letters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}
