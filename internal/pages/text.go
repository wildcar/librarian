package pages

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// Plain text files (.txt, .md, .csv) are read as they are: no OCR, no cost.

// TextTypes are the content types read as plain text.
var TextTypes = map[string]bool{"text/plain": true}

// MaxTextBytes bounds a text file; a bigger one is more likely a log or a
// dump than a document.
const MaxTextBytes = 2 << 20

// TextPageRunes is the size of a text "page": about a printed page, so the
// split into documents sees pages as it does for a PDF.
const TextPageRunes = 3500

// ReadText reads a text file and cuts it into pages at paragraph breaks
// (lines when a paragraph is longer than a page). The text is UTF-8, with or
// without a byte-order mark, or else Windows-1251, the usual encoding of
// older Russian text files.
func ReadText(path string) ([]string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > MaxTextBytes {
		return nil, fmt.Errorf("text file over %d MB", MaxTextBytes>>20)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text, err := decodeText(b)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil, errors.New("text file is empty")
	}
	return cutPages(text, TextPageRunes), nil
}

func decodeText(b []byte) (string, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if bytes.HasPrefix(b, []byte("\xff\xfe")) || bytes.HasPrefix(b, []byte("\xfe\xff")) {
		return "", errors.New("UTF-16 text is not read yet")
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", errors.New("not a text file")
	}
	if utf8.Valid(b) {
		return string(b), nil
	}
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c >= 0xC0:
			sb.WriteRune(rune(0x410 + int(c) - 0xC0)) // А…я
		default:
			sb.WriteRune(cp1251[c-0x80])
		}
	}
	return sb.String(), nil
}

// cp1251 maps Windows-1251 bytes 0x80–0xBF.
var cp1251 = [64]rune{
	'Ђ', 'Ѓ', '‚', 'ѓ', '„', '…', '†', '‡', '€', '‰', 'Љ', '‹', 'Њ', 'Ќ', 'Ћ', 'Џ',
	'ђ', '‘', '’', '“', '”', '•', '–', '—', '�', '™', 'љ', '›', 'њ', 'ќ', 'ћ', 'џ',
	' ', 'Ў', 'ў', 'Ј', '¤', 'Ґ', '¦', '§', 'Ё', '©', 'Є', '«', '¬', '­', '®', 'Ї',
	'°', '±', 'І', 'і', 'ґ', 'µ', '¶', '·', 'ё', '№', 'є', '»', 'ј', 'Ѕ', 'ѕ', 'ї',
}

// cutPages packs paragraphs into pages of at most max runes; a paragraph
// longer than a page is cut at lines, and a line longer than a page at max.
func cutPages(text string, max int) []string {
	var out []string
	var cur strings.Builder
	n := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
		n = 0
	}
	add := func(piece, sep string) {
		l := utf8.RuneCountInString(piece)
		if n > 0 && n+l > max {
			flush()
		}
		if n > 0 {
			cur.WriteString(sep)
		}
		cur.WriteString(piece)
		n += l + len(sep)
	}
	for _, para := range strings.Split(text, "\n\n") {
		if utf8.RuneCountInString(para) <= max {
			add(para, "\n\n")
			continue
		}
		for _, line := range strings.Split(para, "\n") {
			for r := []rune(line); len(r) > 0; {
				k := min(len(r), max)
				add(string(r[:k]), "\n")
				r = r[k:]
			}
		}
	}
	flush()
	return out
}
