package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/store"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st)
}

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(h, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("garbage", "x") {
		t.Fatal("garbage hash accepted")
	}
}

func TestSetupLoginSession(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()

	if mode, _ := m.Mode(ctx); mode != ModeUnconfigured {
		t.Fatalf("mode = %q, want unconfigured", mode)
	}
	if _, err := m.Login(ctx, "nathan", "password12", "ip1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("login before setup: %v", err)
	}

	u, err := m.SetupPassword(ctx, "nathan", "password12")
	if err != nil {
		t.Fatal(err)
	}
	if mode, _ := m.Mode(ctx); mode != ModePassword {
		t.Fatalf("mode = %q, want password", mode)
	}
	if _, err := m.SetupPassword(ctx, "other", "password12"); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("second setup: %v", err)
	}

	if _, err := m.Login(ctx, "nathan", "wrong-password", "ip1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := m.Login(ctx, "nobody", "password12", "ip1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	got, err := m.Login(ctx, "nathan", "password12", "ip1")
	if err != nil || got.ID != u.ID {
		t.Fatalf("login: %v %+v", err, got)
	}

	token, err := m.CreateSession(ctx, u.ID, "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	su, err := m.ValidateSession(ctx, token)
	if err != nil || su.Username != "nathan" {
		t.Fatalf("validate: %v %+v", err, su)
	}
	if _, err := m.ValidateSession(ctx, "bogus"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("bogus token: %v", err)
	}
	if err := m.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ValidateSession(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("after logout: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	u, err := m.SetupPassword(ctx, "nathan", "password12")
	if err != nil {
		t.Fatal(err)
	}
	token, err := m.CreateSession(ctx, u.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Now().Add(SessionTTL + time.Hour) }
	if _, err := m.ValidateSession(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired session accepted: %v", err)
	}
}

func TestChangePasswordRevokesSessions(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	u, err := m.SetupPassword(ctx, "nathan", "password12")
	if err != nil {
		t.Fatal(err)
	}
	token, _ := m.CreateSession(ctx, u.ID, "")
	if err := m.ChangePassword(ctx, u.ID, "wrong", "newpassword12"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password: %v", err)
	}
	if err := m.ChangePassword(ctx, u.ID, "password12", "short"); !errors.Is(err, ErrValidation) {
		t.Fatalf("weak new password: %v", err)
	}
	if err := m.ChangePassword(ctx, u.ID, "password12", "newpassword12"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ValidateSession(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session survived password change: %v", err)
	}
	if _, err := m.Login(ctx, "nathan", "newpassword12", "ip1"); err != nil {
		t.Fatalf("login with new password: %v", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	if _, err := m.SetupPassword(ctx, "nathan", "password12"); err != nil {
		t.Fatal(err)
	}
	var last error
	for range 11 {
		_, last = m.Login(ctx, "nathan", "wrong-password", "attacker")
	}
	if !errors.Is(last, ErrRateLimited) {
		t.Fatalf("11th attempt: %v, want rate limited", last)
	}
	// A different client is unaffected.
	if _, err := m.Login(ctx, "nathan", "password12", "friendly"); err != nil {
		t.Fatalf("other client blocked: %v", err)
	}
}
