package account

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestValidateID(t *testing.T) {
	for _, id := range []string{"ab", "family-1", "sergey"} {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) = %v, want nil", id, err)
		}
	}
	for _, id := range []string{"", "a", "..", "../x", "a/b", `a\b`, "Sergey", "-ab", "a.b", "семья"} {
		if err := ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) = nil, want error", id)
		}
	}
}

func TestCreateOpenIsolated(t *testing.T) {
	home := t.TempDir()
	a, err := Create(home, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Create(home, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if a.CatalogPath() == b.CatalogPath() {
		t.Fatal("two accounts share a catalog path")
	}
	if got, want := a.CatalogPath(), filepath.Join(home, "accounts", "alpha", "catalog.sqlite"); got != want {
		t.Errorf("CatalogPath = %s, want %s", got, want)
	}
	if _, err := Create(home, "alpha"); !errors.Is(err, ErrExists) {
		t.Errorf("second Create = %v, want ErrExists", err)
	}
	if _, err := Open(home, "gamma"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open unknown = %v, want ErrNotFound", err)
	}
	if _, err := Open(home, "../alpha"); err == nil {
		t.Error("Open with a path in the id succeeded")
	}
	if got, err := Open(home, "alpha"); err != nil || got.Root() != a.Root() {
		t.Errorf("Open(alpha) = %v, %v", got, err)
	}
}
