package account

import (
	"errors"
	"strings"
	"testing"
)

func TestTokens(t *testing.T) {
	home := t.TempDir()
	alpha, _ := Create(home, "alpha")
	beta, _ := Create(home, "beta")
	ta, err := alpha.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := beta.NewToken()
	if a, err := Authenticate(home, ta); err != nil || a.ID != "alpha" {
		t.Fatalf("own token: %v %v", a, err)
	}
	if a, err := Authenticate(home, tb); err != nil || a.ID != "beta" {
		t.Fatalf("own token: %v %v", a, err)
	}
	// alpha's secret under beta's name must not open beta.
	forged := strings.Replace(ta, "lib_alpha_", "lib_beta_", 1)
	for name, tok := range map[string]string{
		"forged": forged, "empty": "", "no prefix": "alpha_x", "unknown account": "lib_gamma_abc",
		"path in id": "lib_../alpha_abc", "truncated": ta[:len(ta)-1],
	} {
		if _, err := Authenticate(home, tok); !errors.Is(err, ErrBadToken) {
			t.Errorf("%s: %v, want ErrBadToken", name, err)
		}
	}
	// A new token replaces the old one.
	ta2, _ := alpha.NewToken()
	if _, err := Authenticate(home, ta); !errors.Is(err, ErrBadToken) {
		t.Error("old token still works after rotation")
	}
	if _, err := Authenticate(home, ta2); err != nil {
		t.Errorf("new token: %v", err)
	}
}
