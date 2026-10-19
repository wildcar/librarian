package account

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A token is "lib_<account id>_<43 random characters>". The account id makes
// the lookup direct; only the sha256 of the whole token is stored, in the
// account's own directory, so a leaked data directory leaks no token.
const tokenPrefix = "lib_"

// ErrBadToken covers every way a token can fail; callers must not tell them
// apart to the client.
var ErrBadToken = errors.New("invalid token")

func (a *Account) tokenPath() string { return filepath.Join(a.root, "token.sha256") }

// NewToken issues a token for the account, replacing any previous one, and
// returns it. It is shown once and cannot be recovered.
func (a *Account) NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("new token: %w", err)
	}
	tok := tokenPrefix + a.ID + "_" + base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(tok))
	if err := os.WriteFile(a.tokenPath(), []byte(hex.EncodeToString(sum[:])+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("new token: %w", err)
	}
	return tok, nil
}

// Authenticate returns the account a token belongs to.
func Authenticate(home, token string) (*Account, error) {
	rest, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok {
		return nil, ErrBadToken
	}
	// Account ids never contain "_" (idPattern); the random part may.
	i := strings.IndexByte(rest, '_')
	if i <= 0 {
		return nil, ErrBadToken
	}
	a, err := Open(home, rest[:i])
	if err != nil {
		return nil, ErrBadToken
	}
	stored, err := os.ReadFile(a.tokenPath())
	if err != nil {
		return nil, ErrBadToken
	}
	want, err := hex.DecodeString(strings.TrimSpace(string(stored)))
	if err != nil || len(want) != sha256.Size {
		return nil, ErrBadToken
	}
	got := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		return nil, ErrBadToken
	}
	return a, nil
}
