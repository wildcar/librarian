package pages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadText(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// UTF-8 with a byte-order mark and Windows line ends.
	ps, err := ReadText(write("bom.md", []byte("\xef\xbb\xbf# Анализ крови\r\n\r\nГемоглобин 140\r\n")))
	if err != nil || len(ps) != 1 || ps[0] != "# Анализ крови\n\nГемоглобин 140" {
		t.Errorf("utf-8: %q, %v", ps, err)
	}
	// Windows-1251: «Ёлка № 1».
	ps, err = ReadText(write("old.txt", []byte{0xAB, 0xA8, 0xEB, 0xEA, 0xE0, ' ', 0xB9, ' ', '1', 0xBB}))
	if err != nil || len(ps) != 1 || ps[0] != "«Ёлка № 1»" {
		t.Errorf("cp1251: %q, %v", ps, err)
	}
	// Long text: pages at paragraph breaks, none over the size.
	para := strings.Repeat("слово ", 200) // 1200 runes
	ps, err = ReadText(write("long.txt", []byte(strings.Repeat(para+"\n\n", 6))))
	if err != nil || len(ps) != 3 {
		t.Fatalf("long: %d pages, %v", len(ps), err)
	}
	for _, p := range ps {
		if utf8.RuneCountInString(p) > TextPageRunes {
			t.Errorf("page of %d runes", utf8.RuneCountInString(p))
		}
	}
	// One huge line is cut at the page size.
	ps, _ = ReadText(write("line.txt", []byte(strings.Repeat("я", 8000))))
	if len(ps) != 3 || utf8.RuneCountInString(ps[0]) != TextPageRunes {
		t.Errorf("line: %d pages", len(ps))
	}
	for name, b := range map[string][]byte{"empty.txt": []byte(" \n "), "bin.txt": {'a', 0, 'b'}, "u16.txt": {0xff, 0xfe, 'a', 0}} {
		if _, err := ReadText(write(name, b)); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}
