package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Who refined a document. The owner outranks an assistant; both outrank
// batches, rules and models (ADR-0006).
const (
	OriginOwner     = "owner"
	OriginAssistant = "assistant"
)

// ErrOwnerSet is returned when an assistant tries to change what the owner
// set or ruled out by hand.
var ErrOwnerSet = errors.New("задано владельцем")

// DocChange is a refinement of one document. Refinements outrank batches,
// rules and models, and index never redoes them.
type DocChange struct {
	Link   []string `json:"link,omitempty"`   // persons to link
	Unlink []string `json:"unlink,omitempty"` // persons this document is not about, whatever batch or rule say
	Attrs  []Attr   `json:"attrs,omitempty"`  // characteristics to give
	Unset  []Attr   `json:"unset,omitempty"`  // characteristics it does not have; an empty Value means the whole key
	// Date: nil leaves it; "" says the document has no date; otherwise
	// YYYY-MM-DD, YYYY-MM, YYYY or --MM-DD.
	Date      *string `json:"date,omitempty"`
	ClearDate bool    `json:"clear_date,omitempty"` // forget the refined date: the derived one returns
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

// ChangeDocument applies a refinement by the owner or an assistant and logs
// it with its reason. An assistant cannot undo what the owner set or ruled
// out by hand: the whole change is refused with ErrOwnerSet. Derived links
// and characteristics follow on the next index.
func (c *Catalog) ChangeDocument(ctx context.Context, docID string, ch DocChange, origin, reason string, at time.Time) error {
	if origin != OriginOwner && origin != OriginAssistant {
		return fmt.Errorf("unknown origin %q", origin)
	}
	if ch.Date != nil && *ch.Date != "" {
		if _, err := CheckDate(*ch.Date); err != nil {
			return err
		}
	}
	// The owner's rows: "manual"; an assistant's: "assistant".
	mark := map[string]string{OriginOwner: "manual", OriginAssistant: "assistant"}[origin]
	byAssistant := origin == OriginAssistant
	ts := at.UTC().Format(time.RFC3339Nano)
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	count := func(q string, args ...any) int {
		var n int
		if err == nil {
			err = tx.QueryRowContext(ctx, q, args...).Scan(&n)
		}
		return n
	}
	exec := func(q string, args ...any) {
		if err == nil {
			_, err = tx.ExecContext(ctx, q, args...)
		}
	}
	owner := func(what string) error {
		return fmt.Errorf("%w: %s; изменить может только владелец", ErrOwnerSet, what)
	}

	if count("SELECT COUNT(*) FROM documents WHERE id = ?", docID) == 0 && err == nil {
		return fmt.Errorf("%w: %s", ErrNoDocument, docID)
	}
	for _, code := range ch.Link {
		if count("SELECT COUNT(*) FROM persons WHERE code = ?", code) == 0 && err == nil {
			return fmt.Errorf("человека с кодом %s нет", code)
		}
		if byAssistant && count("SELECT COUNT(*) FROM doc_rejected WHERE doc_id = ? AND kind = 'person' AND key = ? AND origin = 'owner'", docID, code) > 0 {
			return owner("владелец исключил " + code)
		}
		exec("DELETE FROM doc_rejected WHERE doc_id = ? AND kind = 'person' AND key = ?", docID, code)
		exec(`INSERT INTO doc_persons (doc_id, code, method, reason) VALUES (?, ?, ?, ?)
			ON CONFLICT(doc_id, code) DO UPDATE SET method = excluded.method, reason = excluded.reason
			WHERE ? = 'manual' OR doc_persons.method != 'manual'`, docID, code, mark, reasonOr(reason), mark)
	}
	for _, code := range ch.Unlink {
		if byAssistant && count("SELECT COUNT(*) FROM doc_persons WHERE doc_id = ? AND code = ? AND method = 'manual'", docID, code) > 0 {
			return owner("владелец привязал " + code)
		}
		exec("DELETE FROM doc_persons WHERE doc_id = ? AND code = ?", docID, code)
		exec(`INSERT INTO doc_rejected (doc_id, kind, key, value, created, origin) VALUES (?, 'person', ?, '', ?, ?)
			ON CONFLICT DO UPDATE SET origin = excluded.origin WHERE excluded.origin = 'owner'`, docID, code, ts, origin)
	}
	for _, a := range ch.Unset {
		q, args := "SELECT COUNT(*) FROM doc_attrs WHERE doc_id = ? AND key = ? AND source = 'manual'", []any{docID, a.Key}
		if a.Value != "" {
			q, args = q+" AND value = ?", append(args, a.Value)
		}
		if byAssistant && count(q, args...) > 0 {
			return owner("характеристику " + a.Key + " задал владелец")
		}
		if a.Value == "" {
			exec("DELETE FROM doc_attrs WHERE doc_id = ? AND key = ?", docID, a.Key)
		} else {
			exec("DELETE FROM doc_attrs WHERE doc_id = ? AND key = ? AND value = ?", docID, a.Key, a.Value)
		}
		exec(`INSERT INTO doc_rejected (doc_id, kind, key, value, created, origin) VALUES (?, 'attr', ?, ?, ?, ?)
			ON CONFLICT DO UPDATE SET origin = excluded.origin WHERE excluded.origin = 'owner'`, docID, a.Key, a.Value, ts, origin)
	}
	for _, a := range ch.Attrs {
		if byAssistant && count(`SELECT COUNT(*) FROM doc_rejected WHERE doc_id = ? AND kind = 'attr' AND key = ?
			AND value IN (?, '') AND origin = 'owner'`, docID, a.Key, a.Value) > 0 {
			return owner("владелец исключил " + a.String())
		}
		// A whole-key rejection stays: it keeps the batch's values of that key
		// out, while a refined value passes.
		exec("DELETE FROM doc_rejected WHERE doc_id = ? AND kind = 'attr' AND key = ? AND value = ?", docID, a.Key, a.Value)
		exec(`INSERT INTO doc_attrs (doc_id, key, value, source) VALUES (?, ?, ?, ?)
			ON CONFLICT(doc_id, key, value) DO UPDATE SET source = excluded.source
			WHERE excluded.source = 'manual' OR doc_attrs.source != 'manual'`, docID, a.Key, a.Value, mark)
	}
	if (ch.ClearDate || ch.Date != nil) && byAssistant &&
		count("SELECT COUNT(*) FROM doc_manual_dates WHERE doc_id = ? AND origin = 'owner'", docID) > 0 {
		return owner("дату задал владелец")
	}
	switch {
	case ch.ClearDate:
		exec("DELETE FROM doc_manual_dates WHERE doc_id = ?", docID)
	case ch.Date != nil:
		exec(`INSERT INTO doc_manual_dates (doc_id, date, created, origin) VALUES (?, ?, ?, ?)
			ON CONFLICT(doc_id) DO UPDATE SET date = excluded.date, created = excluded.created, origin = excluded.origin`,
			docID, *ch.Date, ts, origin)
	}
	js, _ := json.Marshal(ch)
	exec("INSERT INTO doc_changes (doc_id, origin, change, reason, at) VALUES (?, ?, ?, ?, ?)", docID, origin, string(js), reason, ts)
	if err != nil {
		return fmt.Errorf("change %s: %w", docID, err)
	}
	return tx.Commit()
}

func reasonOr(r string) string {
	if r == "" {
		return "вручную"
	}
	return r
}

// ConfirmAssistant makes an assistant's refinements of a document the
// owner's own. It returns how many facts it confirmed.
func (c *Catalog) ConfirmAssistant(ctx context.Context, docID string) (int64, error) {
	return c.assistantRows(ctx, docID, []string{
		"UPDATE doc_persons SET method = 'manual' WHERE doc_id = ? AND method = 'assistant'",
		"UPDATE doc_attrs SET source = 'manual' WHERE doc_id = ? AND source = 'assistant'",
		"UPDATE doc_rejected SET origin = 'owner' WHERE doc_id = ? AND origin = 'assistant'",
		"UPDATE doc_manual_dates SET origin = 'owner' WHERE doc_id = ? AND origin = 'assistant'",
	})
}

// RevertAssistant drops an assistant's refinements of a document; what was
// derived returns on the next index. It returns how many facts it dropped.
func (c *Catalog) RevertAssistant(ctx context.Context, docID string) (int64, error) {
	return c.assistantRows(ctx, docID, []string{
		"DELETE FROM doc_persons WHERE doc_id = ? AND method = 'assistant'",
		"DELETE FROM doc_attrs WHERE doc_id = ? AND source = 'assistant'",
		"DELETE FROM doc_rejected WHERE doc_id = ? AND origin = 'assistant'",
		"DELETE FROM doc_manual_dates WHERE doc_id = ? AND origin = 'assistant'",
	})
}

func (c *Catalog) assistantRows(ctx context.Context, docID string, qs []string) (int64, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int64
	for _, q := range qs {
		res, err := tx.ExecContext(ctx, q, docID)
		if err != nil {
			return 0, fmt.Errorf("assistant refinements of %s: %w", docID, err)
		}
		k, _ := res.RowsAffected()
		n += k
	}
	return n, tx.Commit()
}

// HasAssistantChanges says whether a document carries refinements an
// assistant made and the owner has not confirmed or reverted.
func (c *Catalog) HasAssistantChanges(ctx context.Context, docID string) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM doc_persons WHERE doc_id = ?1 AND method = 'assistant') +
		(SELECT COUNT(*) FROM doc_attrs WHERE doc_id = ?1 AND source = 'assistant') +
		(SELECT COUNT(*) FROM doc_rejected WHERE doc_id = ?1 AND origin = 'assistant') +
		(SELECT COUNT(*) FROM doc_manual_dates WHERE doc_id = ?1 AND origin = 'assistant')`, docID).Scan(&n)
	return n > 0, err
}

// DocChangeEntry is one logged refinement.
type DocChangeEntry struct {
	Origin, Change, Reason string
	At                     time.Time
}

// DocChanges returns a document's refinement log, oldest first.
func (c *Catalog) DocChanges(ctx context.Context, docID string) ([]DocChangeEntry, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT origin, change, reason, at FROM doc_changes WHERE doc_id = ? ORDER BY id", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DocChangeEntry
	for rows.Next() {
		var e DocChangeEntry
		var at string
		if err := rows.Scan(&e.Origin, &e.Change, &e.Reason, &at); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Rejected is what the owner or an assistant said a document is not: persons, and
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

// ManualDate returns a refined date of a document and who gave it; ok is
// false when there is none, and date is "" when it was said there is no date.
func (c *Catalog) ManualDate(ctx context.Context, docID string) (date, origin string, ok bool, err error) {
	err = c.db.QueryRowContext(ctx, "SELECT date, origin FROM doc_manual_dates WHERE doc_id = ?", docID).Scan(&date, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return date, origin, err == nil, err
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
