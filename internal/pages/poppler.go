// Package pages turns an original into page images and text: the text layer of
// a PDF where it exists, page images for OCR where it does not.
package pages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Poppler runs the poppler command-line tools (pdftotext, pdftoppm). They are
// an external dependency (ADR-0001): poppler-utils on Linux, the poppler
// package on Windows.
type Poppler struct {
	Dir string // where the binaries are; empty means look them up on PATH
}

// ErrNoPoppler means the tools are not installed or not found.
var ErrNoPoppler = errors.New("poppler tools not found: install poppler-utils or set LIBRARIAN_POPPLER_DIR")

func (p Poppler) bin(name string) (string, error) {
	if p.Dir != "" {
		for _, n := range []string{name, name + ".exe"} {
			c := filepath.Join(p.Dir, n)
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
		return "", fmt.Errorf("%w (looked in %s)", ErrNoPoppler, p.Dir)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", ErrNoPoppler
	}
	return path, nil
}

// Check verifies that both tools can be run.
func (p Poppler) Check() error {
	for _, n := range []string{"pdftotext", "pdftoppm"} {
		if _, err := p.bin(n); err != nil {
			return err
		}
	}
	return nil
}

// TextLayer returns the text layer of every page, in order. A page without a
// text layer comes back as an empty string.
func (p Poppler) TextLayer(ctx context.Context, pdf string) ([]string, error) {
	bin, err := p.bin("pdftotext")
	if err != nil {
		return nil, err
	}
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-enc", "UTF-8", "-layout", pdf, "-")
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftotext %s: %w: %s", pdf, err, strings.TrimSpace(stderr.String()))
	}
	pages := strings.Split(out.String(), "\f")
	if n := len(pages); n > 0 && strings.TrimSpace(pages[n-1]) == "" {
		pages = pages[:n-1] // pdftotext ends every page, the last one too, with \f
	}
	return pages, nil
}

// MeaningfulText reports whether a text layer is worth more than OCR: enough
// letters, not a scanner's stray hidden layer.
func MeaningfulText(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters >= 100
}

// Render writes one grayscale JPEG per page into dir, the long side scaled to
// longSide pixels, and returns their paths in page order. These are the
// settings the OCR bake-off used (ADR-0002).
func (p Poppler) Render(ctx context.Context, pdf, dir string, longSide int) ([]string, error) {
	bin, err := p.bin("pdftoppm")
	if err != nil {
		return nil, err
	}
	return p.render(ctx, bin, pdf, dir, "jpg",
		"-gray", "-jpeg", "-jpegopt", "quality=88", "-scale-to", fmt.Sprint(longSide))
}

// RenderPNG writes one grayscale PNG per page at dpi: the input for the
// tesseract second reading, which wants full resolution.
func (p Poppler) RenderPNG(ctx context.Context, pdf, dir string, dpi int) ([]string, error) {
	bin, err := p.bin("pdftoppm")
	if err != nil {
		return nil, err
	}
	return p.render(ctx, bin, pdf, dir, "png", "-gray", "-png", "-r", fmt.Sprint(dpi))
}

func (p Poppler) render(ctx context.Context, bin, pdf, dir, ext string, opts ...string) ([]string, error) {
	prefix := filepath.Join(dir, "page")
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, append(opts, pdf, prefix)...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm %s: %w: %s", pdf, err, strings.TrimSpace(stderr.String()))
	}
	files, err := filepath.Glob(prefix + "-*." + ext)
	if err != nil {
		return nil, err
	}
	// pdftoppm pads page numbers to the width of the largest one, so a plain
	// sort is page order within one run.
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("pdftoppm %s: no pages rendered", pdf)
	}
	return files, nil
}
