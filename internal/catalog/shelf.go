package catalog

import (
	"context"
	"fmt"
)

// ShelfDoc is a document as the web tree needs it (ADR-0007).
type ShelfDoc struct {
	ID      string
	Date    string
	Partial string
	Pages   int
	Summary string
	Title   string // "" until made
	Persons []string
	Attrs   map[string][]string
	Given   map[string][]string // of Attrs, those not read by the model: shown in lists
}

// Shelf lists every document with text layers, newest first, with its
// persons and characteristics, in three queries whatever the account's size.
func (c *Catalog) Shelf(ctx context.Context) ([]ShelfDoc, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT d.id, COALESCE(t.doc_date, ''), COALESCE(t.doc_date_partial, ''), d.last_page - d.first_page + 1, COALESCE(t.summary, ''), COALESCE(t.title, '')
		FROM documents d JOIN texts t ON t.doc_id = d.id
		WHERE t.status IN ('done', 'flagged')
		ORDER BY COALESCE(t.doc_date, '') DESC, d.id`)
	if err != nil {
		return nil, fmt.Errorf("shelf: %w", err)
	}
	var out []ShelfDoc
	at := map[string]int{}
	for rows.Next() {
		var d ShelfDoc
		if err := rows.Scan(&d.ID, &d.Date, &d.Partial, &d.Pages, &d.Summary, &d.Title); err != nil {
			rows.Close()
			return nil, fmt.Errorf("shelf: %w", err)
		}
		d.Attrs = map[string][]string{}
		at[d.ID] = len(out)
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = c.db.QueryContext(ctx, `SELECT doc_id, code FROM doc_persons GROUP BY doc_id, code ORDER BY doc_id, code`)
	if err != nil {
		return nil, fmt.Errorf("shelf persons: %w", err)
	}
	for rows.Next() {
		var id, code string
		if err := rows.Scan(&id, &code); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := at[id]; ok {
			out[i].Persons = append(out[i].Persons, code)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = c.db.QueryContext(ctx, `SELECT doc_id, key, value, source FROM doc_attrs ORDER BY doc_id, key, value`)
	if err != nil {
		return nil, fmt.Errorf("shelf attrs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, k, v, src string
		if err := rows.Scan(&id, &k, &v, &src); err != nil {
			return nil, err
		}
		if i, ok := at[id]; ok {
			out[i].Attrs[k] = append(out[i].Attrs[k], v)
			if src != SourceModel {
				if out[i].Given == nil {
					out[i].Given = map[string][]string{}
				}
				out[i].Given[k] = append(out[i].Given[k], v)
			}
		}
	}
	return out, rows.Err()
}
