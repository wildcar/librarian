package account_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wildcar/librarian/internal/account"
)

func TestRoots(t *testing.T) {
	a, err := account.Create(t.TempDir(), "acc")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	docs := filepath.Join(base, "docs")
	secret := filepath.Join(base, "docs-secret")
	for _, d := range []string{filepath.Join(docs, "2026"), secret} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.Symlink(secret, filepath.Join(docs, "link"))
	if _, err := a.AddRoot(filepath.Join(base, "missing")); err == nil {
		t.Error("missing folder allowed")
	}
	if _, err := a.AddRoot(docs); err != nil {
		t.Fatal(err)
	}
	a.AddRoot(docs) // twice is once
	if rs, _ := a.Roots(); len(rs) != 1 {
		t.Errorf("roots: %v", rs)
	}
	for path, ok := range map[string]bool{
		docs:                                    true,
		filepath.Join(docs, "2026"):             true,
		filepath.Join(docs, "2026", "..", ".."): false,
		secret:                                  false, // a sibling with the same prefix
		filepath.Join(docs, "link"):             false, // a link out of the root
		"docs/2026":                             false,
		"/":                                     false,
	} {
		_, _, err := a.Within(path)
		if (err == nil) != ok || (err != nil && !errors.Is(err, account.ErrOutside)) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if err := a.RemoveRoot(docs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Within(docs); err == nil {
		t.Error("removed root still allowed")
	}
}
