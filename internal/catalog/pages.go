package catalog

import (
	"context"
	"fmt"
	"time"
)

// ResolveSHA expands a unique sha256 prefix of an original.
func (c *Catalog) ResolveSHA(ctx context.Context, prefix string) (string, error) {
	if len(prefix) < 6 {
		return "", fmt.Errorf("sha256 prefix %q is too short, give at least 6 characters", prefix)
	}
	rows, err := c.db.QueryContext(ctx, "SELECT sha256 FROM originals WHERE sha256 LIKE ? LIMIT 2", escapeLike(prefix)+"%")
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", prefix, err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", fmt.Errorf("resolve %s: %w", prefix, err)
		}
		found = append(found, s)
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no original starts with %s", prefix)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("prefix %s is ambiguous", prefix)
}

// Extraction statuses.
const (
	ExtractDone        = "done"
	ExtractError       = "error"
	ExtractUnsupported = "unsupported"
)

// Pending is an original that still needs its page text.
type Pending struct {
	SHA256 string
	MIME   string
	Size   int64
	URIs   []string // present sources, oldest first
}

// PendingExtraction lists originals without finished extraction; errors are
// retried, unsupported types are not. limit <= 0 means all.
func (c *Catalog) PendingExtraction(ctx context.Context, limit int) ([]Pending, error) {
	q := `SELECT o.sha256, o.mime, o.size, s.uri
		FROM originals o JOIN sources s ON s.sha256 = o.sha256 AND s.missing = 0
		LEFT JOIN extraction e ON e.sha256 = o.sha256
		WHERE e.status IS NULL OR e.status = 'error'
		ORDER BY o.first_seen, o.sha256, s.first_seen`
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("pending extraction: %w", err)
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var p Pending
		var uri string
		if err := rows.Scan(&p.SHA256, &p.MIME, &p.Size, &uri); err != nil {
			return nil, fmt.Errorf("pending extraction: %w", err)
		}
		if n := len(out); n > 0 && out[n-1].SHA256 == p.SHA256 {
			out[n-1].URIs = append(out[n-1].URIs, uri)
			continue
		}
		if limit > 0 && len(out) == limit {
			break
		}
		p.URIs = []string{uri}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Page is the verbatim text of one page and where it came from.
type Page struct {
	SHA256   string
	Page     int // 1-based
	Method   string
	Engine   string
	Text     string
	CostUSD  float64
	Pipeline int
}

// SavePage stores (or replaces) the text of one page.
func (c *Catalog) SavePage(ctx context.Context, p Page, at time.Time) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO pages (sha256, page, method, engine, text, cost_usd, pipeline, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(sha256, page) DO UPDATE SET method = excluded.method, engine = excluded.engine,
		  text = excluded.text, cost_usd = excluded.cost_usd, pipeline = excluded.pipeline,
		  created = excluded.created`,
		p.SHA256, p.Page, p.Method, p.Engine, p.Text, p.CostUSD, p.Pipeline, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save page %s/%d: %w", p.SHA256[:12], p.Page, err)
	}
	return nil
}

// DonePages returns the page numbers already stored for an original at the
// given pipeline version or later.
func (c *Catalog) DonePages(ctx context.Context, sha string, pipeline int) (map[int]bool, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT page FROM pages WHERE sha256 = ? AND pipeline >= ?", sha, pipeline)
	if err != nil {
		return nil, fmt.Errorf("done pages: %w", err)
	}
	defer rows.Close()
	done := map[int]bool{}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("done pages: %w", err)
		}
		done[n] = true
	}
	return done, rows.Err()
}

// Pages returns the stored pages of an original in order.
func (c *Catalog) Pages(ctx context.Context, sha string) ([]Page, error) {
	rows, err := c.db.QueryContext(ctx,
		"SELECT sha256, page, method, engine, text, cost_usd, pipeline FROM pages WHERE sha256 = ? ORDER BY page", sha)
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		var p Page
		if err := rows.Scan(&p.SHA256, &p.Page, &p.Method, &p.Engine, &p.Text, &p.CostUSD, &p.Pipeline); err != nil {
			return nil, fmt.Errorf("pages: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetExtraction records the outcome for an original.
func (c *Catalog) SetExtraction(ctx context.Context, sha string, pages int, status, errText string, at time.Time) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO extraction (sha256, pages, status, error, updated) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(sha256) DO UPDATE SET pages = excluded.pages, status = excluded.status,
		  error = excluded.error, updated = excluded.updated`,
		sha, pages, status, errText, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set extraction %s: %w", sha[:12], err)
	}
	return nil
}

// ExtractionStats summarises page text in the catalog.
type ExtractionStats struct {
	ByStatus map[string]int // originals per status; "pending" for none yet
	ByMethod map[string]int // pages per method
	CostUSD  float64
	Errors   []string // "sha12: error" for originals in error
}

// ExtractionSummary counts extraction state across the catalog.
func (c *Catalog) ExtractionSummary(ctx context.Context) (ExtractionStats, error) {
	st := ExtractionStats{ByStatus: map[string]int{}, ByMethod: map[string]int{}}
	rows, err := c.db.QueryContext(ctx, `
		SELECT COALESCE(e.status, 'pending'), COUNT(*) FROM originals o
		LEFT JOIN extraction e ON e.sha256 = o.sha256 GROUP BY 1`)
	if err != nil {
		return st, fmt.Errorf("extraction summary: %w", err)
	}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return st, fmt.Errorf("extraction summary: %w", err)
		}
		st.ByStatus[s] = n
	}
	rows.Close()
	rows, err = c.db.QueryContext(ctx, "SELECT method, COUNT(*), COALESCE(SUM(cost_usd), 0) FROM pages GROUP BY method")
	if err != nil {
		return st, fmt.Errorf("extraction summary: %w", err)
	}
	for rows.Next() {
		var m string
		var n int
		var cost float64
		if err := rows.Scan(&m, &n, &cost); err != nil {
			rows.Close()
			return st, fmt.Errorf("extraction summary: %w", err)
		}
		st.ByMethod[m] = n
		st.CostUSD += cost
	}
	rows.Close()
	rows, err = c.db.QueryContext(ctx, "SELECT sha256, COALESCE(error, '') FROM extraction WHERE status = 'error' ORDER BY sha256")
	if err != nil {
		return st, fmt.Errorf("extraction summary: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sha, e string
		if err := rows.Scan(&sha, &e); err != nil {
			return st, fmt.Errorf("extraction summary: %w", err)
		}
		st.Errors = append(st.Errors, sha[:12]+": "+e)
	}
	return st, rows.Err()
}
