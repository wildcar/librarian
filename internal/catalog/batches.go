package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Batch is what came into the account at one time, with the persons and
// characteristics the owner gave it; its documents inherit them (ADR-0006).
type Batch struct {
	ID        int64
	Kind      string // folder | upload | link
	Root      string
	Label     string
	Created   time.Time
	Persons   []string
	Attrs     []Attr
	Originals int // filled by Batches
	Documents int // filled by Batches
}

// Attr is one characteristic: a key and one of its values.
type Attr struct {
	Key, Value string
}

func (a Attr) String() string { return a.Key + "=" + a.Value }

// AttrKey normalises a characteristic key: trimmed and lower-cased, so
// «Тема» and «тема » are one key.
func AttrKey(k string) string { return strings.ToLower(strings.TrimSpace(k)) }

// ParseAttr reads "key=value".
func ParseAttr(s string) (Attr, error) {
	k, v, ok := strings.Cut(s, "=")
	a := Attr{Key: AttrKey(k), Value: strings.TrimSpace(v)}
	if !ok || a.Key == "" || a.Value == "" {
		return a, fmt.Errorf("характеристика %q: нужно ключ=значение", s)
	}
	if a.Key == MissingDate || a.Key == MissingPerson {
		return a, fmt.Errorf("характеристика %q: ключ %s занят", s, a.Key)
	}
	return a, nil
}

// CreateBatch records a batch and the originals it holds. Its persons must
// exist.
func (c *Catalog) CreateBatch(ctx context.Context, b Batch, originals []string, at time.Time) (int64, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "INSERT INTO batches (kind, root, label, created) VALUES (?, ?, ?, ?)",
		b.Kind, b.Root, b.Label, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("create batch: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, sha := range originals {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO batch_originals (batch_id, sha256) VALUES (?, ?)", id, sha); err != nil {
			return 0, fmt.Errorf("create batch: %w", err)
		}
	}
	if err := setBatchPersons(ctx, tx, id, b.Persons); err != nil {
		return 0, err
	}
	for _, a := range b.Attrs {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO batch_attrs (batch_id, key, value) VALUES (?, ?, ?)",
			id, a.Key, a.Value); err != nil {
			return 0, fmt.Errorf("create batch: %w", err)
		}
	}
	return id, tx.Commit()
}

// BatchChange edits a batch; nil Persons leaves them as they are.
type BatchChange struct {
	Persons *[]string
	Add     []Attr
	Remove  []Attr // an empty Value removes every value of the key
}

// ChangeBatch applies a change to a batch. Documents follow on the next index.
func (c *Catalog) ChangeBatch(ctx context.Context, id int64, ch BatchChange) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM batches WHERE id = ?", id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("пачки %d нет", id)
	}
	if ch.Persons != nil {
		if _, err := tx.ExecContext(ctx, "DELETE FROM batch_persons WHERE batch_id = ?", id); err != nil {
			return err
		}
		if err := setBatchPersons(ctx, tx, id, *ch.Persons); err != nil {
			return err
		}
	}
	for _, a := range ch.Remove {
		q, args := "DELETE FROM batch_attrs WHERE batch_id = ? AND key = ?", []any{id, a.Key}
		if a.Value != "" {
			q, args = q+" AND value = ?", append(args, a.Value)
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	for _, a := range ch.Add {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO batch_attrs (batch_id, key, value) VALUES (?, ?, ?)",
			id, a.Key, a.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func setBatchPersons(ctx context.Context, tx *sql.Tx, id int64, codes []string) error {
	for _, code := range codes {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM persons WHERE code = ?", code).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("человека с кодом %s нет: добавь его (--name) или выбери из person list", code)
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO batch_persons (batch_id, code) VALUES (?, ?)", id, code); err != nil {
			return err
		}
	}
	return nil
}

// Batches lists the account's batches, newest first, with what they hold.
func (c *Catalog) Batches(ctx context.Context) ([]Batch, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT b.id, b.kind, COALESCE(b.root, ''), COALESCE(b.label, ''), b.created,
		  (SELECT COUNT(*) FROM batch_originals bo WHERE bo.batch_id = b.id),
		  (SELECT COUNT(*) FROM documents d JOIN batch_originals bo ON bo.sha256 = d.sha256 WHERE bo.batch_id = b.id)
		FROM batches b ORDER BY b.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("batches: %w", err)
	}
	var out []Batch
	for rows.Next() {
		var b Batch
		var created string
		if err := rows.Scan(&b.ID, &b.Kind, &b.Root, &b.Label, &created, &b.Originals, &b.Documents); err != nil {
			rows.Close()
			return nil, fmt.Errorf("batches: %w", err)
		}
		b.Created, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Persons, out[i].Attrs, err = c.batchLabels(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Catalog) batchLabels(ctx context.Context, id int64) ([]string, []Attr, error) {
	var persons []string
	rows, err := c.db.QueryContext(ctx, "SELECT code FROM batch_persons WHERE batch_id = ? ORDER BY code", id)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, nil, err
		}
		persons = append(persons, s)
	}
	rows.Close()
	var attrs []Attr
	rows, err = c.db.QueryContext(ctx, "SELECT key, value FROM batch_attrs WHERE batch_id = ? ORDER BY key, value", id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Attr
		if err := rows.Scan(&a.Key, &a.Value); err != nil {
			return nil, nil, err
		}
		attrs = append(attrs, a)
	}
	return persons, attrs, rows.Err()
}

// Inherited is what one batch passes on to the documents of an original.
type Inherited struct {
	BatchID int64
	Persons []string
	Attrs   []Attr
}

// BatchesOf returns the batches an original belongs to, oldest first.
func (c *Catalog) BatchesOf(ctx context.Context, sha string) ([]Inherited, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT batch_id FROM batch_originals WHERE sha256 = ? ORDER BY batch_id", sha)
	if err != nil {
		return nil, fmt.Errorf("batches of %s: %w", sha[:12], err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []Inherited
	for _, id := range ids {
		p, a, err := c.batchLabels(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, Inherited{BatchID: id, Persons: p, Attrs: a})
	}
	return out, nil
}

// ClearDerived removes what index derives for a document — rule and batch
// links, batch characteristics — before it is redone. Manual ones stay.
func (c *Catalog) ClearDerived(ctx context.Context, docID string) error {
	if _, err := c.db.ExecContext(ctx, "DELETE FROM doc_persons WHERE doc_id = ? AND method IN ('rule', 'batch')", docID); err != nil {
		return err
	}
	_, err := c.db.ExecContext(ctx, "DELETE FROM doc_attrs WHERE doc_id = ? AND source LIKE 'batch:%'", docID)
	return err
}

// SetDocAttr gives a document a derived characteristic. One given by the
// owner or an assistant is never replaced.
func (c *Catalog) SetDocAttr(ctx context.Context, docID string, a Attr, source string) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO doc_attrs (doc_id, key, value, source) VALUES (?, ?, ?, ?)
		ON CONFLICT(doc_id, key, value) DO UPDATE SET source = excluded.source
		WHERE doc_attrs.source NOT IN ('manual', 'assistant')`, docID, a.Key, a.Value, source)
	if err != nil {
		return fmt.Errorf("attr %s of %s: %w", a, docID, err)
	}
	return nil
}

// DocAttrs returns a document's characteristics: key → values.
func (c *Catalog) DocAttrs(ctx context.Context, docID string) (map[string][]string, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT key, value FROM doc_attrs WHERE doc_id = ? ORDER BY key, value", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out map[string][]string
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[k] = append(out[k], v)
	}
	return out, rows.Err()
}

// DocBatches returns the batches a document came in.
func (c *Catalog) DocBatches(ctx context.Context, docID string) ([]int64, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT bo.batch_id FROM documents d
		JOIN batch_originals bo ON bo.sha256 = d.sha256 WHERE d.id = ? ORDER BY bo.batch_id`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// BatchProgress is how far a batch's originals have come (ADR-0007).
type BatchProgress struct {
	Originals   int
	Read        int // text of every page stored
	Unsupported int // a kind of file not read yet (DOCX, XLSX, archives…)
	Failed      int // reading failed; retried on the next run
	Split       int // cut into documents
	Documents   int
	Ready       int // documents with text layers: in search
}

// Done says whether nothing is left to do for the batch.
func (p BatchProgress) Done() bool {
	return p.Read+p.Unsupported == p.Originals && p.Split == p.Read && p.Ready == p.Documents
}

// BatchProgresses returns every batch's progress, by batch id.
func (c *Catalog) BatchProgresses(ctx context.Context) (map[int64]BatchProgress, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT bo.batch_id,
		  COUNT(*),
		  SUM(e.status = 'done'),
		  SUM(e.status = 'unsupported'),
		  SUM(e.status = 'error'),
		  SUM(s.status IS NOT NULL AND s.status != 'error'),
		  (SELECT COUNT(*) FROM documents d JOIN batch_originals x ON x.sha256 = d.sha256 WHERE x.batch_id = bo.batch_id),
		  (SELECT COUNT(*) FROM documents d JOIN batch_originals x ON x.sha256 = d.sha256
		     JOIN texts t ON t.doc_id = d.id AND t.status IN ('done', 'flagged') WHERE x.batch_id = bo.batch_id)
		FROM batch_originals bo
		LEFT JOIN extraction e ON e.sha256 = bo.sha256
		LEFT JOIN split s ON s.sha256 = bo.sha256
		GROUP BY bo.batch_id`)
	if err != nil {
		return nil, fmt.Errorf("batch progress: %w", err)
	}
	defer rows.Close()
	out := map[int64]BatchProgress{}
	for rows.Next() {
		var id int64
		var p BatchProgress
		var read, unsup, failed, split sql.NullInt64
		if err := rows.Scan(&id, &p.Originals, &read, &unsup, &failed, &split, &p.Documents, &p.Ready); err != nil {
			return nil, fmt.Errorf("batch progress: %w", err)
		}
		p.Read, p.Unsupported, p.Failed, p.Split = int(read.Int64), int(unsup.Int64), int(failed.Int64), int(split.Int64)
		out[id] = p
	}
	return out, rows.Err()
}
