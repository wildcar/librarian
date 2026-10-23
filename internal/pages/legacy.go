package pages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Word and Excel files of the old binary format: .doc by catdoc, .xls by
// the Python library xlrd (packages catdoc and python3-xlrd).

// ErrNoCatdoc means catdoc is not installed.
var ErrNoCatdoc = errors.New("catdoc is not installed: old .doc files are not read (apt install catdoc)")

func runCatdoc(name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", ErrNoCatdoc
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(errb.String()))
	}
	if out.Len() > MaxTextBytes {
		return "", fmt.Errorf("%s: text over %d MB", name, MaxTextBytes>>20)
	}
	return out.String(), nil
}

// ReadDOC returns an old Word document's text. Word 6 and 95 keep text in
// the code page of the Windows that wrote it, and catdoc takes it for
// cp1252: Russian comes out as «Êàôåäðà». Such text is read again as cp1251.
func ReadDOC(file string) ([]string, error) {
	t, err := runCatdoc("catdoc", "-d", "utf-8", "-w", file)
	if err != nil {
		return nil, err
	}
	if misread1251(t) {
		if t, err = runCatdoc("catdoc", "-s", "cp1251", "-d", "utf-8", "-w", file); err != nil {
			return nil, err
		}
	}
	return pagesOf(fastSaved.ReplaceAllString(t, ""), "doc")
}

// fastSaved is catdoc's own note on a fast-saved file, not the document's text.
var fastSaved = regexp.MustCompile(`(?m)^\[This was fast-saved +\d+ times\. Some information is lost\]\n?`)

// misread1251 tells cp1251 text decoded as cp1252: letters of À–ÿ make most
// of the letters and there is no Cyrillic at all.
func misread1251(t string) bool {
	var latin1, letters int
	for _, r := range t {
		switch {
		case r >= 0x400 && r <= 0x4FF:
			return false
		case r >= 0xC0 && r <= 0xFF && r != 0xD7 && r != 0xF7:
			latin1++
			letters++
		case unicode.IsLetter(r):
			letters++
		}
	}
	return latin1 > 0 && latin1*2 >= letters
}

// xlsScript prints a workbook for ReadXLS: a sheet's name, then its rows,
// cells separated by \x1f; sheets separated by \f. xlrd knows which numbers
// are dates and the workbook's date system; xls2csv shifts some dates by
// four years, so it is not used.
const xlsScript = `
import sys, xlrd
book = xlrd.open_workbook(sys.argv[1])
out = []
for sh in book.sheets():
    rows = [sh.name]
    for r in range(sh.nrows):
        cells = []
        for c in sh.row(r):
            v = c.value
            if c.ctype == xlrd.XL_CELL_DATE:
                try:
                    d = xlrd.xldate.xldate_as_datetime(v, book.datemode)
                    v = d.strftime("%d.%m.%Y" if (d.hour, d.minute, d.second) == (0, 0, 0) else "%d.%m.%Y %H:%M")
                except Exception:
                    pass
            elif c.ctype == xlrd.XL_CELL_NUMBER and v == int(v):
                v = int(v)
            elif c.ctype == xlrd.XL_CELL_BOOLEAN:
                v = "да" if v else "нет"
            elif c.ctype == xlrd.XL_CELL_ERROR:
                v = ""
            cells.append(str(v).replace("\x1f", " ").replace("\f", " ").replace("\n", " "))
        rows.append("\x1f".join(cells))
    out.append("\n".join(rows))
sys.stdout.write("\f".join(out))
`

// ErrNoXlrd means python3-xlrd is not installed.
var ErrNoXlrd = errors.New("python3-xlrd is not installed: old .xls files are not read (apt install python3-xlrd)")

// ReadXLS returns an old Excel workbook's text in the form ReadXLSX gives:
// each sheet under its name, a row a line, cells separated by « | », dates
// as ДД.ММ.ГГГГ.
func ReadXLS(file string) ([]string, error) {
	py, err := exec.LookPath("python3")
	if err != nil {
		return nil, ErrNoXlrd
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, py, "-I", "-c", xlsScript, file)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if strings.Contains(errb.String(), "No module named 'xlrd'") {
			return nil, ErrNoXlrd
		}
		return nil, fmt.Errorf("xls: %v: %s", err, lastLine(errb.String()))
	}
	var sheets []string
	for _, sheet := range strings.Split(out.String(), "\f") {
		name, rows, _ := strings.Cut(sheet, "\n")
		var b strings.Builder
		for _, line := range strings.Split(rows, "\n") {
			cells := strings.Split(line, "\x1f")
			for j := range cells {
				cells[j] = strings.TrimSpace(cells[j])
			}
			b.WriteString(row(cells))
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			sheets = append(sheets, "Лист «"+name+"»\n"+s)
		}
	}
	return pagesOf(strings.Join(sheets, "\n\n"), "xls")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// OfficeTypes are the office formats read as text, with their readers.
var OfficeTypes = map[string]func(string) ([]string, error){
	DOCX: ReadDOCX, XLSX: ReadXLSX, DOC: ReadDOC, XLS: ReadXLS,
}
