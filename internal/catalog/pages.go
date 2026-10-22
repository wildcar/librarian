package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"
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
		  created = excluded.created, confirmed = NULL`,
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

// PageCheck is the second reading of one page and how it compares.
type PageCheck struct {
	Engine  string
	Text    string
	Words   int
	Matched int
	Missing []string
	Run     int // longest run of consecutive missing confident words
	Flagged bool
}

// SaveCheck stores the second reading of a page.
func (c *Catalog) SaveCheck(ctx context.Context, sha string, page int, k PageCheck) error {
	_, err := c.db.ExecContext(ctx, `
		UPDATE pages SET check_engine = ?, check_text = ?, check_words = ?, check_matched = ?,
		  check_missing = ?, check_run = ?, flagged = ?, confirmed = NULL WHERE sha256 = ? AND page = ?`,
		k.Engine, k.Text, k.Words, k.Matched, strings.Join(k.Missing, " "), k.Run, boolInt(k.Flagged), sha, page)
	if err != nil {
		return fmt.Errorf("save check %s/%d: %w", sha[:12], page, err)
	}
	return nil
}

// ConfirmPage records that the owner checked a doubted page of a document
// against the original; page counts from the document's first page.
func (c *Catalog) ConfirmPage(ctx context.Context, docID string, page int, at time.Time) error {
	_, err := c.db.ExecContext(ctx, `UPDATE pages SET confirmed = ?
		WHERE flagged = 1 AND (sha256, page) = (SELECT sha256, first_page + ? - 1 FROM documents
		  WHERE id = ? AND ? >= 1 AND first_page + ? - 1 <= last_page)`,
		at.UTC().Format(time.RFC3339Nano), page, docID, page, page)
	if err != nil {
		return fmt.Errorf("confirm page %d of %s: %w", page, docID, err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Unchecked lists OCR pages without a second reading, grouped by original,
// with the present sources of each original.
func (c *Catalog) Unchecked(ctx context.Context, limit int) ([]Pending, map[string][]int, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT p.sha256, p.page FROM pages p
		WHERE p.method = 'ocr' AND p.check_engine IS NULL ORDER BY p.sha256, p.page`)
	if err != nil {
		return nil, nil, fmt.Errorf("unchecked: %w", err)
	}
	byOrig := map[string][]int{}
	var order []string
	for rows.Next() {
		var sha string
		var n int
		if err := rows.Scan(&sha, &n); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("unchecked: %w", err)
		}
		if _, ok := byOrig[sha]; !ok {
			if limit > 0 && len(order) == limit {
				continue
			}
			order = append(order, sha)
		}
		byOrig[sha] = append(byOrig[sha], n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var out []Pending
	for _, sha := range order {
		p := Pending{SHA256: sha}
		err := c.db.QueryRowContext(ctx, "SELECT mime, size FROM originals WHERE sha256 = ?", sha).Scan(&p.MIME, &p.Size)
		if err != nil {
			return nil, nil, fmt.Errorf("unchecked %s: %w", sha[:12], err)
		}
		srows, err := c.db.QueryContext(ctx,
			"SELECT uri FROM sources WHERE sha256 = ? AND missing = 0 ORDER BY first_seen", sha)
		if err != nil {
			return nil, nil, fmt.Errorf("unchecked %s: %w", sha[:12], err)
		}
		for srows.Next() {
			var u string
			if err := srows.Scan(&u); err != nil {
				srows.Close()
				return nil, nil, err
			}
			p.URIs = append(p.URIs, u)
		}
		srows.Close()
		out = append(out, p)
	}
	return out, byOrig, nil
}

// Flagged is a page whose two readings disagree.
type Flagged struct {
	SHA256  string
	Page    int
	Words   int
	Matched int
	Run     int
	Missing string
}

// FlaggedPages lists pages flagged by the second reading.
func (c *Catalog) FlaggedPages(ctx context.Context) ([]Flagged, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT sha256, page, check_words, check_matched, COALESCE(check_run, 0), COALESCE(check_missing, '')
		FROM pages WHERE flagged = 1 AND confirmed IS NULL ORDER BY sha256, page`)
	if err != nil {
		return nil, fmt.Errorf("flagged pages: %w", err)
	}
	defer rows.Close()
	var out []Flagged
	for rows.Next() {
		var f Flagged
		if err := rows.Scan(&f.SHA256, &f.Page, &f.Words, &f.Matched, &f.Run, &f.Missing); err != nil {
			return nil, fmt.Errorf("flagged pages: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CheckShares returns, for every checked OCR page, its confident-word count
// and matched count: the data the flag threshold is calibrated on.
func (c *Catalog) CheckShares(ctx context.Context) ([][4]any, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT sha256, page, check_words, check_matched FROM pages
		WHERE check_engine IS NOT NULL ORDER BY sha256, page`)
	if err != nil {
		return nil, fmt.Errorf("check shares: %w", err)
	}
	defer rows.Close()
	var out [][4]any
	for rows.Next() {
		var sha string
		var page, words, matched int
		if err := rows.Scan(&sha, &page, &words, &matched); err != nil {
			return nil, fmt.Errorf("check shares: %w", err)
		}
		out = append(out, [4]any{sha, page, words, matched})
	}
	return out, rows.Err()
}

// SourceFile is one file an original was found as.
type SourceFile struct {
	Name     string // what the owner calls it: the uploaded name or the file's own
	Path     string // where it lies on the server, for a folder file
	File     string // the file on the server, folder file or upload
	Uploaded bool
	Missing  bool // gone from its folder since
}

// sourceFile names a source: an upload is kept under its hash, so its name
// is the one it was sent with (unknown for uploads made before names were
// kept); a folder file is its path.
func sourceFile(sha, uri string, name sql.NullString, missing bool) SourceFile {
	p := strings.TrimPrefix(uri, "file://")
	base := path.Base(p)
	if strings.TrimSuffix(base, path.Ext(base)) == sha {
		return SourceFile{Name: name.String, File: p, Uploaded: true, Missing: missing}
	}
	return SourceFile{Name: base, Path: p, File: p, Missing: missing}
}

// OriginalMIME is the content type an original was recognised as.
func (c *Catalog) OriginalMIME(ctx context.Context, sha string) (string, error) {
	var m string
	if err := c.db.QueryRowContext(ctx, "SELECT mime FROM originals WHERE sha256 = ?", sha).Scan(&m); err != nil {
		return "", fmt.Errorf("original %s: %w", sha, err)
	}
	return m, nil
}

// SourceFiles lists the files an original was found as, oldest first.
func (c *Catalog) SourceFiles(ctx context.Context, sha string) ([]SourceFile, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT uri, name, missing FROM sources WHERE sha256 = ? ORDER BY first_seen, uri`, sha)
	if err != nil {
		return nil, fmt.Errorf("source files: %w", err)
	}
	defer rows.Close()
	var out []SourceFile
	for rows.Next() {
		var uri string
		var name sql.NullString
		var missing bool
		if err := rows.Scan(&uri, &name, &missing); err != nil {
			return nil, fmt.Errorf("source files: %w", err)
		}
		out = append(out, sourceFile(sha, uri, name, missing))
	}
	return out, rows.Err()
}
