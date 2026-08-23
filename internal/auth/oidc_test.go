package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// fakeIDP is a minimal OIDC provider: discovery, JWKS and a token endpoint
// that issues RS256 ID tokens for whatever subject the test selects.
type fakeIDP struct {
	srv     *httptest.Server
	key     *rsa.PrivateKey
	subject string
	email   string
	// lastVerifier records the PKCE code_verifier the client sent.
	lastVerifier string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{key: key, subject: "user-123", email: "nathan@example.com"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/keys",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &f.key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.lastVerifier = r.PostFormValue("code_verifier")
		idt := f.signIDToken(t, map[string]any{
			"iss": f.srv.URL, "aud": "client-1", "sub": f.subject,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
			"email": f.email, "preferred_username": "nathan",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "id_token": idt,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIDP) signIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
		(&jose.SignerOptions{}).WithHeader("kid", "test"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sig.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func stateFrom(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state")
}

func TestOIDCSetupAndLogin(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	idp := newFakeIDP(t)

	// Configure against the fake provider (discovery must succeed).
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: "https://unreachable.invalid", ClientID: "client-1"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad issuer accepted: %v", err)
	}
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: idp.srv.URL, ClientID: "client-1", ClientSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	// Mode is still unconfigured until the pinning sign-in completes.
	if mode, _ := m.Mode(ctx); mode != ModeUnconfigured {
		t.Fatalf("mode = %q before pinning", mode)
	}

	// Login flow is refused before setup completes; setup flow proceeds.
	if _, err := m.StartOIDC(ctx, "http://app/cb", false); !errors.Is(err, ErrOIDCNotConfigured) {
		t.Fatalf("login before setup: %v", err)
	}
	authURL, err := m.StartOIDC(ctx, "http://app/cb", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authURL, "code_challenge=") || !strings.Contains(authURL, "code_challenge_method=S256") {
		t.Fatalf("no PKCE challenge in %s", authURL)
	}

	// Callback pins the subject, creates the user and flips the mode.
	u, err := m.CompleteOIDC(ctx, stateFrom(t, authURL), "code-1")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "nathan" {
		t.Fatalf("username = %q", u.Username)
	}
	if idp.lastVerifier == "" {
		t.Fatal("token endpoint did not receive a PKCE code_verifier")
	}
	if mode, _ := m.Mode(ctx); mode != ModeOIDC {
		t.Fatalf("mode = %q after pinning", mode)
	}

	// A second setup flow is refused.
	if _, err := m.StartOIDC(ctx, "http://app/cb", true); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("second setup: %v", err)
	}

	// Login flow with the pinned subject succeeds.
	authURL, err = m.StartOIDC(ctx, "http://app/cb", false)
	if err != nil {
		t.Fatal(err)
	}
	state := stateFrom(t, authURL)
	if _, err := m.CompleteOIDC(ctx, state, "code-2"); err != nil {
		t.Fatalf("login: %v", err)
	}
	// State is single-use.
	if _, err := m.CompleteOIDC(ctx, state, "code-2"); !errors.Is(err, ErrOIDCStateInvalid) {
		t.Fatalf("state reuse: %v", err)
	}

	// A different subject at the provider is rejected.
	idp.subject = "someone-else"
	authURL, _ = m.StartOIDC(ctx, "http://app/cb", false)
	if _, err := m.CompleteOIDC(ctx, stateFrom(t, authURL), "code-3"); !errors.Is(err, ErrOIDCWrongSubject) {
		t.Fatalf("wrong subject: %v", err)
	}

	// Password auth cannot be layered on top of a configured OIDC mode.
	if _, err := m.SetupPassword(ctx, "nathan", "password1234"); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("password setup after oidc: %v", err)
	}
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: idp.srv.URL, ClientID: "client-1"}); err != nil {
		t.Fatalf("reconfigure while in oidc mode should be allowed (key-gated at the API): %v", err)
	}
}

func TestOIDCFlowExpiry(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	idp := newFakeIDP(t)
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: idp.srv.URL, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	authURL, err := m.StartOIDC(ctx, "http://app/cb", true)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Now().Add(flowTTL + time.Minute) }
	if _, err := m.CompleteOIDC(ctx, stateFrom(t, authURL), "code"); !errors.Is(err, ErrOIDCStateInvalid) {
		t.Fatalf("expired flow: %v", err)
	}
}

func TestOIDCIssuerChangeResetsPinning(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	idp1 := newFakeIDP(t)
	idp2 := newFakeIDP(t)

	// Pin against provider 1.
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: idp1.srv.URL, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	authURL, _ := m.StartOIDC(ctx, "http://app/cb", true)
	u, err := m.CompleteOIDC(ctx, stateFrom(t, authURL), "code")
	if err != nil {
		t.Fatal(err)
	}
	token, _ := m.CreateSession(ctx, u.ID, "")

	// Same-issuer reconfiguration (e.g. secret rotation) keeps the pinning.
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: idp1.srv.URL, ClientID: "client-1", ClientSecret: "rotated"}); err != nil {
		t.Fatal(err)
	}
	if mode, _ := m.Mode(ctx); mode != ModeOIDC {
		t.Fatalf("mode after secret rotation = %q", mode)
	}

	// Changing the issuer resets pinning: mode drops to unconfigured, the
	// user and its sessions are gone, and subjects at the new provider don't
	// inherit access even when the sub string collides.
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: idp2.srv.URL, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	if mode, _ := m.Mode(ctx); mode != ModeUnconfigured {
		t.Fatalf("mode after issuer change = %q", mode)
	}
	if _, err := m.ValidateSession(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session survived issuer change: %v", err)
	}
	if _, err := m.StartOIDC(ctx, "http://app/cb", false); !errors.Is(err, ErrOIDCNotConfigured) {
		t.Fatalf("login flow available without re-pinning: %v", err)
	}

	// Re-pinning against provider 2 works.
	authURL, err = m.StartOIDC(ctx, "http://app/cb", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CompleteOIDC(ctx, stateFrom(t, authURL), "code"); err != nil {
		t.Fatal(err)
	}
	if mode, _ := m.Mode(ctx); mode != ModeOIDC {
		t.Fatalf("mode after re-pinning = %q", mode)
	}
}
