package users

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// OAuth lifetimes (ADR-0007).
const (
	CodeTTL    = 5 * time.Minute
	AccessTTL  = time.Hour
	RefreshTTL = 90 * 24 * time.Hour
)

// ErrGrant is an invalid code, token or client in a token request (OAuth
// "invalid_grant" / "invalid_client"); callers do not tell the cases apart.
var ErrGrant = errors.New("invalid grant")

// Client is an assistant registered by dynamic client registration.
type Client struct {
	ID           string
	Secret       string // only right after registration, for a confidential client
	Name         string
	RedirectURIs []string
	Confidential bool
}

// AuthRequest is what an assistant asked for at the authorization endpoint.
type AuthRequest struct {
	ClientID    string `json:"client_id"`
	RedirectURI string `json:"redirect_uri"`
	State       string `json:"state,omitempty"`
	Challenge   string `json:"code_challenge"`
	Resource    string `json:"resource,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

// CheckRedirect accepts an https address, or http on the loopback interface
// for assistants running on the owner's computer (RFC 8252).
func CheckRedirect(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" {
		return fmt.Errorf("redirect_uri %q: need an absolute address without a fragment", raw)
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && loopback(u.Hostname()):
		return nil
	}
	return fmt.Errorf("redirect_uri %q: only https, or http on localhost", raw)
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AllowsRedirect says whether uri is one the client registered. A loopback
// address matches on any port: local clients pick a free port each run.
func (c Client) AllowsRedirect(uri string) bool {
	for _, r := range c.RedirectURIs {
		if r == uri {
			return true
		}
		a, err1 := url.Parse(r)
		b, err2 := url.Parse(uri)
		if err1 == nil && err2 == nil && a.Scheme == "http" && loopback(a.Hostname()) &&
			a.Scheme == b.Scheme && a.Hostname() == b.Hostname() && a.Path == b.Path && a.RawQuery == b.RawQuery {
			return true
		}
	}
	return false
}

// Host is what the letter and the «Ассистенты» page show next to the
// self-declared name: the host the code is sent to.
func Host(redirect string) string {
	u, err := url.Parse(redirect)
	if err != nil {
		return ""
	}
	if loopback(u.Hostname()) {
		return "этот компьютер (" + u.Hostname() + ")"
	}
	return u.Hostname()
}

// RegisterClient stores a new client (RFC 7591).
func (r *Registry) RegisterClient(ctx context.Context, name string, redirects []string, confidential bool, at time.Time) (Client, error) {
	if len(redirects) == 0 {
		return Client{}, fmt.Errorf("redirect_uris is required")
	}
	for _, u := range redirects {
		if err := CheckRedirect(u); err != nil {
			return Client{}, err
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "без названия"
	}
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	id, _, err := secret()
	if err != nil {
		return Client{}, err
	}
	c := Client{ID: "c_" + id[:22], Name: name, RedirectURIs: redirects, Confidential: confidential}
	var secretHash any
	if confidential {
		s, h, err := secret()
		if err != nil {
			return Client{}, err
		}
		c.Secret, secretHash = s, h
	}
	js, _ := json.Marshal(redirects)
	_, err = r.db.ExecContext(ctx, `INSERT INTO clients (client_id, secret_hash, name, redirect_uris, created) VALUES (?, ?, ?, ?, ?)`,
		c.ID, secretHash, c.Name, string(js), stamp(at))
	return c, err
}

// Client looks a client up.
func (r *Registry) Client(ctx context.Context, id string) (Client, error) {
	var c Client
	var js string
	var secretHash sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT client_id, name, redirect_uris, secret_hash FROM clients WHERE client_id = ?`, id).
		Scan(&c.ID, &c.Name, &js, &secretHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, ErrUnknown
	}
	if err != nil {
		return Client{}, err
	}
	c.Confidential = secretHash.Valid
	return c, json.Unmarshal([]byte(js), &c.RedirectURIs)
}

// authClient checks a client at the token endpoint: a confidential client
// must present its secret.
func (r *Registry) authClient(ctx context.Context, id, sec string) error {
	var secretHash sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT secret_hash FROM clients WHERE client_id = ?`, id).Scan(&secretHash)
	if err != nil {
		return ErrGrant
	}
	if secretHash.Valid && subtle.ConstantTimeCompare([]byte(hash(sec)), []byte(secretHash.String)) != 1 {
		return ErrGrant
	}
	return nil
}

// IssueCode answers an authorization request the owner allowed.
func (r *Registry) IssueCode(ctx context.Context, email string, req AuthRequest, at time.Time) (string, error) {
	code, h, err := secret()
	if err != nil {
		return "", err
	}
	js, _ := json.Marshal(req)
	_, err = r.db.ExecContext(ctx, `INSERT INTO codes (hash, request, email, expires) VALUES (?, ?, ?, ?)`,
		h, string(js), email, stamp(at.Add(CodeTTL)))
	if err != nil {
		return "", err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM codes WHERE expires < ?`, stamp(at.Add(-24*time.Hour)))
	return code, err
}

// Tokens is a token endpoint answer.
type Tokens struct {
	Access, Refresh string
	ExpiresIn       time.Duration
}

// Exchange trades a code for tokens: one use, the same client and redirect,
// and the PKCE verifier that matches the challenge (S256). A code used twice
// revokes what it gave the first time (RFC 6749 §4.1.2).
func (r *Registry) Exchange(ctx context.Context, code, clientID, clientSecret, redirect, verifier string, at time.Time) (Tokens, error) {
	if err := r.authClient(ctx, clientID, clientSecret); err != nil {
		return Tokens{}, err
	}
	var js, email, expires string
	var used sql.NullString
	var grant sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT request, email, expires, used, grant_id FROM codes WHERE hash = ?`, hash(code)).
		Scan(&js, &email, &expires, &used, &grant)
	if err != nil {
		return Tokens{}, ErrGrant
	}
	if used.Valid {
		if grant.Valid {
			r.revoke(ctx, grant.Int64, at)
		}
		return Tokens{}, ErrGrant
	}
	var req AuthRequest
	if err := json.Unmarshal([]byte(js), &req); err != nil {
		return Tokens{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	if expires <= stamp(at) || req.ClientID != clientID || req.RedirectURI != redirect ||
		subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(req.Challenge)) != 1 {
		return Tokens{}, ErrGrant
	}
	if _, err := r.Account(ctx, email); err != nil {
		return Tokens{}, ErrGrant
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO grants (client_id, email, host, created) VALUES (?, ?, ?, ?)`,
		clientID, email, Host(req.RedirectURI), stamp(at))
	if err != nil {
		return Tokens{}, err
	}
	id, _ := res.LastInsertId()
	res, err = r.db.ExecContext(ctx, `UPDATE codes SET used = ?, grant_id = ? WHERE hash = ? AND used IS NULL`, stamp(at), id, hash(code))
	if err != nil {
		return Tokens{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		r.revoke(ctx, id, at)
		return Tokens{}, ErrGrant
	}
	return r.tokens(ctx, id, at)
}

func (r *Registry) tokens(ctx context.Context, grant int64, at time.Time) (Tokens, error) {
	access, _, err := secret()
	if err != nil {
		return Tokens{}, err
	}
	refresh, _, err := secret()
	if err != nil {
		return Tokens{}, err
	}
	t := Tokens{Access: "lia_" + access, Refresh: "lir_" + refresh, ExpiresIn: AccessTTL}
	ah, rh := hash(t.Access), hash(t.Refresh)
	_, err = r.db.ExecContext(ctx, `INSERT INTO tokens (hash, grant_id, kind, expires) VALUES (?, ?, 'access', ?), (?, ?, 'refresh', ?)`,
		ah, grant, stamp(at.Add(AccessTTL)), rh, grant, stamp(at.Add(RefreshTTL)))
	if err != nil {
		return Tokens{}, err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM tokens WHERE expires < ?`, stamp(at))
	return t, err
}

// Refresh rotates a refresh token. A refresh token used a second time means
// it leaked: the whole connection is revoked.
func (r *Registry) Refresh(ctx context.Context, refresh, clientID, clientSecret string, at time.Time) (Tokens, error) {
	if err := r.authClient(ctx, clientID, clientSecret); err != nil {
		return Tokens{}, err
	}
	var grant int64
	var expires, client string
	var used, revoked sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT t.grant_id, t.expires, t.used, g.client_id, g.revoked FROM tokens t
		JOIN grants g ON g.id = t.grant_id WHERE t.hash = ? AND t.kind = 'refresh'
		AND g.email IN (SELECT email FROM users)`, hash(refresh)).Scan(&grant, &expires, &used, &client, &revoked)
	if err != nil || client != clientID || revoked.Valid || expires <= stamp(at) {
		return Tokens{}, ErrGrant
	}
	if used.Valid {
		r.revoke(ctx, grant, at)
		return Tokens{}, ErrGrant
	}
	res, err := r.db.ExecContext(ctx, `UPDATE tokens SET used = ? WHERE hash = ? AND used IS NULL`, stamp(at), hash(refresh))
	if err != nil {
		return Tokens{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		r.revoke(ctx, grant, at)
		return Tokens{}, ErrGrant
	}
	return r.tokens(ctx, grant, at)
}

func (r *Registry) revoke(ctx context.Context, grant int64, at time.Time) {
	r.db.ExecContext(ctx, `UPDATE grants SET revoked = ? WHERE id = ? AND revoked IS NULL`, stamp(at), grant)
	r.db.ExecContext(ctx, `DELETE FROM tokens WHERE grant_id = ?`, grant)
}

// Access is what a valid access token stands for.
type Access struct {
	Email, Account string
	Grant          int64
	Assistant      string // the client's name and host, for the refinement log
	Expires        time.Time
}

// VerifyAccess checks an access token on /mcp.
func (r *Registry) VerifyAccess(ctx context.Context, token string, at time.Time) (Access, error) {
	var a Access
	var expires, name, host string
	err := r.db.QueryRowContext(ctx, `SELECT g.id, g.email, u.account, t.expires, c.name, g.host FROM tokens t
		JOIN grants g ON g.id = t.grant_id
		JOIN clients c ON c.client_id = g.client_id
		JOIN users u ON u.email = g.email
		WHERE t.hash = ? AND t.kind = 'access' AND g.revoked IS NULL AND t.expires > ?`, hash(token), stamp(at)).
		Scan(&a.Grant, &a.Email, &a.Account, &expires, &name, &host)
	if errors.Is(err, sql.ErrNoRows) {
		return Access{}, ErrUnknown
	}
	if err != nil {
		return Access{}, err
	}
	a.Expires, _ = time.Parse(time.RFC3339, expires)
	a.Assistant = name + " (" + host + ")"
	_, err = r.db.ExecContext(ctx, `UPDATE grants SET last_used = ? WHERE id = ?`, stamp(at), a.Grant)
	return a, err
}

// Connection is a connected assistant as the «Ассистенты» page lists it.
type Connection struct {
	ID       int64
	Name     string
	Host     string
	Email    string
	Created  time.Time
	LastUsed time.Time // zero when never used
}

// Connections lists an account's assistants that still have access.
func (r *Registry) Connections(ctx context.Context, accountID string) ([]Connection, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT g.id, c.name, g.host, g.email, g.created, COALESCE(g.last_used, '') FROM grants g
		JOIN clients c ON c.client_id = g.client_id
		JOIN users u ON u.email = g.email
		WHERE u.account = ? AND g.revoked IS NULL ORDER BY g.created DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		var c Connection
		var created, used string
		if err := rows.Scan(&c.ID, &c.Name, &c.Host, &c.Email, &created, &used); err != nil {
			return nil, err
		}
		c.Created, _ = time.Parse(time.RFC3339, created)
		c.LastUsed, _ = time.Parse(time.RFC3339, used)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Disconnect revokes an assistant's access, only within the account.
func (r *Registry) Disconnect(ctx context.Context, accountID string, id int64, at time.Time) error {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM grants g JOIN users u ON u.email = g.email
		WHERE g.id = ? AND u.account = ? AND g.revoked IS NULL`, id, accountID).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrUnknown
	}
	r.revoke(ctx, id, at)
	return nil
}
