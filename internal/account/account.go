// Package account owns the per-account storage root.
//
// An account is the unit of isolation (ADR-0004): every file the service writes
// for an account lives under <home>/accounts/<id>/, and nothing outside this
// package builds such paths. Code that holds an *Account can reach only that
// account's data.
package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// idPattern keeps account ids safe as directory names on every OS: no path
// separators, no dots, no case-folding surprises.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)

// ErrNotFound is returned by Open for an account that was never created.
var ErrNotFound = errors.New("account not found")

// ErrExists is returned by Create for an account that already exists.
var ErrExists = errors.New("account already exists")

// Account is an opened account storage root.
type Account struct {
	ID   string
	root string
}

// ValidateID reports whether id can name an account.
func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid account id %q: use 2-40 characters a-z, 0-9 and '-', starting with a letter or digit", id)
	}
	return nil
}

func dir(home, id string) string {
	return filepath.Join(home, "accounts", id)
}

// Create makes a new, empty account under home.
func Create(home, id string) (*Account, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	d := dir(home, id)
	if _, err := os.Stat(d); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, id)
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return nil, fmt.Errorf("create account %s: %w", id, err)
	}
	return &Account{ID: id, root: d}, nil
}

// Open opens an existing account under home.
func Open(home, id string) (*Account, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	d := dir(home, id)
	st, err := os.Stat(d)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("open account %s: %w", id, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("open account %s: %s is not a directory", id, d)
	}
	return &Account{ID: id, root: d}, nil
}

// CatalogPath is the account's SQLite catalog.
func (a *Account) CatalogPath() string {
	return filepath.Join(a.root, "catalog.sqlite")
}

// Root is the account's storage directory.
func (a *Account) Root() string {
	return a.root
}
