package users_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/users"
)

func open(t *testing.T) (*users.Registry, string) {
	t.Helper()
	home := t.TempDir()
	if _, err := account.Create(home, "acc"); err != nil {
		t.Fatal(err)
	}
	r, err := users.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, home
}

func TestLoginByLetter(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	r, _ := open(t)
	if err := r.SetEmail(ctx, "Owner@Example.org", "acc", now); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEmail(ctx, "x@example.org", "nope", now); err == nil {
		t.Error("e-mail bound to a missing account")
	}
	if _, err := r.StartLogin(ctx, "stranger@example.org", users.Request{Purpose: users.PurposeWeb}, now); !errors.Is(err, users.ErrUnknown) {
		t.Errorf("stranger: %v", err)
	}

	l, err := r.StartLogin(ctx, "owner@example.org", users.Request{Purpose: users.PurposeWeb, IP: "1.2.3.4"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if st, _, _ := r.Take(ctx, l.Wait, now); st != users.Waiting {
		t.Errorf("before the button: %v", st)
	}
	p, err := r.Peek(ctx, l.Link, now)
	if err != nil || p.Email != "owner@example.org" || p.IP != "1.2.3.4" {
		t.Errorf("peek: %+v %v", p, err)
	}
	if _, err := r.Peek(ctx, l.Link, now); err != nil {
		t.Error("peeking used the link")
	}
	if p, err := r.Confirm(ctx, l.Link, now); err != nil || p.Email != "owner@example.org" {
		t.Fatalf("confirm: %+v %v", p, err)
	}
	if _, err := r.Confirm(ctx, l.Link, now); err == nil {
		t.Error("link used twice")
	}
	if st, tk, _ := r.Take(ctx, l.Wait, now); st != users.Confirmed || tk.Email != "owner@example.org" || tk.Auth != nil {
		t.Errorf("take: %v %+v", st, tk)
	}
	if st, _, _ := r.Take(ctx, l.Wait, now); st != users.Expired {
		t.Errorf("taken twice: %v", st)
	}

	// Expiry.
	l2, _ := r.StartLogin(ctx, "owner@example.org", users.Request{Purpose: users.PurposeWeb}, now)
	late := now.Add(users.LinkTTL + time.Second)
	if _, err := r.Confirm(ctx, l2.Link, late); err == nil {
		t.Error("expired link confirmed")
	}
	if st, _, _ := r.Take(ctx, l2.Wait, late); st != users.Expired {
		t.Errorf("expired wait: %v", st)
	}

	// Rate limit: five letters an hour.
	for i := 0; i < 3; i++ {
		if _, err := r.StartLogin(ctx, "owner@example.org", users.Request{Purpose: users.PurposeWeb}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.StartLogin(ctx, "owner@example.org", users.Request{Purpose: users.PurposeWeb}, now); !errors.Is(err, users.ErrTooMany) {
		t.Errorf("sixth letter: %v", err)
	}
}

func TestSessions(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	r, _ := open(t)
	if err := r.SetEmail(ctx, "owner@example.org", "acc", now); err != nil {
		t.Fatal(err)
	}
	s, err := r.NewSession(ctx, "owner@example.org", now)
	if err != nil {
		t.Fatal(err)
	}
	if email, err := r.Session(ctx, s, now.Add(time.Hour)); err != nil || email != "owner@example.org" {
		t.Errorf("session: %q %v", email, err)
	}
	if a, err := r.Account(ctx, "owner@example.org"); err != nil || a.ID != "acc" {
		t.Errorf("account: %v %v", a, err)
	}
	if _, err := r.Session(ctx, s, now.Add(users.SessionTTL+2*time.Hour)); err == nil {
		t.Error("session outlived its time")
	}
	s2, _ := r.NewSession(ctx, "owner@example.org", now)
	l, _ := r.StartLogin(ctx, "owner@example.org", users.Request{Purpose: users.PurposeWeb}, now)
	if err := r.RemoveEmail(ctx, "owner@example.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Confirm(ctx, l.Link, now); err == nil {
		t.Error("link of a removed e-mail confirmed")
	}
	if st, _, _ := r.Take(ctx, l.Wait, now); st != users.Expired {
		t.Errorf("wait of a removed e-mail: %v", st)
	}
	if _, err := r.Session(ctx, s2, now); err == nil {
		t.Error("session of a removed e-mail")
	}
	if _, err := r.Session(ctx, "forged", now); err == nil {
		t.Error("forged session")
	}
}
