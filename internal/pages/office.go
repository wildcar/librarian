package pages

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Office documents are read as text, like plain text files: Word and Excel
// files of the current format (zipped XML) by this package, older binary
// ones by catdoc (see legacy.go). No OCR, no cost.

const (
	DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	XLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	DOC  = "application/msword"
	XLS  = "application/vnd.ms-excel"
)

// maxPartBytes bounds one unzipped part, so a zip bomb stops early.
const maxPartBytes = 64 << 20

// ReadDOCX returns a Word document's text: headers first, then the body
// (table cells separated by « | », a row a line), then footers.
func ReadDOCX(file string) ([]string, error) {
	z, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("docx: %w", err)
	}
	defer z.Close()
	body, err := partText(&z.Reader, "word/document.xml", docxText)
	if err != nil {
		return nil, err
	}
	var heads, foots []string
	seen := map[string]bool{}
	for _, f := range sortedParts(&z.Reader) {
		isHead := strings.HasPrefix(f, "word/header")
		if !isHead && !strings.HasPrefix(f, "word/footer") {
			continue
		}
		t, err := partText(&z.Reader, f, docxText)
		if err != nil || t == "" || seen[t] {
			continue
		}
		seen[t] = true
		if isHead {
			heads = append(heads, t)
		} else {
			foots = append(foots, t)
		}
	}
	return pagesOf(strings.Join(append(append(heads, body), foots...), "\n\n"), "docx")
}

// docxText walks WordprocessingML: text runs, tabs and breaks; paragraphs
// end lines, except inside a table cell, where they join with a space.
func docxText(d *xml.Decoder) (string, error) {
	var b strings.Builder
	var cells []string // the current row's cells
	var cell *strings.Builder
	inText, depth := false, 0
	out := func() *strings.Builder {
		if cell != nil {
			return cell
		}
		return &b
	}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				out().WriteString("\t")
			case "br", "cr":
				out().WriteString("\n")
			case "noBreakHyphen":
				out().WriteString("-")
			case "tbl":
				depth++
			case "tc":
				if depth == 1 {
					cell = &strings.Builder{}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if cell != nil {
					cell.WriteString(" ")
				} else {
					b.WriteString("\n")
				}
			case "tc":
				if depth == 1 && cell != nil {
					cells = append(cells, strings.TrimSpace(cell.String()))
					cell = nil
				}
			case "tr":
				if depth == 1 {
					b.WriteString(row(cells))
					cells = nil
				}
			case "tbl":
				depth--
			}
		case xml.CharData:
			if inText {
				out().Write(t)
			}
		}
	}
	return tidy(b.String()), nil
}

// row writes table cells as one line, trailing empty cells dropped.
func row(cells []string) string {
	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	if len(cells) == 0 {
		return ""
	}
	return strings.Join(cells, " | ") + "\n"
}

// ReadXLSX returns a workbook's text: each sheet under its name, a row a
// line, cells separated by « | ». Dates are written as dates, not as the
// day numbers Excel keeps them as.
func ReadXLSX(file string) ([]string, error) {
	z, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("xlsx: %w", err)
	}
	defer z.Close()
	var wb struct {
		Pr struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := partXML(&z.Reader, "xl/workbook.xml", &wb); err != nil {
		return nil, err
	}
	var rels struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := partXML(&z.Reader, "xl/_rels/workbook.xml.rels", &rels); err != nil {
		return nil, err
	}
	target := map[string]string{}
	for _, r := range rels.Rels {
		t := r.Target
		if strings.HasPrefix(t, "/") {
			t = strings.TrimPrefix(t, "/")
		} else {
			t = path.Join("xl", t)
		}
		target[r.ID] = t
	}
	var ss struct {
		Items []struct {
			T    string `xml:"t"`
			Runs []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := partXML(&z.Reader, "xl/sharedStrings.xml", &ss); err != nil && !errors.Is(err, errNoPart) {
		return nil, err
	}
	strs := make([]string, len(ss.Items))
	for i, it := range ss.Items {
		s := it.T
		for _, r := range it.Runs {
			s += r.T
		}
		strs[i] = s
	}
	dates, err := dateStyles(&z.Reader)
	if err != nil {
		return nil, err
	}
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if wb.Pr.Date1904 == "1" || wb.Pr.Date1904 == "true" {
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	var out []string
	for _, sh := range wb.Sheets {
		t, err := partText(&z.Reader, target[sh.RID], func(d *xml.Decoder) (string, error) {
			return sheetText(d, strs, dates, epoch)
		})
		if err != nil {
			return nil, err
		}
		if t != "" {
			out = append(out, "Лист «"+sh.Name+"»\n"+t)
		}
	}
	return pagesOf(strings.Join(out, "\n\n"), "xlsx")
}

type xlsxCell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Style  int    `xml:"s,attr"`
	Value  string `xml:"v"`
	Inline struct {
		T    string `xml:"t"`
		Runs []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}

func sheetText(d *xml.Decoder, strs []string, dates map[int]bool, epoch time.Time) (string, error) {
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "row" {
			continue
		}
		var r struct {
			Cells []xlsxCell `xml:"c"`
		}
		if err := d.DecodeElement(&r, &se); err != nil {
			return "", err
		}
		var cells []string
		for _, c := range r.Cells {
			if col := column(c.Ref); col > len(cells) && col < 1000 {
				cells = append(cells, make([]string, col-len(cells))...)
			}
			cells = append(cells, strings.TrimSpace(cellText(c, strs, dates, epoch)))
		}
		b.WriteString(row(cells))
	}
	return strings.TrimSpace(b.String()), nil
}

func cellText(c xlsxCell, strs []string, dates map[int]bool, epoch time.Time) string {
	switch c.Type {
	case "s":
		i, err := strconv.Atoi(c.Value)
		if err != nil || i < 0 || i >= len(strs) {
			return ""
		}
		return strs[i]
	case "inlineStr":
		s := c.Inline.T
		for _, r := range c.Inline.Runs {
			s += r.T
		}
		return s
	case "b":
		if c.Value == "1" {
			return "да"
		}
		return "нет"
	case "str", "e":
		return c.Value
	}
	if !dates[c.Style] {
		return c.Value
	}
	f, err := strconv.ParseFloat(c.Value, 64)
	if err != nil || f < 0 || f > 2958465 { // 9999-12-31
		return c.Value
	}
	days, frac := math.Modf(f)
	t := epoch.AddDate(0, 0, int(days)).Add(time.Duration(math.Round(frac*86400)) * time.Second)
	if frac == 0 {
		return t.Format("02.01.2006")
	}
	if days == 0 {
		return t.Format("15:04")
	}
	return t.Format("02.01.2006 15:04")
}

// column is the zero-based column of a cell reference such as "AB12".
func column(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	return n - 1
}

// dateFormat is a number format that shows a date: it has a day, month or
// year outside quoted text and brackets.
var (
	quotedOrBracketed = regexp.MustCompile(`"[^"]*"|\[[^\]]*\]|\\.`)
	dateLetters       = regexp.MustCompile(`[dDyY]`)
)

// dateStyles lists the cell styles whose number format is a date.
func dateStyles(z *zip.Reader) (map[int]bool, error) {
	var st struct {
		Fmts []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Xfs []struct {
			Fmt int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}
	if err := partXML(z, "xl/styles.xml", &st); err != nil {
		if errors.Is(err, errNoPart) {
			return map[int]bool{}, nil
		}
		return nil, err
	}
	isDate := map[int]bool{14: true, 15: true, 16: true, 17: true, 22: true}
	for _, f := range st.Fmts {
		if dateLetters.MatchString(quotedOrBracketed.ReplaceAllString(f.Code, "")) {
			isDate[f.ID] = true
		}
	}
	out := map[int]bool{}
	for i, x := range st.Xfs {
		out[i] = isDate[x.Fmt]
	}
	return out, nil
}

var errNoPart = errors.New("no such part")

func openPart(z *zip.Reader, name string) (io.ReadCloser, error) {
	for _, f := range z.File {
		if f.Name == name {
			if f.UncompressedSize64 > maxPartBytes {
				return nil, fmt.Errorf("%s: part over %d MB", name, maxPartBytes>>20)
			}
			return f.Open()
		}
	}
	return nil, fmt.Errorf("%s: %w", name, errNoPart)
}

func partXML(z *zip.Reader, name string, v any) error {
	r, err := openPart(z, name)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := xml.NewDecoder(io.LimitReader(r, maxPartBytes)).Decode(v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func partText(z *zip.Reader, name string, read func(*xml.Decoder) (string, error)) (string, error) {
	r, err := openPart(z, name)
	if err != nil {
		return "", err
	}
	defer r.Close()
	t, err := read(xml.NewDecoder(io.LimitReader(r, maxPartBytes)))
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return t, nil
}

func sortedParts(z *zip.Reader) []string {
	var out []string
	for _, f := range z.File {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

var blankLines = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)+`)

// tidy trims trailing spaces and squeezes runs of empty lines into one.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// pagesOf cuts a document's text into pages, refusing an empty one or one
// too big to be a document.
func pagesOf(text, kind string) ([]string, error) {
	text = tidy(text)
	if text == "" {
		return nil, fmt.Errorf("%s: no text", kind)
	}
	if len(text) > MaxTextBytes {
		return nil, fmt.Errorf("%s: text over %d MB", kind, MaxTextBytes>>20)
	}
	return cutPages(text, TextPageRunes), nil
}
