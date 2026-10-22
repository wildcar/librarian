package pages

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipped writes an office file from its parts.
func zipped(t *testing.T, name string, parts map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for n, body := range parts {
		w, _ := z.Create(n)
		w.Write([]byte(body))
	}
	z.Close()
	f.Close()
	return p
}

const w = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func TestReadDOCX(t *testing.T) {
	p := zipped(t, "a.docx", map[string]string{
		"word/document.xml": `<w:document ` + w + `><w:body>
<w:p><w:r><w:t>Заключение</w:t></w:r><w:r><w:tab/><w:t xml:space="preserve">кардиолога </w:t></w:r></w:p>
<w:p><w:r><w:instrText>PAGE</w:instrText><w:t>от 02.04.2022</w:t><w:br/><w:t>Жалоб нет</w:t></w:r></w:p>
<w:p/><w:p/><w:p/>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Показатель</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Значение</w:t></w:r></w:p><w:p><w:r><w:t>(норма)</w:t></w:r></w:p></w:tc><w:tc><w:p/></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>ЧСС</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>72</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:r><w:delText>стёрто</w:delText><w:t>Врач</w:t></w:r></w:p></w:body></w:document>`,
		"word/header1.xml": `<w:hdr ` + w + `><w:p><w:r><w:t>Клиника</w:t></w:r></w:p></w:hdr>`,
		"word/header2.xml": `<w:hdr ` + w + `><w:p><w:r><w:t>Клиника</w:t></w:r></w:p></w:hdr>`,
		"word/footer1.xml": `<w:ftr ` + w + `><w:p><w:r><w:t>стр.</w:t></w:r></w:p></w:ftr>`,
	})
	got, err := ReadDOCX(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "Клиника\n\nЗаключение\tкардиолога\nот 02.04.2022\nЖалоб нет\n\nПоказатель | Значение (норма)\nЧСС | 72\nВрач\n\nстр."
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if _, err := ReadDOCX(zipped(t, "empty.docx", map[string]string{"word/document.xml": `<w:document ` + w + `><w:body/></w:document>`})); err == nil {
		t.Error("an empty document is read")
	}
	if _, err := ReadDOCX(zipped(t, "x.docx", map[string]string{"a.txt": "x"})); err == nil {
		t.Error("a zip without a document is read")
	}
}

func TestReadXLSX(t *testing.T) {
	const ns = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	p := zipped(t, "a.xlsx", map[string]string{
		"xl/workbook.xml": `<workbook ` + ns + `><sheets><sheet name="Анализы" sheetId="1" r:id="rId1"/><sheet name="Пусто" sheetId="2" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="/xl/worksheets/sheet2.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<sst ` + ns + `><si><t>Показатель</t></si><si><r><t>Гемо</t></r><r><t>глобин</t></r></si></sst>`,
		"xl/styles.xml": `<styleSheet ` + ns + `><numFmts><numFmt numFmtId="164" formatCode="[$-419]DD\.MM\.YYYY;@"/><numFmt numFmtId="165" formatCode="0.0&quot; d&quot;"/></numFmts>
<cellXfs><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="165"/><xf numFmtId="22"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml": `<worksheet ` + ns + `><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="inlineStr"><is><t>Дата</t></is></c></row>
<row r="2"><c r="A2" t="s"><v>1</v></c><c r="B2"><v>135</v></c><c r="C2" s="1"><v>44653</v></c><c r="D2" s="2"><v>44654</v></c><c r="E2" s="3"><v>5</v></c><c r="F2" s="4"><v>44653.5</v></c><c r="G2" t="b"><v>1</v></c></row>
<row r="4"><c r="A4"/></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet ` + ns + `><sheetData/></worksheet>`,
	})
	got, err := ReadXLSX(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "Лист «Анализы»\nПоказатель |  | Дата\nГемоглобин | 135 | 02.04.2022 | 03.04.2022 | 5 | 02.04.2022 12:00 | да"
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestReadXLS(t *testing.T) {
	got, err := ReadXLS("testdata/sample.xls")
	if errors.Is(err, ErrNoXlrd) {
		t.Skip(err)
	}
	if err != nil || len(got) != 1 || got[0] != "Лист «Анализы»\nПоказатель | Значение | Дата\nГемоглобин | 135 | 02.04.2022" {
		t.Errorf("%q %v", got, err)
	}
	t.Setenv("PATH", "")
	if _, err := ReadXLS("testdata/sample.xls"); !errors.Is(err, ErrNoXlrd) {
		t.Errorf("no python: %v", err)
	}
	if _, err := ReadDOC("testdata/sample.xls"); !errors.Is(err, ErrNoCatdoc) {
		t.Errorf("no catdoc: %v", err)
	}
}

func TestOfficeTypesAreRead(t *testing.T) {
	for _, m := range []string{DOCX, XLSX, DOC, XLS} {
		if OfficeTypes[m] == nil {
			t.Errorf("%s not read", m)
		}
	}
	if !strings.Contains(DOCX, "wordprocessingml") {
		t.Error(DOCX)
	}
}
