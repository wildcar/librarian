package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Split statuses.
const (
	SplitDone   = "done"
	SplitReview = "review" // the model and a strong rule disagree: the owner decides
	SplitError  = "error"
)

// Document is a page range of one original.
type Document struct {
	ID        string
	SHA256    string
	FirstPage int
	LastPage  int
}

// DocumentID is stable for the same original and first page.
func DocumentID(sha string, first int) string {
	return fmt.Sprintf("%s-%d", sha[:16], first)
}

// SplitResult is the outcome of splitting one original.
type SplitResult struct {
	SHA256        string
	Status        string
	Method        string
	Engine        string
	CostUSD       float64
	Disagreements string
	Error         string
	Pipeline      int
	Starts        []int // first pages of documents, ascending; empty on error
	Pages         int
}

// PendingSplit lists originals whose page text is complete and that have no
// split yet (or a failed one). limit <= 0 means all.
func (c *Catalog) PendingSplit(ctx context.Context, limit int) ([]string, error) {
	q := `SELECT o.sha256 FROM originals o
		JOIN extraction e ON e.sha256 = o.sha256 AND e.status = 'done'
		LEFT JOIN split s ON s.sha256 = o.sha256
		WHERE s.status IS NULL OR s.status = 'error'
		ORDER BY o.first_seen, o.sha256`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("pending split: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("pending split: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SaveSplit replaces an original's documents and records the split state in
// one transaction, so documents never belong to a stale split.
func (c *Catalog) SaveSplit(ctx context.Context, r SplitResult, at time.Time) error {
	ts := at.UTC().Format(time.RFC3339Nano)
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save split %s: %w", r.SHA256[:12], err)
	}
	defer tx.Rollback()
	if r.Status != SplitError {
		if _, err := tx.ExecContext(ctx, "DELETE FROM documents WHERE sha256 = ?", r.SHA256); err != nil {
			return fmt.Errorf("save split %s: %w", r.SHA256[:12], err)
		}
		for i, first := range r.Starts {
			last := r.Pages
			if i+1 < len(r.Starts) {
				last = r.Starts[i+1] - 1
			}
			_, err := tx.ExecContext(ctx,
				"INSERT INTO documents (id, sha256, first_page, last_page, created) VALUES (?, ?, ?, ?, ?)",
				DocumentID(r.SHA256, first), r.SHA256, first, last, ts)
			if err != nil {
				return fmt.Errorf("save split %s: %w", r.SHA256[:12], err)
			}
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO split (sha256, status, method, engine, cost_usd, disagreements, error, pipeline, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sha256) DO UPDATE SET status = excluded.status, method = excluded.method,
		  engine = excluded.engine, cost_usd = split.cost_usd + excluded.cost_usd,
		  disagreements = excluded.disagreements, error = excluded.error,
		  pipeline = excluded.pipeline, updated = excluded.updated, confirmed = NULL`,
		r.SHA256, r.Status, r.Method, r.Engine, r.CostUSD, r.Disagreements, r.Error, r.Pipeline, ts)
	if err != nil {
		return fmt.Errorf("save split %s: %w", r.SHA256[:12], err)
	}
	return tx.Commit()
}

// ConfirmSplit records that the owner checked the disputed boundaries of
// the original a document was cut from: they hold for all its documents.
func (c *Catalog) ConfirmSplit(ctx context.Context, docID string, at time.Time) error {
	_, err := c.db.ExecContext(ctx, `UPDATE split SET confirmed = ?
		WHERE status = 'review' AND sha256 = (SELECT sha256 FROM documents WHERE id = ?)`,
		at.UTC().Format(time.RFC3339Nano), docID)
	if err != nil {
		return fmt.Errorf("confirm split of %s: %w", docID, err)
	}
	return nil
}

// DocumentRow is a document with its source and first line, for listings.
type DocumentRow struct {
	Document
	Source    string // a present source URI
	FirstLine string
	Status    string // split status of the original
}

// Documents lists documents, optionally of one original, in source order.
func (c *Catalog) Documents(ctx context.Context, sha string) ([]DocumentRow, error) {
	q := `SELECT d.id, d.sha256, d.first_page, d.last_page, sp.status,
		  COALESCE((SELECT uri FROM sources s WHERE s.sha256 = d.sha256 AND s.missing = 0 ORDER BY s.first_seen LIMIT 1), ''),
		  COALESCE((SELECT text FROM pages p WHERE p.sha256 = d.sha256 AND p.page = d.first_page), '')
		FROM documents d JOIN split sp ON sp.sha256 = d.sha256`
	args := []any{}
	if sha != "" {
		q += " WHERE d.sha256 = ?"
		args = append(args, sha)
	}
	q += " ORDER BY 6, d.first_page"
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("documents: %w", err)
	}
	defer rows.Close()
	var out []DocumentRow
	for rows.Next() {
		var d DocumentRow
		var text string
		if err := rows.Scan(&d.ID, &d.SHA256, &d.FirstPage, &d.LastPage, &d.Status, &d.Source, &text); err != nil {
			return nil, fmt.Errorf("documents: %w", err)
		}
		d.FirstLine = firstLine(text)
		out = append(out, d)
	}
	return out, rows.Err()
}

// firstLine is the first line with at least three letters: a bare date or a
// barcode on top of a page says nothing about the document.
func firstLine(t string) string {
	for _, l := range strings.Split(t, "\n") {
		l = strings.TrimSpace(l)
		letters := 0
		for _, ch := range l {
			if unicode.IsLetter(ch) {
				letters++
			}
		}
		if r := []rune(l); letters >= 3 {
			if len(r) > 90 {
				return string(r[:90]) + "…"
			}
			return l
		}
	}
	return ""
}

// SplitSummary counts originals per split status and lists those to review.
func (c *Catalog) SplitSummary(ctx context.Context) (map[string]int, int, []string, error) {
	byStatus := map[string]int{}
	rows, err := c.db.QueryContext(ctx, `SELECT CASE WHEN confirmed IS NULL THEN status ELSE 'done' END s, COUNT(*)
		FROM split GROUP BY s`)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("split summary: %w", err)
	}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return nil, 0, nil, fmt.Errorf("split summary: %w", err)
		}
		byStatus[s] = n
	}
	rows.Close()
	var docs int
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents").Scan(&docs); err != nil {
		return nil, 0, nil, fmt.Errorf("split summary: %w", err)
	}
	rows, err = c.db.QueryContext(ctx,
		"SELECT sha256, COALESCE(disagreements, ''), COALESCE(error, '') FROM split WHERE status IN ('review', 'error') AND confirmed IS NULL ORDER BY sha256")
	if err != nil {
		return nil, 0, nil, fmt.Errorf("split summary: %w", err)
	}
	defer rows.Close()
	var notes []string
	for rows.Next() {
		var sha, dis, e string
		if err := rows.Scan(&sha, &dis, &e); err != nil {
			return nil, 0, nil, fmt.Errorf("split summary: %w", err)
		}
		notes = append(notes, sha[:12]+": "+dis+e)
	}
	return byStatus, docs, notes, rows.Err()
}
