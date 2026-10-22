package catalog

import (
	"context"
	"fmt"
)

// TitleInput is a document waiting for its title.
type TitleInput struct {
	DocID   string
	Summary string
}

// PendingTitles lists documents with a summary and no title yet.
func (c *Catalog) PendingTitles(ctx context.Context, limit int) ([]TitleInput, error) {
	q := `SELECT doc_id, summary FROM texts
		WHERE status IN ('done', 'flagged') AND title IS NULL AND COALESCE(summary, '') != ''
		ORDER BY doc_id`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("pending titles: %w", err)
	}
	defer rows.Close()
	var out []TitleInput
	for rows.Next() {
		var t TitleInput
		if err := rows.Scan(&t.DocID, &t.Summary); err != nil {
			return nil, fmt.Errorf("pending titles: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveTitle stores a document's title, unless its summary changed meanwhile.
func (c *Catalog) SaveTitle(ctx context.Context, docID, summary, title string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE texts SET title = ? WHERE doc_id = ? AND summary = ?`, title, docID, summary)
	if err != nil {
		return fmt.Errorf("save title %s: %w", docID, err)
	}
	return nil
}
