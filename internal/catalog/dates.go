package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Creation-date statuses.
const (
	DateDone     = "done"     // a date confirmed by its quote
	DateNone     = "none"     // the model found no creation date
	DateRejected = "rejected" // the quote is not in the text or holds another date
	DateError    = "error"
)

// DocDate is the creation date a model read from a document (SPEC 8).
type DocDate struct {
	DocID    string
	Status   string
	Date     string // YYYY-MM-DD, YYYY-MM, YYYY or --MM-DD
	Basis    string // stated: named as the document's date; event: the date of what it records
	Quote    string
	Reason   string
	Model    string
	CostUSD  float64
	Pipeline int
}

// DateInput is a document whose creation date is still to be read.
type DateInput struct {
	DocID     string
	Essential string
}

// PendingDates lists documents with text layers and no creation date read by
// the current pipeline (or a failed read). limit <= 0 means all.
func (c *Catalog) PendingDates(ctx context.Context, pipeline, limit int) ([]DateInput, error) {
	q := `SELECT t.doc_id, COALESCE(t.essential, '') FROM texts t
		LEFT JOIN doc_dates dd ON dd.doc_id = t.doc_id
		WHERE t.status IN ('done', 'flagged')
		  AND (dd.doc_id IS NULL OR dd.status = 'error' OR dd.pipeline < ?)
		ORDER BY t.doc_id`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := c.db.QueryContext(ctx, q, pipeline)
	if err != nil {
		return nil, fmt.Errorf("pending dates: %w", err)
	}
	defer rows.Close()
	var out []DateInput
	for rows.Next() {
		var d DateInput
		if err := rows.Scan(&d.DocID, &d.Essential); err != nil {
			return nil, fmt.Errorf("pending dates: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveDocDate stores a creation-date read, replacing the previous one.
func (c *Catalog) SaveDocDate(ctx context.Context, d DocDate, at time.Time) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO doc_dates (doc_id, status, date, basis, quote, reason, model, cost_usd, pipeline, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(doc_id) DO UPDATE SET status = excluded.status, date = excluded.date, basis = excluded.basis,
		  quote = excluded.quote, reason = excluded.reason, model = excluded.model,
		  cost_usd = doc_dates.cost_usd + excluded.cost_usd, pipeline = excluded.pipeline, updated = excluded.updated`,
		d.DocID, d.Status, d.Date, d.Basis, d.Quote, d.Reason, d.Model, d.CostUSD, d.Pipeline, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save date %s: %w", d.DocID, err)
	}
	return nil
}

// GetDocDate returns the creation-date read of a document; ok is false when
// there is none yet.
func (c *Catalog) GetDocDate(ctx context.Context, docID string) (DocDate, bool, error) {
	d := DocDate{DocID: docID}
	var date, basis, quote, reason sql.NullString
	err := c.db.QueryRowContext(ctx, `SELECT status, date, basis, quote, reason, model, cost_usd, pipeline
		FROM doc_dates WHERE doc_id = ?`, docID).
		Scan(&d.Status, &date, &basis, &quote, &reason, &d.Model, &d.CostUSD, &d.Pipeline)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, fmt.Errorf("get date %s: %w", docID, err)
	}
	d.Date, d.Basis, d.Quote, d.Reason = date.String, basis.String, quote.String, reason.String
	return d, true, nil
}
