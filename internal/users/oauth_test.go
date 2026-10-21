package users_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/users"
)

func challenge(verifier string) string {
	s := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

func TestOAuth(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	r, _ := open(t)
	if err := r.SetEmail(ctx, "owner@example.org", "acc", now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"http://evil.example/cb", "ftp://x/cb", "/cb", "https://x/cb#f"} {
		if _, err := r.RegisterClient(ctx, "X", []string{bad}, false, now); err == nil {
			t.Errorf("redirect %q accepted", bad)
		}
	}
	c, err := r.RegisterClient(ctx, "Claude", []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:3000/cb"}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = r.Client(ctx, c.ID)
	if !c.AllowsRedirect("http://localhost:51234/cb") || c.AllowsRedirect("https://claude.ai/other") || c.AllowsRedirect("http://localhost:1/other") {
		t.Error("redirect matching")
	}

	verifier := "a-long-verifier-of-the-owner-0123456789-abcdefgh"
	req := users.AuthRequest{ClientID: c.ID, RedirectURI: "https://claude.ai/api/mcp/auth_callback", Challenge: challenge(verifier), State: "s"}
	code, err := r.IssueCode(ctx, "owner@example.org", req, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Exchange(ctx, code, c.ID, "", req.RedirectURI, "wrong verifier", now); err == nil {
		t.Error("wrong verifier accepted")
	}
	tk, err := r.Exchange(ctx, code, c.ID, "", req.RedirectURI, verifier, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.VerifyAccess(ctx, tk.Access, now)
	if err != nil || a.Account != "acc" || a.Assistant != "Claude (claude.ai)" {
		t.Fatalf("access: %+v %v", a, err)
	}
	if _, err := r.VerifyAccess(ctx, tk.Access, now.Add(users.AccessTTL+time.Second)); err == nil {
		t.Error("expired access token")
	}
	// Rotation, then reuse of the old refresh token revokes everything.
	tk2, err := r.Refresh(ctx, tk.Refresh, c.ID, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.VerifyAccess(ctx, tk2.Access, now); err != nil {
		t.Errorf("rotated access: %v", err)
	}
	if _, err := r.Refresh(ctx, tk.Refresh, c.ID, "", now); err == nil {
		t.Error("refresh token used twice")
	}
	if _, err := r.VerifyAccess(ctx, tk2.Access, now); err == nil {
		t.Error("connection survived a reused refresh token")
	}

	// A code works once; its second use revokes what it gave.
	code, _ = r.IssueCode(ctx, "owner@example.org", req, now)
	tk3, err := r.Exchange(ctx, code, c.ID, "", req.RedirectURI, verifier, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Exchange(ctx, code, c.ID, "", req.RedirectURI, verifier, now); err == nil {
		t.Error("code used twice")
	}
	if _, err := r.VerifyAccess(ctx, tk3.Access, now); err == nil {
		t.Error("connection survived a reused code")
	}

	// The «Ассистенты» page: list and disconnect, only within the account.
	code, _ = r.IssueCode(ctx, "owner@example.org", req, now)
	tk4, _ := r.Exchange(ctx, code, c.ID, "", req.RedirectURI, verifier, now)
	r.VerifyAccess(ctx, tk4.Access, now)
	cs, err := r.Connections(ctx, "acc")
	if err != nil || len(cs) != 1 || cs[0].Name != "Claude" || cs[0].LastUsed.IsZero() {
		t.Fatalf("connections: %+v %v", cs, err)
	}
	if err := r.Disconnect(ctx, "other", cs[0].ID, now); err == nil {
		t.Error("disconnected from another account")
	}
	if err := r.Disconnect(ctx, "acc", cs[0].ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.VerifyAccess(ctx, tk4.Access, now); err == nil {
		t.Error("disconnected assistant still in")
	}
	if _, err := r.Refresh(ctx, tk4.Refresh, c.ID, "", now); err == nil {
		t.Error("disconnected assistant refreshed")
	}

	// A confidential client must present its secret.
	cc, _ := r.RegisterClient(ctx, "ChatGPT", []string{"https://chatgpt.com/cb"}, true, now)
	creq := users.AuthRequest{ClientID: cc.ID, RedirectURI: "https://chatgpt.com/cb", Challenge: challenge(verifier)}
	code, _ = r.IssueCode(ctx, "owner@example.org", creq, now)
	if _, err := r.Exchange(ctx, code, cc.ID, "wrong", creq.RedirectURI, verifier, now); err == nil {
		t.Error("confidential client without its secret")
	}
	code, _ = r.IssueCode(ctx, "owner@example.org", creq, now)
	if _, err := r.Exchange(ctx, code, cc.ID, cc.Secret, creq.RedirectURI, verifier, now); err != nil {
		t.Errorf("confidential client: %v", err)
	}
}
