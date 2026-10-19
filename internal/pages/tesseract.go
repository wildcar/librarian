package pages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Tesseract runs the tesseract command line: the local, independent second
// reading of a page (ADR-0002).
type Tesseract struct {
	Exe      string // path to the binary; empty means "tesseract" on PATH
	Tessdata string // --tessdata-dir; empty means tesseract's default
	Langs    string // default "rus+eng"
}

// ErrNoTesseract means the binary or its language data is missing.
var ErrNoTesseract = errors.New("tesseract not found: install tesseract-ocr with the rus language, or set LIBRARIAN_TESSERACT / LIBRARIAN_TESSDATA")

func (t Tesseract) exe() (string, error) {
	name := t.Exe
	if name == "" {
		name = "tesseract"
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", ErrNoTesseract
	}
	return p, nil
}

func (t Tesseract) langs() string {
	if t.Langs == "" {
		return "rus+eng"
	}
	return t.Langs
}

// Check verifies the binary runs and has every language it needs.
func (t Tesseract) Check(ctx context.Context) error {
	exe, err := t.exe()
	if err != nil {
		return err
	}
	args := []string{"--list-langs"}
	if t.Tessdata != "" {
		args = append([]string{"--tessdata-dir", t.Tessdata}, args...)
	}
	out, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoTesseract, err)
	}
	have := map[string]bool{}
	for _, l := range strings.Fields(string(out)) {
		have[l] = true
	}
	for _, l := range strings.Split(t.langs(), "+") {
		if !have[l] {
			return fmt.Errorf("%w: language %q missing", ErrNoTesseract, l)
		}
	}
	return nil
}

// Word is one recognised word and tesseract's confidence in it (0-100).
type Word struct {
	Text string
	Conf float64
}

// Reading is tesseract's result for one page image.
type Reading struct {
	Text  string // words joined line by line
	Words []Word
}

// Read recognises one page image. Page segmentation mode 1 lets tesseract
// detect orientation, as in the bake-off.
func (t Tesseract) Read(ctx context.Context, image string) (Reading, error) {
	exe, err := t.exe()
	if err != nil {
		return Reading{}, err
	}
	// Image and output first, options after: the order tesseract 5 accepts on
	// every platform. The "tsv" config file name is not portable, the -c is.
	args := []string{image, "stdout"}
	if t.Tessdata != "" {
		args = append(args, "--tessdata-dir", t.Tessdata)
	}
	args = append(args, "-l", t.langs(), "--psm", "1", "-c", "tessedit_create_tsv=1")
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return Reading{}, fmt.Errorf("tesseract %s: %w: %s", image, err, strings.TrimSpace(stderr.String()))
	}
	return parseTSV(out.String()), nil
}

// parseTSV reads tesseract's TSV: level page block par line word left top
// width height conf text. Word rows have level 5.
func parseTSV(tsv string) Reading {
	var r Reading
	var lines []string
	var cur []string
	lastLine := ""
	for i, row := range strings.Split(tsv, "\n") {
		if i == 0 || row == "" {
			continue
		}
		f := strings.SplitN(strings.TrimRight(row, "\r"), "\t", 12)
		if len(f) < 12 || f[0] != "5" {
			continue
		}
		text := strings.TrimSpace(f[11])
		if text == "" {
			continue
		}
		conf, _ := strconv.ParseFloat(f[10], 64)
		r.Words = append(r.Words, Word{Text: text, Conf: conf})
		key := f[1] + "." + f[2] + "." + f[3] + "." + f[4]
		if key != lastLine && len(cur) > 0 {
			lines = append(lines, strings.Join(cur, " "))
			cur = nil
		}
		lastLine = key
		cur = append(cur, text)
	}
	if len(cur) > 0 {
		lines = append(lines, strings.Join(cur, " "))
	}
	r.Text = strings.Join(lines, "\n")
	return r
}
