package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// DocChange is the owner's refinement of one document. Manual facts outrank
// batches, rules and models, and index never redoes them (ADR-0006).
type DocChange struct {
	Link   []string // persons to link
	Unlink []string // persons this document is not about, whatever batch or rule say
	Attrs  []Attr   // characteristics to give
	Unset  []Attr   // characteristics it does not have; an empty Value means the whole key
	// Date: nil leaves it; "" says the document has no date; otherwise
	// YYYY-MM-DD, YYYY-MM, YYYY or --MM-DD.
	Date      *string
	ClearDate bool // forget the manual date: the derived one returns
}

var dateForm = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|\d{4}-\d{2}|\d{4}|--\d{2}-\d{2})$`)

// CheckDate validates a full or partial date and says whether it is full.
func CheckDate(s string) (full bool, err error) {
	if !dateForm.MatchString(s) {
		return false, fmt.Errorf("дата %q: нужно ГГГГ-ММ-ДД, ГГГГ-ММ, ГГГГ или --ММ-ДД", s)
	}
	layout := map[int]string{10: "2006-01-02", 7: "2006-01", 4: "2006"}[len(s)]
	probe := s
	if s[0] == '-' {
		layout, probe = "2006-01-02", "2000"+s[1:] // a leap year, so --02-29 passes
	}
	if _, err := time.Parse(layout, probe); err != nil {
		return false, fmt.Errorf("дата %q: такой даты нет", s)
	}
	return len(s) == 10 && s[0] != '-', nil
}

// ChangeDocument applies the owner's refinement. Derived links and
// characteristics follow on the next index.
func (c *Catalog) ChangeDocument(ctx context.Context, docID string, ch DocChange, at time.Time) error {
	if ch.Date != nil && *ch.Date != "" {
		if _, err := CheckDate(*ch.Date); err != nil {
			return err
		}
	}
	ts := at.UTC().Format(time.RFC3339Nano)
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents WHERE id = ?", docID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNoDocument, docID)
	}
	exec := func(q string, args ...any) {
		if err == nil {
			_, err = tx.ExecContext(ctx, q, args...)
		}
	}
	for _, code := range ch.Link {
		var k int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM persons WHERE code = ?", code).Scan(&k); err != nil {
			return err
		}
		if k == 0 {
			return fmt.Errorf("человека с кодом %s нет", code)
		}
		exec("DELETE FROM doc_rejected WHERE doc_id = ? AND kind = 'person' AND key = ?", docID, code)
		exec(`INSERT INTO doc_persons (doc_id, code, method, reason) VALUES (?, ?, 'manual', 'вручную')
			ON CONFLICT(doc_id, code) DO UPDATE SET method = 'manual', reason = 'вручную'`, docID, code)
	}
	for _, code := range ch.Unlink {
		exec("DELETE FROM doc_persons WHERE doc_id = ? AND code = ?", docID, code)
		exec("INSERT OR IGNORE INTO doc_rejected (doc_id, kind, key, value, created) VALUES (?, 'person', ?, '', ?)", docID, code, ts)
	}
	for _, a := range ch.Unset {
		if a.Value == "" {
			exec("DELETE FROM doc_attrs WHERE doc_id = ? AND key = ?", docID, a.Key)
			exec("DELETE FROM doc_rejected WHERE doc_id = ? AND kind = 'attr' AND key = ?", docID, a.Key)
		} else {
			exec("DELETE FROM doc_attrs WHERE doc_id = ? AND key = ? AND value = ?", docID, a.Key, a.Value)
		}
		exec("INSERT OR IGNORE INTO doc_rejected (doc_id, kind, key, value, created) VALUES (?, 'attr', ?, ?, ?)", docID, a.Key, a.Value, ts)
	}
	for _, a := range ch.Attrs {
		// A whole-key rejection stays: it keeps the batch's values of that key
		// out, while the owner's own value is manual and passes.
		exec("DELETE FROM doc_rejected WHERE doc_id = ? AND kind = 'attr' AND key = ? AND value = ?", docID, a.Key, a.Value)
		exec(`INSERT INTO doc_attrs (doc_id, key, value, source) VALUES (?, ?, ?, 'manual')
			ON CONFLICT(doc_id, key, value) DO UPDATE SET source = 'manual'`, docID, a.Key, a.Value)
	}
	switch {
	case ch.ClearDate:
		exec("DELETE FROM doc_manual_dates WHERE doc_id = ?", docID)
	case ch.Date != nil:
		exec(`INSERT INTO doc_manual_dates (doc_id, date, created) VALUES (?, ?, ?)
			ON CONFLICT(doc_id) DO UPDATE SET date = excluded.date, created = excluded.created`, docID, *ch.Date, ts)
	}
	if err != nil {
		return fmt.Errorf("change %s: %w", docID, err)
	}
	return tx.Commit()
}

// Rejected is what the owner said a document is not: persons, and
// characteristic values (an empty value stands for the whole key).
type Rejected struct {
	Persons map[string]bool
	Attrs   map[Attr]bool
}

// Rejects says whether a derived characteristic is ruled out.
func (r Rejected) Rejects(a Attr) bool {
	return r.Attrs[a] || r.Attrs[Attr{Key: a.Key}]
}

// DocRejected returns what must not be derived for a document.
func (c *Catalog) DocRejected(ctx context.Context, docID string) (Rejected, error) {
	r := Rejected{Persons: map[string]bool{}, Attrs: map[Attr]bool{}}
	rows, err := c.db.QueryContext(ctx, "SELECT kind, key, value FROM doc_rejected WHERE doc_id = ?", docID)
	if err != nil {
		return r, fmt.Errorf("rejected %s: %w", docID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, key, value string
		if err := rows.Scan(&kind, &key, &value); err != nil {
			return r, err
		}
		if kind == "person" {
			r.Persons[key] = true
		} else {
			r.Attrs[Attr{Key: key, Value: value}] = true
		}
	}
	return r, rows.Err()
}

// ManualDate returns the owner's date of a document; ok is false when the
// owner gave none, and date is "" when the owner said there is no date.
func (c *Catalog) ManualDate(ctx context.Context, docID string) (date string, ok bool, err error) {
	err = c.db.QueryRowContext(ctx, "SELECT date FROM doc_manual_dates WHERE doc_id = ?", docID).Scan(&date)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return date, err == nil, err
}

// PersonLink is one person of a document and why.
type PersonLink struct {
	Code, Method, Reason string
}

// DocLinks returns a document's person links with their provenance.
func (c *Catalog) DocLinks(ctx context.Context, docID string) ([]PersonLink, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT code, method, reason FROM doc_persons WHERE doc_id = ? ORDER BY code", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersonLink
	for rows.Next() {
		var l PersonLink
		if err := rows.Scan(&l.Code, &l.Method, &l.Reason); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SourcedAttr is a characteristic with where it came from.
type SourcedAttr struct {
	Attr
	Source string
}

// DocAttrSources returns a document's characteristics with their provenance.
func (c *Catalog) DocAttrSources(ctx context.Context, docID string) ([]SourcedAttr, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT key, value, source FROM doc_attrs WHERE doc_id = ? ORDER BY key, value", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourcedAttr
	for rows.Next() {
		var a SourcedAttr
		if err := rows.Scan(&a.Key, &a.Value, &a.Source); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
