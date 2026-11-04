package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func nativeFixture(t *testing.T) (*Manager, *fakeIDP) {
	t.Helper()
	m, idp := newManager(t), newFakeIDP(t)
	ctx := context.Background()
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: idp.srv.URL, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	start, err := m.StartOIDC(ctx, "https://server.example.test/api/v1/auth/oidc/callback", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CompleteOIDC(ctx, stateFrom(t, start), "setup-code"); err != nil {
		t.Fatal(err)
	}
	return m, idp
}
func startNative(t *testing.T, m *Manager) (state, verifier, clientState string) {
	t.Helper()
	verifier = oauth2.GenerateVerifier()
	clientState, _ = randomToken()
	start, err := m.StartNativeOIDC(context.Background(), "https://server.example.test/api/v1/auth/oidc/callback", oauth2.S256ChallengeFromVerifier(verifier), clientState, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	return stateFrom(t, start), verifier, clientState
}
func grantNative(t *testing.T, m *Manager) (code, verifier string) {
	t.Helper()
	state, verifier, clientState := startNative(t, m)
	result, err := m.FinishOIDC(context.Background(), state, "provider-code", false)
	if err != nil {
		t.Fatal(err)
	}
	callback, err := url.Parse(result.NativeRedirect)
	if err != nil {
		t.Fatal(err)
	}
	if callback.Scheme != "dev.bhanji.debrid" || callback.Host != "auth" || callback.Path != "/callback" || callback.Query().Get("state") != clientState || result.User.ID != "" {
		t.Fatal("invalid callback contract")
	}
	code = callback.Query().Get("code")
	if !validNativeRandom(code) || len(callback.Query()) != 2 {
		t.Fatal("unexpected callback payload")
	}
	return code, verifier
}

func TestNativeGrantSessionAndReplay(t *testing.T) {
	m, _ := nativeFixture(t)
	ctx := context.Background()
	code, verifier := grantNative(t, m)
	if _, err := m.ValidateSession(ctx, code); !errors.Is(err, ErrNoSession) {
		t.Fatal("grant itself authenticated")
	}
	token, user, err := m.ExchangeNativeGrant(ctx, code, verifier, "fixture", "native-test")
	if err != nil || user.Username != "nathan" || token == "" {
		t.Fatalf("exchange: %v", err)
	}
	if got, err := m.ValidateSession(ctx, token); err != nil || got.ID != user.ID {
		t.Fatalf("session: %v", err)
	}
	if _, _, err := m.ExchangeNativeGrant(ctx, code, verifier, "fixture", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("grant replay accepted")
	}
}
func TestNativeWrongVerifierExpiryAndConcurrentConsumption(t *testing.T) {
	m, _ := nativeFixture(t)
	ctx := context.Background()
	code, verifier := grantNative(t, m)
	if _, _, err := m.ExchangeNativeGrant(ctx, code, oauth2.GenerateVerifier(), "wrong", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("wrong verifier accepted")
	}
	if _, _, err := m.ExchangeNativeGrant(ctx, code, verifier, "replay", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("wrong attempt did not consume")
	}
	code, verifier = grantNative(t, m)
	now := time.Now()
	m.now = func() time.Time { return now.Add(nativeGrantTTL + time.Second) }
	if _, _, err := m.ExchangeNativeGrant(ctx, code, verifier, "expired", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("expired grant accepted")
	}
	m.now = time.Now
	code, verifier = grantNative(t, m)
	var successes atomic.Int32
	var group sync.WaitGroup
	for range 5 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, _, err := m.ExchangeNativeGrant(ctx, code, verifier, "race", ""); err == nil {
				successes.Add(1)
			}
		}()
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent successes: %d", successes.Load())
	}
}
func TestNativeDenialWrongSubjectAndBrowserIsolation(t *testing.T) {
	m, idp := nativeFixture(t)
	ctx := context.Background()
	state, _, clientState := startNative(t, m)
	result, err := m.FinishOIDC(ctx, state, "", true)
	if err == nil || !strings.Contains(result.NativeRedirect, "error=sign_in_failed") || !strings.Contains(result.NativeRedirect, clientState) {
		t.Fatal("denial not safely returned")
	}
	if _, err := m.FinishOIDC(ctx, state, "provider-code", false); !errors.Is(err, ErrOIDCStateInvalid) {
		t.Fatal("denied flow replayed")
	}
	state, _, _ = startNative(t, m)
	if _, err := m.CompleteOIDC(ctx, state, "provider-code"); !errors.Is(err, ErrOIDCStateInvalid) {
		t.Fatal("native flow accepted as browser")
	}
	idp.subject = "other-user"
	state, _, _ = startNative(t, m)
	result, err = m.FinishOIDC(ctx, state, "provider-code", false)
	if !errors.Is(err, ErrOIDCWrongSubject) || !strings.Contains(result.NativeRedirect, "error=sign_in_failed") {
		t.Fatal("wrong subject accepted")
	}
}
func TestNativeIssuerAndClientBinding(t *testing.T) {
	for _, changeIssuer := range []bool{true, false} {
		t.Run(map[bool]string{true: "issuer", false: "client"}[changeIssuer], func(t *testing.T) {
			m, idp := nativeFixture(t)
			ctx := context.Background()
			state, _, _ := startNative(t, m)
			code, verifier := grantNative(t, m)
			cfg := OIDCConfig{Issuer: idp.srv.URL, ClientID: "replacement-client"}
			if changeIssuer {
				cfg = OIDCConfig{Issuer: newFakeIDP(t).srv.URL, ClientID: "client-1"}
			}
			if err := m.ConfigureOIDC(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := m.FinishOIDC(ctx, state, "provider-code", false); !errors.Is(err, ErrOIDCStateInvalid) {
				t.Fatalf("changed config flow: %v", err)
			}
			if _, _, err := m.ExchangeNativeGrant(ctx, code, verifier, "changed", ""); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("changed config grant: %v", err)
			}
		})
	}
}
func TestNativeValidationSetupRefusalAndLimits(t *testing.T) {
	m := newManager(t)
	verifier, state := oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
	challenge := oauth2.S256ChallengeFromVerifier(verifier)
	if _, err := m.StartNativeOIDC(context.Background(), "https://server/cb", challenge, state, "unconfigured"); !errors.Is(err, ErrOIDCNotConfigured) {
		t.Fatal("native setup allowed")
	}
	m, _ = nativeFixture(t)
	for _, bad := range []string{"", strings.Repeat("a", 42), strings.Repeat("a", 44), strings.Repeat("!", 43)} {
		if _, err := m.StartNativeOIDC(context.Background(), "https://server/cb", bad, state, "bad"); !errors.Is(err, ErrValidation) {
			t.Fatalf("bad challenge accepted: %v", err)
		}
	}
	for range 11 {
		_, _ = m.StartNativeOIDC(context.Background(), "https://server/cb", challenge, state, "limited")
	}
	if _, err := m.StartNativeOIDC(context.Background(), "https://server/cb", challenge, state, "limited"); !errors.Is(err, ErrRateLimited) {
		t.Fatal("start not limited")
	}
	m.oidcRT.mu.Lock()
	if len(m.oidcRT.flows) > maxLiveFlows {
		t.Fatal("unbounded flow map")
	}
	m.oidcRT.mu.Unlock()
}

func TestOIDCSetupConfigurationChangeDuringExchangeCannotPinOldProvider(t *testing.T) {
	m, idp := newManager(t), newFakeIDP(t)
	ctx := context.Background()
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: idp.srv.URL, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	start, err := m.StartOIDC(ctx, "https://server/cb", true)
	if err != nil {
		t.Fatal(err)
	}
	idp.tokenEntered, idp.tokenResume = make(chan struct{}, 1), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(idp.tokenResume) })
	done := make(chan error, 1)
	go func() { _, err := m.CompleteOIDC(ctx, stateFrom(t, start), "setup-code"); done <- err }()
	select {
	case <-idp.tokenEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("exchange did not start")
	}
	replacement := newFakeIDP(t)
	if err := m.ConfigureOIDC(ctx, OIDCConfig{Issuer: replacement.srv.URL, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(idp.tokenResume) })
	select {
	case err := <-done:
		if !errors.Is(err, ErrOIDCStateInvalid) {
			t.Fatalf("obsolete setup: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exchange did not finish")
	}
	count, err := m.store.CountUsers(ctx)
	if err != nil || count != 0 {
		t.Fatal("obsolete provider pinned a user")
	}
}

func TestNativeConfigurationChangeAtSessionIssuanceIsRejected(t *testing.T) {
	m, idp := nativeFixture(t)
	code, verifier := grantNative(t, m)
	// The injected clock is a deterministic barrier immediately before session persistence.
	// This catches a binding check placed before CreateSession instead of inside its transaction.
	now := time.Now()
	entered, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var release sync.Once
	defer release.Do(func() { close(resume) })
	m.now = func() time.Time {
		if calls.Add(1) == 3 {
			close(entered)
			<-resume
		}
		return now
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := m.ExchangeNativeGrant(context.Background(), code, verifier, "race-config", "")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("session issuance did not start")
	}
	if err := m.ConfigureOIDC(context.Background(), OIDCConfig{Issuer: idp.srv.URL, ClientID: "replacement-client"}); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(resume) })
	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("obsolete grant: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("session issuance did not finish")
	}
	var count int
	if err := m.store.DB().QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); err != nil || count != 0 {
		t.Fatal("obsolete grant created a session")
	}
}

func TestNativeFlowAndGrantMemoryBounds(t *testing.T) {
	m, _ := nativeFixture(t)
	ctx := context.Background()
	for i := 0; i < maxLiveFlows+3; i++ {
		if _, err := m.StartNativeOIDC(ctx, "https://server/cb", oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier()), oauth2.GenerateVerifier(), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	m.oidcRT.mu.Lock()
	count := len(m.oidcRT.flows)
	m.oidcRT.grants = make(map[string]nativeGrant, maxLiveFlows)
	for i := 0; i < maxLiveFlows; i++ {
		m.oidcRT.grants[fmt.Sprint(i)] = nativeGrant{expires: time.Now().Add(time.Minute)}
	}
	m.oidcRT.mu.Unlock()
	if count != maxLiveFlows {
		t.Fatalf("flow budget: %d", count)
	}
	state, _, _ := startNative(t, m)
	result, err := m.FinishOIDC(ctx, state, "provider-code", false)
	if !errors.Is(err, ErrRateLimited) || !strings.Contains(result.NativeRedirect, "error=sign_in_failed") {
		t.Fatalf("grant cap: %v", err)
	}
}
