package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Text layer statuses.
const (
	TextsDone    = "done"
	TextsFlagged = "flagged" // a summary check still fails after the retry: the owner looks
	TextsError   = "error"
)

// Texts are the derived text layers of one document (ADR-0005).
type Texts struct {
	DocID              string
	Status             string
	Essential          string
	Summary            string
	Removed            string // JSON
	RestoredRepeats    int
	RestoredSuspicious int
	Flags              string
	Error              string
	Model              string
	CostUSD            float64
	Pipeline           int
	CharsBefore        int // not stored: for run reports
}

// PendingTexts lists documents without text layers (or with a failed run).
func (c *Catalog) PendingTexts(ctx context.Context, limit int) ([]Document, error) {
	q := `SELECT d.id, d.sha256, d.first_page, d.last_page FROM documents d
		LEFT JOIN texts t ON t.doc_id = d.id
		WHERE t.status IS NULL OR t.status = 'error'
		ORDER BY d.sha256, d.first_page`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("pending texts: %w", err)
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.SHA256, &d.FirstPage, &d.LastPage); err != nil {
			return nil, fmt.Errorf("pending texts: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveTexts stores (or replaces) a document's text layers. Costs add up over
// retries.
func (c *Catalog) SaveTexts(ctx context.Context, t Texts, at time.Time) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO texts (doc_id, status, essential, summary, removed, restored_repeats, restored_suspicious,
		  flags, error, model, cost_usd, pipeline, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(doc_id) DO UPDATE SET status = excluded.status, essential = excluded.essential,
		  summary = excluded.summary, removed = excluded.removed, restored_repeats = excluded.restored_repeats,
		  restored_suspicious = excluded.restored_suspicious, flags = excluded.flags, error = excluded.error,
		  model = excluded.model, cost_usd = texts.cost_usd + excluded.cost_usd,
		  pipeline = excluded.pipeline, updated = excluded.updated`,
		t.DocID, t.Status, t.Essential, t.Summary, t.Removed, t.RestoredRepeats, t.RestoredSuspicious,
		t.Flags, t.Error, t.Model, t.CostUSD, t.Pipeline, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save texts %s: %w", t.DocID, err)
	}
	return nil
}

// ErrNoDocument is returned for an unknown document id.
var ErrNoDocument = errors.New("no such document")

// GetDocument returns a document and its text layers (zero Texts if none yet).
func (c *Catalog) GetDocument(ctx context.Context, id string) (Document, Texts, error) {
	var d Document
	err := c.db.QueryRowContext(ctx, "SELECT id, sha256, first_page, last_page FROM documents WHERE id = ?", id).
		Scan(&d.ID, &d.SHA256, &d.FirstPage, &d.LastPage)
	if errors.Is(err, sql.ErrNoRows) {
		return d, Texts{}, fmt.Errorf("%w: %s", ErrNoDocument, id)
	}
	if err != nil {
		return d, Texts{}, fmt.Errorf("get document %s: %w", id, err)
	}
	var t Texts
	var ess, sum, rem, flags, e sql.NullString
	err = c.db.QueryRowContext(ctx, `SELECT doc_id, status, essential, summary, removed, restored_repeats,
		restored_suspicious, flags, error, model, cost_usd, pipeline FROM texts WHERE doc_id = ?`, id).
		Scan(&t.DocID, &t.Status, &ess, &sum, &rem, &t.RestoredRepeats, &t.RestoredSuspicious, &flags, &e, &t.Model, &t.CostUSD, &t.Pipeline)
	if errors.Is(err, sql.ErrNoRows) {
		return d, Texts{}, nil
	}
	if err != nil {
		return d, t, fmt.Errorf("get document %s: %w", id, err)
	}
	t.Essential, t.Summary, t.Removed, t.Flags, t.Error = ess.String, sum.String, rem.String, flags.String, e.String
	return d, t, nil
}

// TextsSummary counts documents per text-layer status and lists flagged ones.
func (c *Catalog) TextsSummary(ctx context.Context) (map[string]int, float64, []string, error) {
	by := map[string]int{}
	var cost float64
	rows, err := c.db.QueryContext(ctx, `
		SELECT COALESCE(t.status, 'pending'), COUNT(*), COALESCE(SUM(t.cost_usd), 0)
		FROM documents d LEFT JOIN texts t ON t.doc_id = d.id GROUP BY 1`)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("texts summary: %w", err)
	}
	for rows.Next() {
		var s string
		var n int
		var cst float64
		if err := rows.Scan(&s, &n, &cst); err != nil {
			rows.Close()
			return nil, 0, nil, fmt.Errorf("texts summary: %w", err)
		}
		by[s] = n
		cost += cst
	}
	rows.Close()
	rows, err = c.db.QueryContext(ctx,
		"SELECT doc_id, COALESCE(flags, ''), COALESCE(error, '') FROM texts WHERE status IN ('flagged', 'error') ORDER BY doc_id")
	if err != nil {
		return nil, 0, nil, fmt.Errorf("texts summary: %w", err)
	}
	defer rows.Close()
	var notes []string
	for rows.Next() {
		var id, f, e string
		if err := rows.Scan(&id, &f, &e); err != nil {
			return nil, 0, nil, fmt.Errorf("texts summary: %w", err)
		}
		notes = append(notes, id+": "+f+e)
	}
	return by, cost, notes, rows.Err()
}
