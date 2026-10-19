// Package catalog is an account's SQLite catalog of originals and the places
// they were seen.
//
// One catalog belongs to exactly one account (ADR-0004). Originals of folder
// and link sources are not copied (ADR-0005): the catalog keeps their content
// hash and every source URI, so an original can be fetched and checked later.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// schema is applied step by step; PRAGMA user_version records the last step.
var schema = []string{
	`CREATE TABLE originals (
		sha256     TEXT PRIMARY KEY,
		size       INTEGER NOT NULL,
		mime       TEXT NOT NULL,
		first_seen TEXT NOT NULL
	);
	CREATE TABLE sources (
		uri        TEXT PRIMARY KEY,
		sha256     TEXT NOT NULL REFERENCES originals(sha256),
		size       INTEGER NOT NULL,
		mtime      TEXT NOT NULL,
		ext_mime   TEXT NOT NULL,
		first_seen TEXT NOT NULL,
		last_seen  TEXT NOT NULL,
		missing    INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX sources_sha256 ON sources(sha256);
	CREATE TABLE intake_runs (
		id        INTEGER PRIMARY KEY,
		root      TEXT NOT NULL,
		started   TEXT NOT NULL,
		finished  TEXT,
		files     INTEGER NOT NULL DEFAULT 0,
		hashed    INTEGER NOT NULL DEFAULT 0,
		new_files INTEGER NOT NULL DEFAULT 0,
		errors    INTEGER NOT NULL DEFAULT 0
	);`,
	// 2: verbatim text per page (ADR-0002, ADR-0005) and extraction state.
	`CREATE TABLE pages (
		sha256   TEXT NOT NULL REFERENCES originals(sha256),
		page     INTEGER NOT NULL,
		method   TEXT NOT NULL,            -- text-layer | ocr
		engine   TEXT NOT NULL,            -- pdftotext, or the model that answered
		text     TEXT NOT NULL,
		cost_usd REAL NOT NULL DEFAULT 0,
		pipeline INTEGER NOT NULL,
		created  TEXT NOT NULL,
		PRIMARY KEY (sha256, page)
	);
	CREATE TABLE extraction (
		sha256  TEXT PRIMARY KEY REFERENCES originals(sha256),
		pages   INTEGER,
		status  TEXT NOT NULL,             -- done | error | unsupported
		error   TEXT,
		updated TEXT NOT NULL
	);`,
	// 3: the second, independent reading of OCR pages (ADR-0002).
	`ALTER TABLE pages ADD COLUMN check_engine  TEXT;     -- tesseract; NULL = not checked yet
	ALTER TABLE pages ADD COLUMN check_text    TEXT;
	ALTER TABLE pages ADD COLUMN check_words   INTEGER;  -- confident words of the second reading
	ALTER TABLE pages ADD COLUMN check_matched INTEGER;  -- of them, found in the primary text
	ALTER TABLE pages ADD COLUMN check_missing TEXT;     -- confident words not found, space-separated
	ALTER TABLE pages ADD COLUMN flagged       INTEGER NOT NULL DEFAULT 0;`,
	// 4: longest run of consecutive confident words missing from the primary text.
	`ALTER TABLE pages ADD COLUMN check_run INTEGER;`,
	// 5: documents cut out of originals (ADR-0003) and the split state.
	`CREATE TABLE documents (
		id         TEXT PRIMARY KEY,         -- <sha256[:16]>-<first page>: stable while the cut holds
		sha256     TEXT NOT NULL REFERENCES originals(sha256),
		first_page INTEGER NOT NULL,
		last_page  INTEGER NOT NULL,
		created    TEXT NOT NULL
	);
	CREATE INDEX documents_sha256 ON documents(sha256);
	CREATE TABLE split (
		sha256        TEXT PRIMARY KEY REFERENCES originals(sha256),
		status        TEXT NOT NULL,        -- done | review | error
		method        TEXT NOT NULL,        -- single | model
		engine        TEXT NOT NULL,        -- the model that answered, or "-"
		cost_usd      REAL NOT NULL DEFAULT 0,
		disagreements TEXT,                 -- strong rules the model contradicted
		error         TEXT,
		pipeline      INTEGER NOT NULL,
		updated       TEXT NOT NULL
	);`,
	// 6: text layers per document (ADR-0005).
	`CREATE TABLE texts (
		doc_id              TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
		status              TEXT NOT NULL,   -- done | flagged | error
		essential           TEXT,
		summary             TEXT,            -- de-identified
		removed             TEXT,            -- JSON: lines the essential text left out
		restored_repeats    INTEGER NOT NULL DEFAULT 0,
		restored_suspicious INTEGER NOT NULL DEFAULT 0,
		flags               TEXT,            -- failed summary checks after the retry
		error               TEXT,
		model               TEXT NOT NULL,
		cost_usd            REAL NOT NULL DEFAULT 0,
		pipeline            INTEGER NOT NULL,
		updated             TEXT NOT NULL
	);`,
	// 7: persons of the account, links to documents, document dates and the
	// full-text index the MCP search runs on (ADR-0004).
	`CREATE TABLE persons (
		code       TEXT PRIMARY KEY,         -- short code used in file names, e.g. ЕБВ
		label      TEXT NOT NULL,            -- what the assistant sees, e.g. "Папа"
		full_name  TEXT NOT NULL,
		aliases    TEXT NOT NULL DEFAULT '[]', -- JSON: spellings found in documents
		birth_date TEXT,                     -- YYYY-MM-DD
		created    TEXT NOT NULL
	);
	CREATE TABLE doc_persons (
		doc_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		code   TEXT NOT NULL REFERENCES persons(code) ON DELETE CASCADE,
		method TEXT NOT NULL,                -- rule | manual
		reason TEXT NOT NULL,
		PRIMARY KEY (doc_id, code)
	);
	ALTER TABLE texts ADD COLUMN doc_date TEXT;          -- YYYY-MM-DD
	ALTER TABLE texts ADD COLUMN doc_date_source TEXT;   -- where the date came from
	CREATE VIRTUAL TABLE docs_fts USING fts5(doc_id UNINDEXED, summary, essential,
		tokenize = 'unicode61 remove_diacritics 2');`,
}

// Catalog is an open account catalog.
type Catalog struct {
	db *sql.DB
}

// Open opens (and creates or migrates) the catalog at path.
func Open(path string) (*Catalog, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open catalog: %w", err)
	}
	db.SetMaxOpenConns(1) // one writer; SQLite serialises anyway
	c := &Catalog{db: db}
	if err := c.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}

// Close closes the catalog.
func (c *Catalog) Close() error { return c.db.Close() }

func (c *Catalog) migrate(ctx context.Context) error {
	var v int
	if err := c.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := v; i < len(schema); i++ {
		tx, err := c.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migrate to %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, schema[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate to %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate to %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate to %d: %w", i+1, err)
		}
	}
	return nil
}

// Source is what the catalog remembers about one place an original was seen.
type Source struct {
	URI    string
	SHA256 string
	Size   int64
	MTime  time.Time
}

// LookupSource returns the stored source for uri, or nil if it is unknown.
func (c *Catalog) LookupSource(ctx context.Context, uri string) (*Source, error) {
	var s Source
	var mtime string
	err := c.db.QueryRowContext(ctx,
		"SELECT uri, sha256, size, mtime FROM sources WHERE uri = ?", uri).
		Scan(&s.URI, &s.SHA256, &s.Size, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup source: %w", err)
	}
	s.MTime, err = time.Parse(time.RFC3339Nano, mtime)
	if err != nil {
		return nil, fmt.Errorf("lookup source %s: bad mtime: %w", uri, err)
	}
	return &s, nil
}

// Seen is one observation of a file during intake.
type Seen struct {
	URI     string
	SHA256  string
	Size    int64
	MTime   time.Time
	MIME    string // by content
	ExtMIME string // by file extension, may be empty
	At      time.Time
}

// RecordSeen stores an observation. It reports whether the original (by
// content) was new to the account.
func (c *Catalog) RecordSeen(ctx context.Context, s Seen) (newOriginal bool, err error) {
	at := s.At.UTC().Format(time.RFC3339Nano)
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("record %s: %w", s.URI, err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO originals (sha256, size, mime, first_seen) VALUES (?, ?, ?, ?)
		 ON CONFLICT(sha256) DO NOTHING`, s.SHA256, s.Size, s.MIME, at)
	if err != nil {
		return false, fmt.Errorf("record original %s: %w", s.URI, err)
	}
	n, _ := res.RowsAffected()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO sources (uri, sha256, size, mtime, ext_mime, first_seen, last_seen, missing)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0)
		 ON CONFLICT(uri) DO UPDATE SET sha256 = excluded.sha256, size = excluded.size,
		   mtime = excluded.mtime, ext_mime = excluded.ext_mime,
		   last_seen = excluded.last_seen, missing = 0`,
		s.URI, s.SHA256, s.Size, s.MTime.UTC().Format(time.RFC3339Nano), s.ExtMIME, at, at)
	if err != nil {
		return false, fmt.Errorf("record source %s: %w", s.URI, err)
	}
	return n == 1, tx.Commit()
}

// TouchSource marks an unchanged source as seen again.
func (c *Catalog) TouchSource(ctx context.Context, uri string, at time.Time) error {
	_, err := c.db.ExecContext(ctx, "UPDATE sources SET last_seen = ?, missing = 0 WHERE uri = ?",
		at.UTC().Format(time.RFC3339Nano), uri)
	if err != nil {
		return fmt.Errorf("touch source %s: %w", uri, err)
	}
	return nil
}

// MarkMissing flags sources under prefix that were not seen since `since`.
// It returns how many were flagged. Nothing is deleted: the catalog remembers
// what it once saw.
func (c *Catalog) MarkMissing(ctx context.Context, prefix string, since time.Time) (int64, error) {
	res, err := c.db.ExecContext(ctx,
		"UPDATE sources SET missing = 1 WHERE uri LIKE ? ESCAPE '\\' AND last_seen < ? AND missing = 0",
		escapeLike(prefix)+"%", since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("mark missing: %w", err)
	}
	return res.RowsAffected()
}

func escapeLike(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '%' || r == '_' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

// Run is the record of one intake run.
type Run struct {
	ID                              int64
	Root                            string
	Started, Finished               time.Time
	Files, Hashed, NewFiles, Errors int
}

// StartRun opens a run record.
func (c *Catalog) StartRun(ctx context.Context, root string, at time.Time) (int64, error) {
	res, err := c.db.ExecContext(ctx, "INSERT INTO intake_runs (root, started) VALUES (?, ?)",
		root, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	return res.LastInsertId()
}

// FinishRun closes a run record with its counters.
func (c *Catalog) FinishRun(ctx context.Context, r Run) error {
	_, err := c.db.ExecContext(ctx,
		"UPDATE intake_runs SET finished = ?, files = ?, hashed = ?, new_files = ?, errors = ? WHERE id = ?",
		r.Finished.UTC().Format(time.RFC3339Nano), r.Files, r.Hashed, r.NewFiles, r.Errors, r.ID)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	return nil
}

// Duplicate is an original seen in more than one place.
type Duplicate struct {
	SHA256 string
	Size   int64
	MIME   string
	URIs   []string
}

// Duplicates lists originals with more than one present source, largest first.
func (c *Catalog) Duplicates(ctx context.Context) ([]Duplicate, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT o.sha256, o.size, o.mime, s.uri
		FROM originals o JOIN sources s ON s.sha256 = o.sha256
		WHERE s.missing = 0 AND o.sha256 IN (
			SELECT sha256 FROM sources WHERE missing = 0 GROUP BY sha256 HAVING COUNT(*) > 1)
		ORDER BY o.size DESC, o.sha256, s.uri`)
	if err != nil {
		return nil, fmt.Errorf("duplicates: %w", err)
	}
	defer rows.Close()
	var out []Duplicate
	for rows.Next() {
		var sha, mime, uri string
		var size int64
		if err := rows.Scan(&sha, &size, &mime, &uri); err != nil {
			return nil, fmt.Errorf("duplicates: %w", err)
		}
		if len(out) == 0 || out[len(out)-1].SHA256 != sha {
			out = append(out, Duplicate{SHA256: sha, Size: size, MIME: mime})
		}
		out[len(out)-1].URIs = append(out[len(out)-1].URIs, uri)
	}
	return out, rows.Err()
}

// Stats summarises the catalog.
type Stats struct {
	Originals, Sources, Missing int
	Bytes                       int64
	ByMIME                      map[string]int
}

// Stats counts originals, sources and types.
func (c *Catalog) Stats(ctx context.Context) (Stats, error) {
	st := Stats{ByMIME: map[string]int{}}
	err := c.db.QueryRowContext(ctx,
		"SELECT COUNT(*), COALESCE(SUM(size), 0) FROM originals").Scan(&st.Originals, &st.Bytes)
	if err != nil {
		return st, fmt.Errorf("stats: %w", err)
	}
	err = c.db.QueryRowContext(ctx,
		"SELECT COUNT(*), COALESCE(SUM(missing), 0) FROM sources").Scan(&st.Sources, &st.Missing)
	if err != nil {
		return st, fmt.Errorf("stats: %w", err)
	}
	rows, err := c.db.QueryContext(ctx, "SELECT mime, COUNT(*) FROM originals GROUP BY mime")
	if err != nil {
		return st, fmt.Errorf("stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var n int
		if err := rows.Scan(&m, &n); err != nil {
			return st, fmt.Errorf("stats: %w", err)
		}
		st.ByMIME[m] = n
	}
	return st, rows.Err()
}

// MIMEMismatches lists sources whose extension disagrees with their content.
func (c *Catalog) MIMEMismatches(ctx context.Context) ([][3]string, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT s.uri, s.ext_mime, o.mime FROM sources s JOIN originals o ON o.sha256 = s.sha256
		WHERE s.missing = 0 AND s.ext_mime != '' AND s.ext_mime != o.mime ORDER BY s.uri`)
	if err != nil {
		return nil, fmt.Errorf("mime mismatches: %w", err)
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var r [3]string
		if err := rows.Scan(&r[0], &r[1], &r[2]); err != nil {
			return nil, fmt.Errorf("mime mismatches: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
