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

// SourceModel marks a characteristic read by the model; SourceModelGuess one
// the model guessed from the content because the document does not say it.
const (
	SourceModel      = "model"
	SourceModelGuess = "model:guess"
)

// ByModel says whether a characteristic's source is the model.
func ByModel(source string) bool { return source == SourceModel || source == SourceModelGuess }

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

// Description is what the model says about a document besides its summary.
type Description struct {
	Title   string
	Attrs   map[string][]string
	Guessed map[string]bool // keys whose value the model guessed from the content
}

// SaveDescription stores a document's title and model characteristics,
// unless its summary changed meanwhile.
func (c *Catalog) SaveDescription(ctx context.Context, docID, summary string, d Description) error {
	attrs, err := json.Marshal(d.Attrs)
	if err != nil {
		return err
	}
	var guessed []string
	for k, g := range d.Guessed {
		if g {
			guessed = append(guessed, k)
		}
	}
	sort.Strings(guessed)
	gs, err := json.Marshal(guessed)
	if err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx, `UPDATE texts SET title = ?, model_attrs = ?, model_guessed = ? WHERE doc_id = ? AND summary = ?`,
		d.Title, string(attrs), string(gs), docID, summary)
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

// DocGuesses lists a document's characteristics the model guessed, as
// "key=value".
func (c *Catalog) DocGuesses(ctx context.Context, docID string) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT key, value FROM doc_attrs WHERE doc_id = ? AND source = ? ORDER BY key, value`,
		docID, SourceModelGuess)
	if err != nil {
		return nil, fmt.Errorf("guesses %s: %w", docID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a Attr
		if err := rows.Scan(&a.Key, &a.Value); err != nil {
			return nil, err
		}
		out = append(out, a.String())
	}
	return out, rows.Err()
}
