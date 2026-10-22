package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// The describe stage gives each document a title and the characteristics the
// model reads from its summary (kind, specialty, organisation); index turns
// those into doc_attrs with source "model", below everything else.

// SourceModel marks a characteristic read by the model.
const SourceModel = "model"

// DescribeInput is a document waiting for its description.
type DescribeInput struct {
	DocID     string
	Summary   string
	Essential string // has names: read for the author's specialty, never stored
}

// PendingDescriptions lists documents with a summary and no title yet.
func (c *Catalog) PendingDescriptions(ctx context.Context, limit int) ([]DescribeInput, error) {
	q := `SELECT doc_id, summary, COALESCE(essential, '') FROM texts
		WHERE status IN ('done', 'flagged') AND title IS NULL AND COALESCE(summary, '') != ''
		ORDER BY doc_id`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("pending descriptions: %w", err)
	}
	defer rows.Close()
	var out []DescribeInput
	for rows.Next() {
		var t DescribeInput
		if err := rows.Scan(&t.DocID, &t.Summary, &t.Essential); err != nil {
			return nil, fmt.Errorf("pending descriptions: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveDescription stores a document's title and model characteristics,
// unless its summary changed meanwhile.
func (c *Catalog) SaveDescription(ctx context.Context, docID, summary, title string, attrs map[string][]string) error {
	js, err := json.Marshal(attrs)
	if err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx, `UPDATE texts SET title = ?, model_attrs = ? WHERE doc_id = ? AND summary = ?`,
		title, string(js), docID, summary)
	if err != nil {
		return fmt.Errorf("save description %s: %w", docID, err)
	}
	return nil
}

// ModelAttrValues lists the values the model has given so far, by key, the
// most used first, so the next documents reuse them.
func (c *Catalog) ModelAttrValues(ctx context.Context) (map[string][]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT model_attrs FROM texts WHERE model_attrs IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("model values: %w", err)
	}
	defer rows.Close()
	counts := map[string]map[string]int{}
	for rows.Next() {
		var js string
		if err := rows.Scan(&js); err != nil {
			return nil, fmt.Errorf("model values: %w", err)
		}
		var m map[string][]string
		if json.Unmarshal([]byte(js), &m) != nil {
			continue
		}
		for k, vs := range m {
			if counts[k] == nil {
				counts[k] = map[string]int{}
			}
			for _, v := range vs {
				counts[k][v]++
			}
		}
	}
	out := map[string][]string{}
	for k, vs := range counts {
		for v := range vs {
			out[k] = append(out[k], v)
		}
		sort.Slice(out[k], func(i, j int) bool {
			a, b := out[k][i], out[k][j]
			if vs[a] != vs[b] {
				return vs[a] > vs[b]
			}
			return a < b
		})
	}
	return out, rows.Err()
}
