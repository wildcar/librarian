package pages

import (
	"context"
	"os"
	"testing"
)

func TestParseTSV(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"1\t1\t0\t0\t0\t0\t0\t0\t100\t100\t-1\t\n" +
		"4\t1\t1\t1\t1\t0\t0\t0\t10\t10\t-1\t\n" +
		"5\t1\t1\t1\t1\t1\t0\t0\t10\t10\t96.5\tПротокол\n" +
		"5\t1\t1\t1\t1\t2\t0\t0\t10\t10\t91\tобследования\r\n" +
		"5\t1\t1\t1\t2\t1\t0\t0\t10\t10\t30\tПациент:\n" +
		"5\t1\t1\t1\t2\t2\t0\t0\t10\t10\t-1\t \n"
	r := parseTSV(tsv)
	if len(r.Words) != 3 || r.Words[0].Text != "Протокол" || r.Words[0].Conf != 96.5 || r.Words[1].Text != "обследования" {
		t.Fatalf("words = %+v", r.Words)
	}
	if r.Text != "Протокол обследования\nПациент:" {
		t.Errorf("text = %q", r.Text)
	}
}

func TestTesseractReal(t *testing.T) {
	tz := Tesseract{Exe: os.Getenv("LIBRARIAN_TESSERACT"), Tessdata: os.Getenv("LIBRARIAN_TESSDATA")}
	if err := tz.Check(context.Background()); err != nil {
		t.Skipf("tesseract not available (%v)", err)
	}
	pop := Poppler{Dir: os.Getenv("LIBRARIAN_POPPLER_DIR")}
	if err := pop.Check(); err != nil {
		t.Skipf("poppler not available (%v)", err)
	}
	dir := t.TempDir()
	pdf := dir + "/p.pdf"
	if err := os.WriteFile(pdf, minimalPDF("Echocardiography protocol left ventricle normal"), 0o644); err != nil {
		t.Fatal(err)
	}
	imgs, err := pop.RenderPNG(context.Background(), pdf, dir, 300)
	if err != nil || len(imgs) != 1 {
		t.Fatalf("render: %v %v", imgs, err)
	}
	r, err := tz.Read(context.Background(), imgs[0])
	if err != nil {
		t.Fatal(err)
	}
	sure := 0
	for _, w := range r.Words {
		if w.Conf >= 80 {
			sure++
		}
	}
	if sure < 4 {
		t.Errorf("tesseract read %q with %d confident words", r.Text, sure)
	}
}

// minimalPDF is one page with one line of Helvetica text.
func minimalPDF(text string) []byte {
	content := "BT /F1 18 Tf 40 700 Td (" + text + ") Tj ET"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Length " + itoa(len(content)) + " >>\nstream\n" + content + "\nendstream",
	}
	b := []byte("%PDF-1.4\n")
	var offs []int
	for i, o := range objs {
		offs = append(offs, len(b))
		b = append(b, []byte(itoa(i+1)+" 0 obj\n"+o+"\nendobj\n")...)
	}
	x := len(b)
	b = append(b, []byte("xref\n0 6\n0000000000 65535 f \n")...)
	for _, o := range offs {
		s := itoa(o)
		for len(s) < 10 {
			s = "0" + s
		}
		b = append(b, []byte(s+" 00000 n \n")...)
	}
	b = append(b, []byte("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n"+itoa(x)+"\n%%EOF\n")...)
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
