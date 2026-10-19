// Package index derives what search needs from the text layers, without model
// calls: the document date, the full-text index and the links to persons.
// It can run any number of times.
package index

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/layers"
)

// Report summarises one run.
type Report struct {
	Documents int
	Dated     int
	Linked    int // documents with at least one person
	Links     map[string]int
}

// Run reindexes every document with text layers.
func Run(ctx context.Context, c *catalog.Catalog, now time.Time) (*Report, error) {
	persons, err := c.Persons(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := c.IndexEntries(ctx)
	if err != nil {
		return nil, err
	}
	rep := &Report{Documents: len(entries), Links: map[string]int{}}
	for _, e := range entries {
		if d := DateFromSummary(e.Summary, now); d != "" {
			if err := c.SetDocDate(ctx, e.DocID, d, "summary"); err != nil {
				return nil, err
			}
			rep.Dated++
		} else if err := c.SetDocDate(ctx, e.DocID, "", ""); err != nil {
			return nil, err
		}
		if err := c.IndexText(ctx, e.DocID, e.Summary, e.Essential); err != nil {
			return nil, fmt.Errorf("index %s: %w", e.DocID, err)
		}
		verbatim, err := text(ctx, c, e)
		if err != nil {
			return nil, err
		}
		if err := c.ClearRuleLinks(ctx, e.DocID); err != nil {
			return nil, err
		}
		linked := false
		for _, p := range persons {
			if reason := Match(p, verbatim); reason != "" {
				if err := c.LinkPerson(ctx, e.DocID, p.Code, "rule", reason); err != nil {
					return nil, err
				}
				rep.Links[p.Code]++
				linked = true
			}
		}
		if linked {
			rep.Linked++
		}
	}
	return rep, nil
}

func text(ctx context.Context, c *catalog.Catalog, e catalog.IndexEntry) (string, error) {
	ps, err := c.Pages(ctx, e.SHA256)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range ps {
		if p.Page >= e.FirstPage && p.Page <= e.LastPage {
			b.WriteString(p.Text)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// DateFromSummary takes the date of the summary's first sentence, which the
// prompt asks to name the document and its date (ADR-0005). No date, or more
// than one different date there, gives "".
func DateFromSummary(summary string, now time.Time) string {
	first := summary
	for i := 0; i+1 < len(summary); i++ {
		if (summary[i] == '.' || summary[i] == '\n') && (i+1 == len(summary) || summary[i+1] == ' ' || summary[i+1] == '\n') {
			// "от 02.04.2022." ends a sentence; "2. апреля" does not occur.
			first = summary[:i]
			break
		}
	}
	ds := layers.Dates(first)
	if len(ds) == 0 {
		return ""
	}
	d := ds[0]
	for _, o := range ds[1:] {
		if o != d {
			return ""
		}
	}
	year := 2000 + d[2]
	if year > now.Year()+1 {
		year -= 100
	}
	t := time.Date(year, time.Month(d[1]), d[0], 0, 0, 0, 0, time.UTC)
	if t.Day() != d[0] || int(t.Month()) != d[1] {
		return "" // 31.02 and the like
	}
	return t.Format("2006-01-02")
}

func norm(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(strings.ReplaceAll(s, "ё", "е")) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteRune(' ')
			space = true
		}
	}
	return " " + strings.TrimSpace(b.String()) + " "
}

// Match says why a document belongs to a person, or "" if nothing points to
// them: the full name, a spelling from the aliases, or the birth date.
func Match(p catalog.Person, text string) string {
	t := norm(text)
	if strings.Contains(t, norm(p.FullName)) {
		return "полное имя"
	}
	for _, a := range p.Aliases {
		if n := norm(a); len([]rune(strings.TrimSpace(n))) >= 4 && strings.Contains(t, n) {
			return "вариант имени «" + a + "»"
		}
	}
	if p.BirthDate != "" {
		b, err := time.Parse("2006-01-02", p.BirthDate)
		if err == nil {
			want := [3]int{b.Day(), int(b.Month()), b.Year() % 100}
			for _, d := range layers.Dates(text) {
				if d == want {
					return "дата рождения"
				}
			}
		}
	}
	return ""
}
