package extract_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/extract"
	"github.com/wildcar/librarian/internal/intake"
	"github.com/wildcar/librarian/internal/pages"
	"github.com/wildcar/librarian/internal/router"
)

// makePDF builds a minimal valid PDF; each entry of texts is one page, an
// empty string gives a page with no text layer (a "scan").
func makePDF(texts ...string) []byte {
	var objs []string
	kids := []string{}
	n := 3 + 2*len(texts) // catalog, pages, font, then page+content pairs
	_ = n
	objs = append(objs, "<< /Type /Catalog /Pages 2 0 R >>")
	for i := range texts {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+2*i))
	}
	objs = append(objs, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(texts)))
	objs = append(objs, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i, t := range texts {
		content := ""
		if t != "" {
			content = fmt.Sprintf("BT /F1 10 Tf 40 700 Td (%s) Tj ET", t)
		}
		objs = append(objs, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		objs = append(objs, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// fakeOCR answers every page; pages listed in fail get an error.
type fakeOCR struct {
	mu    sync.Mutex
	calls int
	fail  map[int]bool // call numbers (1-based) that fail
}

func (f *fakeOCR) Chat(ctx context.Context, r router.ChatRequest) (router.ChatReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail[f.calls] {
		return router.ChatReply{}, errors.New("provider rate limit")
	}
	if len(r.ImagesB64) != 1 || r.ImageMIME != "image/jpeg" || r.Account == "" {
		return router.ChatReply{}, fmt.Errorf("bad request: %d images, %s, account %q", len(r.ImagesB64), r.ImageMIME, r.Account)
	}
	return router.ChatReply{Text: fmt.Sprintf("распознанный текст %d", f.calls), Model: r.Model, CostUSD: 0.01}, nil
}

func poppler(t *testing.T) pages.Poppler {
	p := pages.Poppler{Dir: os.Getenv("LIBRARIAN_POPPLER_DIR")}
	if err := p.Check(); err != nil {
		t.Skipf("poppler not available (%v); set LIBRARIAN_POPPLER_DIR to run", err)
	}
	return p
}

type env struct {
	src   string
	cat   *catalog.Catalog
	stage *extract.Stage
	ocr   *fakeOCR
}

func setup(t *testing.T, files map[string][]byte) *env {
	t.Helper()
	src := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(src, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, err := account.Create(t.TempDir(), "family")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := intake.Folder(context.Background(), c, src, 2); err != nil {
		t.Fatal(err)
	}
	ocr := &fakeOCR{fail: map[int]bool{}}
	return &env{src: src, cat: c, ocr: ocr, stage: &extract.Stage{Catalog: c, Poppler: poppler(t), OCR: ocr}}
}

func (e *env) run(t *testing.T, dry bool) *extract.Report {
	t.Helper()
	rep, err := e.stage.Run(context.Background(), extract.Options{Account: "family", Workers: 2, DryRun: dry})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

var latin = strings.Repeat("Echocardiography protocol, left ventricle normal. ", 4)

func TestTextLayerAndOCR(t *testing.T) {
	var png bytes.Buffer
	if err := pngEncode(&png); err != nil {
		t.Fatal(err)
	}
	e := setup(t, map[string][]byte{
		"digital.pdf": makePDF(latin),
		"scan.pdf":    makePDF("", ""),
		"photo.png":   png.Bytes(),
		"notes.txt":   []byte("Plain text is read as it is."),
		"archive.zip": append([]byte("PK\x03\x04"), make([]byte, 40)...),
	})

	dry := e.run(t, true)
	if e.ocr.calls != 0 || dry.PagesOCR != 3 || dry.PagesTextLayer != 2 {
		t.Fatalf("dry run: calls=%d ocr=%d text=%d, want 0 3 2", e.ocr.calls, dry.PagesOCR, dry.PagesTextLayer)
	}

	rep := e.run(t, false)
	if rep.Done != 4 || rep.Unsupported != 1 || rep.Failed != 0 {
		t.Fatalf("run: done=%d unsupported=%d failed=%d errors=%v", rep.Done, rep.Unsupported, rep.Failed, rep.Errors)
	}
	if rep.PagesTextLayer != 2 || rep.PagesOCR != 3 || e.ocr.calls != 3 {
		t.Errorf("pages: text=%d ocr=%d calls=%d, want 2 3 3", rep.PagesTextLayer, rep.PagesOCR, e.ocr.calls)
	}
	if rep.CostUSD < 0.0299 || rep.CostUSD > 0.0301 {
		t.Errorf("cost = %v, want 0.03", rep.CostUSD)
	}

	sha, _ := intake.HashFile(filepath.Join(e.src, "digital.pdf"))
	ps, err := e.cat.Pages(context.Background(), sha)
	if err != nil || len(ps) != 1 || ps[0].Method != "text-layer" || !strings.Contains(ps[0].Text, "Echocardiography") {
		t.Errorf("digital.pdf pages = %+v, %v", ps, err)
	}
	sha, _ = intake.HashFile(filepath.Join(e.src, "scan.pdf"))
	ps, _ = e.cat.Pages(context.Background(), sha)
	if len(ps) != 2 || ps[0].Method != "ocr" || ps[0].Engine != extract.DefaultModel {
		t.Errorf("scan.pdf pages = %+v", ps)
	}

	sha, _ = intake.HashFile(filepath.Join(e.src, "notes.txt"))
	ps, _ = e.cat.Pages(context.Background(), sha)
	if len(ps) != 1 || ps[0].Engine != "file" || ps[0].Text != "Plain text is read as it is." {
		t.Errorf("notes.txt pages = %+v", ps)
	}

	// Nothing is left: a second run calls no model and the archive is not retried.
	again := e.run(t, false)
	if again.Originals != 0 || e.ocr.calls != 3 {
		t.Errorf("second run: originals=%d calls=%d, want 0 3", again.Originals, e.ocr.calls)
	}
}

func TestFailedPageIsRetriedAlone(t *testing.T) {
	e := setup(t, map[string][]byte{"scan.pdf": makePDF("", "", "")})
	e.ocr.fail[2] = true
	rep := e.run(t, false)
	if rep.Failed != 1 || rep.PagesOCR != 2 || len(rep.Errors) != 1 {
		t.Fatalf("first run: failed=%d ocr=%d errors=%v", rep.Failed, rep.PagesOCR, rep.Errors)
	}
	rep = e.run(t, false)
	if rep.Done != 1 || rep.PagesOCR != 1 || rep.PagesSkipped != 2 || e.ocr.calls != 4 {
		t.Errorf("retry: done=%d ocr=%d skipped=%d calls=%d, want 1 1 2 4", rep.Done, rep.PagesOCR, rep.PagesSkipped, e.ocr.calls)
	}
}

func TestChangedSourceIsNotRead(t *testing.T) {
	e := setup(t, map[string][]byte{"scan.pdf": makePDF("")})
	if err := os.WriteFile(filepath.Join(e.src, "scan.pdf"), makePDF("", ""), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := e.run(t, false)
	if rep.Failed != 1 || e.ocr.calls != 0 || !strings.Contains(strings.Join(rep.Errors, " "), "changed since intake") {
		t.Errorf("changed source: failed=%d calls=%d errors=%v", rep.Failed, e.ocr.calls, rep.Errors)
	}
}

// fakeReader is a second reader: it "sees" the words the fake OCR returns,
// plus, on pages in extra, twenty sure words the OCR text does not contain.
type fakeReader struct {
	mu    sync.Mutex
	calls int
	extra map[int]bool // call numbers that see text the OCR missed
}

func (f *fakeReader) Read(ctx context.Context, image string) (pages.Reading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if _, err := os.Stat(image); err != nil {
		return pages.Reading{}, fmt.Errorf("check image missing: %w", err)
	}
	var r pages.Reading
	for range 25 {
		r.Words = append(r.Words, pages.Word{Text: "распознанный", Conf: 95})
	}
	if f.extra[f.calls] {
		for i := range 20 {
			r.Words = append(r.Words, pages.Word{Text: fmt.Sprintf("пропущено%d", i), Conf: 95})
		}
	}
	return r, nil
}

func TestSecondReadingFlagsDisagreement(t *testing.T) {
	e := setup(t, map[string][]byte{"scan.pdf": makePDF("", "")})
	rd := &fakeReader{extra: map[int]bool{2: true}}
	e.stage.Checker = rd
	rep := e.run(t, false)
	if rep.PagesChecked != 2 || rep.PagesFlagged != 1 || len(rep.CheckErrors) != 0 {
		t.Fatalf("checked=%d flagged=%d errors=%v, want 2 1 none", rep.PagesChecked, rep.PagesFlagged, rep.CheckErrors)
	}
	fl, err := e.cat.FlaggedPages(context.Background())
	if err != nil || len(fl) != 1 || fl[0].Words != 45 || fl[0].Matched != 25 || !strings.Contains(fl[0].Missing, "пропущено0") {
		t.Errorf("flagged pages = %+v, %v", fl, err)
	}
}

func TestVerifyBackfillsOldPages(t *testing.T) {
	e := setup(t, map[string][]byte{"scan.pdf": makePDF("", "", "")})
	e.run(t, false) // no checker: pages stored without the second reading
	rd := &fakeReader{extra: map[int]bool{}}
	e.stage.Checker = rd
	rep, err := e.stage.Verify(context.Background(), extract.Options{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if rep.PagesChecked != 3 || rd.calls != 3 || rep.PagesFlagged != 0 {
		t.Errorf("verify: checked=%d calls=%d flagged=%d, want 3 3 0", rep.PagesChecked, rd.calls, rep.PagesFlagged)
	}
	rep, _ = e.stage.Verify(context.Background(), extract.Options{Workers: 2})
	if rep.Originals != 0 || rd.calls != 3 {
		t.Errorf("second verify redid work: originals=%d calls=%d", rep.Originals, rd.calls)
	}
}

func pngEncode(b *bytes.Buffer) error {
	img := image.NewGray(image.Rect(0, 0, 3000, 1500)) // larger than LongSide: must be scaled
	return png.Encode(b, img)
}

func TestPrepareImageScales(t *testing.T) {
	var b bytes.Buffer
	if err := pngEncode(&b); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := pages.PrepareImage(p, extract.LongSide)
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "jpeg" || cfg.Width != 2000 || cfg.Height != 1000 {
		t.Errorf("prepared image: %s %dx%d %v, want jpeg 2000x1000", format, cfg.Width, cfg.Height, err)
	}
}
