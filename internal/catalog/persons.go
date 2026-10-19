package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Person is someone whose documents the account keeps (ADR-0004).
type Person struct {
	Code      string
	Label     string // shown to the assistant instead of the name
	FullName  string
	Aliases   []string
	BirthDate string // YYYY-MM-DD or ""
	Documents int    // linked documents, filled by Persons
}

// ErrPersonExists is returned when the code is taken.
var ErrPersonExists = errors.New("person already exists")

// AddPerson registers a person.
func (c *Catalog) AddPerson(ctx context.Context, p Person, at time.Time) error {
	if strings.TrimSpace(p.Code) == "" || strings.TrimSpace(p.FullName) == "" {
		return errors.New("a person needs a code and a full name")
	}
	if p.Label == "" {
		p.Label = p.Code
	}
	al, _ := json.Marshal(p.Aliases)
	var bd any
	if p.BirthDate != "" {
		if _, err := time.Parse("2006-01-02", p.BirthDate); err != nil {
			return fmt.Errorf("birth date %q: want YYYY-MM-DD", p.BirthDate)
		}
		bd = p.BirthDate
	}
	_, err := c.db.ExecContext(ctx,
		"INSERT INTO persons (code, label, full_name, aliases, birth_date, created) VALUES (?, ?, ?, ?, ?, ?)",
		p.Code, p.Label, p.FullName, string(al), bd, at.UTC().Format(time.RFC3339Nano))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("%w: %s", ErrPersonExists, p.Code)
	}
	if err != nil {
		return fmt.Errorf("add person %s: %w", p.Code, err)
	}
	return nil
}

// Persons lists the account's persons with their document counts.
func (c *Catalog) Persons(ctx context.Context) ([]Person, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT p.code, p.label, p.full_name, p.aliases, COALESCE(p.birth_date, ''),
		  (SELECT COUNT(*) FROM doc_persons dp WHERE dp.code = p.code)
		FROM persons p ORDER BY p.code`)
	if err != nil {
		return nil, fmt.Errorf("persons: %w", err)
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		var al string
		if err := rows.Scan(&p.Code, &p.Label, &p.FullName, &al, &p.BirthDate, &p.Documents); err != nil {
			return nil, fmt.Errorf("persons: %w", err)
		}
		_ = json.Unmarshal([]byte(al), &p.Aliases)
		out = append(out, p)
	}
	return out, rows.Err()
}

// LinkPerson attaches a person to a document. A manual link is never
// replaced by a rule link.
func (c *Catalog) LinkPerson(ctx context.Context, docID, code, method, reason string) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO doc_persons (doc_id, code, method, reason) VALUES (?, ?, ?, ?)
		ON CONFLICT(doc_id, code) DO UPDATE SET method = excluded.method, reason = excluded.reason
		WHERE doc_persons.method != 'manual'`, docID, code, method, reason)
	if err != nil {
		return fmt.Errorf("link %s to %s: %w", code, docID, err)
	}
	return nil
}

// ClearRuleLinks removes the rule links of a document before they are redone.
func (c *Catalog) ClearRuleLinks(ctx context.Context, docID string) error {
	_, err := c.db.ExecContext(ctx, "DELETE FROM doc_persons WHERE doc_id = ? AND method = 'rule'", docID)
	return err
}

// DocPersons returns the codes linked to a document.
func (c *Catalog) DocPersons(ctx context.Context, docID string) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT code FROM doc_persons WHERE doc_id = ? ORDER BY code", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// IndexEntry is what the index step needs about one document.
type IndexEntry struct {
	DocID     string
	SHA256    string
	FirstPage int
	LastPage  int
	Summary   string
	Essential string
}

// IndexEntries lists every document that has text layers.
func (c *Catalog) IndexEntries(ctx context.Context) ([]IndexEntry, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT d.id, d.sha256, d.first_page, d.last_page, COALESCE(t.summary, ''), COALESCE(t.essential, '')
		FROM documents d JOIN texts t ON t.doc_id = d.id WHERE t.status IN ('done', 'flagged')
		ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("index entries: %w", err)
	}
	defer rows.Close()
	var out []IndexEntry
	for rows.Next() {
		var e IndexEntry
		if err := rows.Scan(&e.DocID, &e.SHA256, &e.FirstPage, &e.LastPage, &e.Summary, &e.Essential); err != nil {
			return nil, fmt.Errorf("index entries: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetDocDate stores the document date and where it came from ("" clears it).
func (c *Catalog) SetDocDate(ctx context.Context, docID, date, source string) error {
	var d, s any
	if date != "" {
		d, s = date, source
	}
	_, err := c.db.ExecContext(ctx, "UPDATE texts SET doc_date = ?, doc_date_source = ? WHERE doc_id = ?", d, s, docID)
	return err
}

// IndexText puts a document's searchable text into the full-text index.
func (c *Catalog) IndexText(ctx context.Context, docID, summary, essential string) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM docs_fts WHERE doc_id = ?", docID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO docs_fts (doc_id, summary, essential) VALUES (?, ?, ?)", docID, summary, essential); err != nil {
		return err
	}
	return tx.Commit()
}

// SearchQuery filters documents. Every field is optional.
type SearchQuery struct {
	Text     string // words; each matches as a prefix, all must occur
	Person   string // person code
	DateFrom string // YYYY-MM-DD, inclusive
	DateTo   string
	Limit    int
}

// Hit is one search result: metadata and the de-identified summary only.
type Hit struct {
	DocID   string
	Date    string
	Persons []string
	Pages   int
	Summary string
	Flagged bool
}

// Search finds documents. It never returns source paths or the essential text.
func (c *Catalog) Search(ctx context.Context, q SearchQuery) ([]Hit, error) {
	where := []string{"t.status IN ('done', 'flagged')"}
	args := []any{}
	from := "documents d JOIN texts t ON t.doc_id = d.id"
	order := "COALESCE(t.doc_date, '') DESC, d.id"
	if m := ftsQuery(q.Text); m != "" {
		// With words, the best match comes first (bm25, summary weighted over
		// the essential text); without them, the newest document.
		from += " JOIN docs_fts ON docs_fts.doc_id = d.id"
		where = append(where, "docs_fts MATCH ?")
		args = append(args, m)
		order = "bm25(docs_fts, 0.0, 2.0, 1.0), " + order
	}
	if q.Person != "" {
		where = append(where, "d.id IN (SELECT doc_id FROM doc_persons WHERE code = ?)")
		args = append(args, q.Person)
	}
	if q.DateFrom != "" {
		where = append(where, "t.doc_date >= ?")
		args = append(args, q.DateFrom)
	}
	if q.DateTo != "" {
		where = append(where, "t.doc_date <= ?")
		args = append(args, q.DateTo)
	}
	limit := q.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT d.id, COALESCE(t.doc_date, ''), d.last_page - d.first_page + 1, COALESCE(t.summary, ''), t.status = 'flagged'
		FROM `+from+`
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY `+order+` LIMIT `+fmt.Sprint(limit), args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.DocID, &h.Date, &h.Pages, &h.Summary, &h.Flagged); err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Persons, err = c.DocPersons(ctx, out[i].DocID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ftsQuery turns free words into an FTS5 query: every word must occur, as a
// prefix of a shortened stem, so Russian endings do not stop a match
// ("кардиологу" finds "кардиолог"). Other characters are dropped, so user
// input can never form FTS syntax.
func ftsQuery(text string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		r := []rune(w)
		if len(r) > 5 {
			r = r[:max(4, len(r)-2)]
		}
		if len(r) >= 2 {
			terms = append(terms, `"`+string(r)+`"*`)
		}
	}
	return strings.Join(terms, " AND ")
}

// DocDate returns a document's date and its source.
func (c *Catalog) DocDate(ctx context.Context, docID string) (string, string, error) {
	var d, s sql.NullString
	err := c.db.QueryRowContext(ctx, "SELECT doc_date, doc_date_source FROM texts WHERE doc_id = ?", docID).Scan(&d, &s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return d.String, s.String, err
}

// Unlinked counts documents with text layers but no person.
func (c *Catalog) Unlinked(ctx context.Context) (int, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM texts t WHERE t.status IN ('done', 'flagged')
		AND NOT EXISTS (SELECT 1 FROM doc_persons dp WHERE dp.doc_id = t.doc_id)`).Scan(&n)
	return n, err
}
