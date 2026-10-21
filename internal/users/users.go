// Package users is the service-level registry (ADR-0007): which e-mail logs
// into which account, the one-time links mailed for a login, and web sessions.
// It lives in <home>/service.sqlite, apart from the account catalogs. Only
// hashes of secrets are stored, as with account tokens.
package users

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wildcar/librarian/internal/account"
)

// Lifetimes and limits.
const (
	LinkTTL       = 15 * time.Minute
	SessionTTL    = 30 * 24 * time.Hour
	LinksPerHour  = 5 // letters per e-mail per hour
	PurposeWeb    = "web"
	PurposeDirect = "direct" // made by the administrator; logs in the browser that opens it
)

// ErrUnknown covers an unknown e-mail, link or session; callers must not tell
// these apart to the client.
var ErrUnknown = errors.New("unknown or expired")

// ErrTooMany is returned when an e-mail asked for too many letters lately.
var ErrTooMany = errors.New("too many login letters, try later")

var schema = []string{
	// 1: users, login links, web sessions.
	`CREATE TABLE users (
		email   TEXT PRIMARY KEY,
		account TEXT NOT NULL,
		created TEXT NOT NULL
	);
	CREATE TABLE logins (
		link_hash TEXT PRIMARY KEY,
		wait_hash TEXT,              -- the browser that asked; NULL for a direct link
		email     TEXT NOT NULL,
		purpose   TEXT NOT NULL,
		ip        TEXT NOT NULL DEFAULT '',
		agent     TEXT NOT NULL DEFAULT '',
		created   TEXT NOT NULL,
		expires   TEXT NOT NULL,
		confirmed TEXT,              -- the owner pressed the button
		taken     TEXT               -- the waiting browser got its session
	);
	CREATE INDEX logins_wait ON logins(wait_hash);
	CREATE INDEX logins_email ON logins(email, created);
	CREATE TABLE sessions (
		hash      TEXT PRIMARY KEY,
		email     TEXT NOT NULL,
		created   TEXT NOT NULL,
		expires   TEXT NOT NULL,
		last_seen TEXT NOT NULL
	);`,
}

// Registry is the opened service registry.
type Registry struct {
	db   *sql.DB
	home string
}

// Open opens or creates the registry under the data root.
func Open(home string) (*Registry, error) {
	path := filepath.Join(home, "service.sqlite")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open registry: %w", err)
	}
	db.SetMaxOpenConns(1)
	r := &Registry{db: db, home: home}
	if err := r.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	// E-mails are personal data: the registry is the service user's alone.
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(f, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.Close()
			return nil, err
		}
	}
	return r, nil
}

// Close closes the registry.
func (r *Registry) Close() error { return r.db.Close() }

func (r *Registry) migrate(ctx context.Context) error {
	var v int
	if err := r.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read registry version: %w", err)
	}
	for i := v; i < len(schema); i++ {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, schema[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate registry to %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// NormEmail checks an address and returns it lower-cased, without a display name.
func NormEmail(s string) (string, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil || a.Name != "" || !strings.Contains(a.Address, "@") {
		return "", fmt.Errorf("invalid e-mail %q", s)
	}
	return strings.ToLower(a.Address), nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func secret() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	s := base64.RawURLEncoding.EncodeToString(buf)
	return s, hash(s), nil
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// SetEmail binds an e-mail to an existing account; an e-mail logs into one
// account only, so binding it again moves it.
func (r *Registry) SetEmail(ctx context.Context, email, accountID string, at time.Time) error {
	email, err := NormEmail(email)
	if err != nil {
		return err
	}
	if _, err := account.Open(r.home, accountID); err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO users (email, account, created) VALUES (?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET account = excluded.account`, email, accountID, stamp(at))
	return err
}

// RemoveEmail unbinds an e-mail and ends its sessions.
func (r *Registry) RemoveEmail(ctx context.Context, email string) error {
	email, err := NormEmail(email)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE email = ?`, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrUnknown, email)
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM sessions WHERE email = ?`, email)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM logins WHERE email = ?`, email)
	return err
}

// User is an e-mail and its account.
type User struct {
	Email, Account string
}

// Users lists the bound e-mails.
func (r *Registry) Users(ctx context.Context) ([]User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT email, account FROM users ORDER BY account, email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Email, &u.Account); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Account opens the account an e-mail logs into.
func (r *Registry) Account(ctx context.Context, email string) (*account.Account, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT account FROM users WHERE email = ?`, email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknown
	}
	if err != nil {
		return nil, err
	}
	return account.Open(r.home, id)
}

// Request describes who asked for a letter, for the letter itself.
type Request struct {
	Purpose string
	IP      string
	Agent   string
}

// Login is a started login: Link goes into the letter, Wait stays with the
// browser that asked.
type Login struct {
	Email   string
	Link    string
	Wait    string
	Expires time.Time
}

// StartLogin makes a one-time link for a known e-mail. For an unknown one it
// returns ErrUnknown, and the caller shows the same page without a letter.
func (r *Registry) StartLogin(ctx context.Context, email string, req Request, at time.Time) (Login, error) {
	email, err := NormEmail(email)
	if err != nil {
		return Login{}, err
	}
	if _, err := r.Account(ctx, email); err != nil {
		return Login{}, ErrUnknown
	}
	var recent int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM logins WHERE email = ? AND created > ?`,
		email, stamp(at.Add(-time.Hour))).Scan(&recent); err != nil {
		return Login{}, err
	}
	if recent >= LinksPerHour {
		return Login{}, ErrTooMany
	}
	link, linkHash, err := secret()
	if err != nil {
		return Login{}, err
	}
	l := Login{Email: email, Link: link, Expires: at.Add(LinkTTL)}
	var waitHash any
	if req.Purpose != PurposeDirect {
		wait, h, err := secret()
		if err != nil {
			return Login{}, err
		}
		l.Wait, waitHash = wait, h
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO logins (link_hash, wait_hash, email, purpose, ip, agent, created, expires)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, linkHash, waitHash, email, req.Purpose, req.IP, req.Agent, stamp(at), stamp(l.Expires))
	if err != nil {
		return Login{}, err
	}
	// Old links are of no use to anyone.
	_, err = r.db.ExecContext(ctx, `DELETE FROM logins WHERE expires < ?`, stamp(at.Add(-24*time.Hour)))
	return l, err
}

// Pending is what the confirmation page shows about a link before the button.
type Pending struct {
	Email   string
	Purpose string
	IP      string
	Agent   string
	Created time.Time
}

func (r *Registry) live(ctx context.Context, link string, at time.Time) (Pending, error) {
	var p Pending
	var created string
	var confirmed sql.NullString
	// A link dies with its e-mail's binding.
	err := r.db.QueryRowContext(ctx, `SELECT email, purpose, ip, agent, created, confirmed FROM logins
		WHERE link_hash = ? AND expires > ? AND email IN (SELECT email FROM users)`, hash(link), stamp(at)).Scan(&p.Email, &p.Purpose, &p.IP, &p.Agent, &created, &confirmed)
	if errors.Is(err, sql.ErrNoRows) || confirmed.Valid {
		return Pending{}, ErrUnknown
	}
	if err != nil {
		return Pending{}, err
	}
	p.Created, _ = time.Parse(time.RFC3339, created)
	return p, nil
}

// Peek shows a link's request without using it: opening the letter's link
// grants nothing, since mail scanners open links on their own.
func (r *Registry) Peek(ctx context.Context, link string, at time.Time) (Pending, error) {
	return r.live(ctx, link, at)
}

// Confirm uses a link (the owner pressed the button) and returns its e-mail.
func (r *Registry) Confirm(ctx context.Context, link string, at time.Time) (string, error) {
	p, err := r.live(ctx, link, at)
	if err != nil {
		return "", err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE logins SET confirmed = ? WHERE link_hash = ? AND confirmed IS NULL`, stamp(at), hash(link))
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", ErrUnknown
	}
	return p.Email, nil
}

// WaitState is what the waiting browser learns when it asks.
type WaitState int

const (
	Waiting WaitState = iota
	Confirmed
	Expired
)

// Take tells the waiting browser whether its login was confirmed; the first
// time it was, it returns the e-mail and marks the login taken.
func (r *Registry) Take(ctx context.Context, wait string, at time.Time) (WaitState, string, error) {
	var email, expires string
	var confirmed, taken sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT email, expires, confirmed, taken FROM logins WHERE wait_hash = ?`, hash(wait)).
		Scan(&email, &expires, &confirmed, &taken)
	if errors.Is(err, sql.ErrNoRows) {
		return Expired, "", nil
	}
	if err != nil {
		return Expired, "", err
	}
	switch {
	case taken.Valid:
		return Expired, "", nil
	case confirmed.Valid:
		res, err := r.db.ExecContext(ctx, `UPDATE logins SET taken = ? WHERE wait_hash = ? AND taken IS NULL`, stamp(at), hash(wait))
		if err != nil {
			return Expired, "", err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return Expired, "", nil
		}
		return Confirmed, email, nil
	case expires <= stamp(at):
		return Expired, "", nil
	}
	return Waiting, "", nil
}

// NewSession starts a web session for an e-mail and returns its cookie value.
func (r *Registry) NewSession(ctx context.Context, email string, at time.Time) (string, error) {
	s, h, err := secret()
	if err != nil {
		return "", err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO sessions (hash, email, created, expires, last_seen) VALUES (?, ?, ?, ?, ?)`,
		h, email, stamp(at), stamp(at.Add(SessionTTL)), stamp(at))
	if err != nil {
		return "", err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires < ?`, stamp(at))
	return s, err
}

// Session returns the e-mail of a live session and extends it.
func (r *Registry) Session(ctx context.Context, s string, at time.Time) (string, error) {
	var email string
	err := r.db.QueryRowContext(ctx, `SELECT email FROM sessions WHERE hash = ? AND expires > ?`, hash(s), stamp(at)).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnknown
	}
	if err != nil {
		return "", err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE sessions SET last_seen = ?, expires = ? WHERE hash = ?`,
		stamp(at), stamp(at.Add(SessionTTL)), hash(s))
	return email, err
}

// EndSession logs a session out.
func (r *Registry) EndSession(ctx context.Context, s string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE hash = ?`, hash(s))
	return err
}
