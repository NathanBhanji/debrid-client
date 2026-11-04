package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// NativeCallback is fixed; clients cannot select an arbitrary redirect destination.
const NativeCallback = "dev.bhanji.debrid://auth/callback"
const nativeGrantTTL = time.Minute

type nativeFlow struct{ challenge, state string }
type nativeGrant struct {
	user                        User
	issuer, clientID, challenge string
	expires                     time.Time
}

func validNativeRandom(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}
func validNativeVerifier(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		allowed := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("-._~", c)
		if !allowed {
			return false
		}
	}
	return true
}

// StartNativeOIDC preserves the existing registered server callback and adds a second,
// app-owned S256 binding. Only already-configured OIDC servers can start; never setup.
func (m *Manager) StartNativeOIDC(ctx context.Context, redirectURI, challenge, state, clientKey string) (string, error) {
	if !m.limiter.allow("native-start:"+clientKey, m.now()) {
		return "", ErrRateLimited
	}
	if !validNativeRandom(challenge) || !validNativeRandom(state) {
		return "", ErrValidation
	}
	return m.startOIDC(ctx, redirectURI, false, &nativeFlow{challenge: challenge, state: state})
}

// OIDCCallbackResult contains either a browser user or a native one-time grant redirect.
// No session credential is ever carried by a redirect URL.
type OIDCCallbackResult struct {
	User           User
	NativeRedirect string
}

func nativeRedirect(flow *nativeFlow, code string) string {
	values := url.Values{"state": {flow.state}}
	if code == "" {
		values.Set("error", "sign_in_failed")
	} else {
		values.Set("code", code)
	}
	return NativeCallback + "?" + values.Encode()
}

// FinishOIDC consumes a flow once, including provider denial/error. A native failure
// returns a generic app callback bound to its original client state.
func (m *Manager) FinishOIDC(ctx context.Context, state, code string, denied bool) (OIDCCallbackResult, error) {
	flow, err := m.takeOIDCFlow(state)
	if err != nil {
		return OIDCCallbackResult{}, err
	}
	result := OIDCCallbackResult{}
	if flow.native != nil {
		result.NativeRedirect = nativeRedirect(flow.native, "")
	}
	if denied || code == "" {
		return result, ErrOIDCStateInvalid
	}
	user, err := m.completeOIDCFlow(ctx, flow, code)
	if err != nil {
		return result, err
	}
	if flow.native == nil {
		result.User = user
		return result, nil
	}
	grantCode, err := randomToken()
	if err != nil {
		return result, err
	}
	m.oidcRT.mu.Lock()
	defer m.oidcRT.mu.Unlock()
	if m.oidcRT.grants == nil {
		m.oidcRT.grants = map[string]nativeGrant{}
	}
	now := m.now()
	for k, g := range m.oidcRT.grants {
		if !now.Before(g.expires) {
			delete(m.oidcRT.grants, k)
		}
	}
	// Refuse rather than grow the public-flow memory budget.
	if len(m.oidcRT.grants) >= maxLiveFlows {
		return result, ErrRateLimited
	}
	m.oidcRT.grants[hashToken(grantCode)] = nativeGrant{user: user, issuer: flow.issuer, clientID: flow.clientID,
		challenge: flow.native.challenge, expires: now.Add(nativeGrantTTL)}
	result.NativeRedirect = nativeRedirect(flow.native, grantCode)
	return result, nil
}

// ExchangeNativeGrant consumes even an incorrect verifier attempt. Grants are hashed,
// short-lived, in-memory, issuer/client/user-bound and never grant access by themselves.
func (m *Manager) ExchangeNativeGrant(ctx context.Context, code, verifier, clientKey, userAgent string) (string, User, error) {
	if !m.limiter.allow("native-exchange:"+clientKey, m.now()) {
		return "", User{}, ErrRateLimited
	}
	if !validNativeRandom(code) {
		return "", User{}, ErrInvalidCredentials
	}
	m.oidcRT.mu.Lock()
	grant, ok := m.oidcRT.grants[hashToken(code)]
	delete(m.oidcRT.grants, hashToken(code))
	m.oidcRT.mu.Unlock()
	if !ok || !m.now().Before(grant.expires) || !validNativeVerifier(verifier) ||
		subtle.ConstantTimeCompare([]byte(oauth2.S256ChallengeFromVerifier(verifier)), []byte(grant.challenge)) != 1 {
		return "", User{}, ErrInvalidCredentials
	}
	// Binding and insertion share one transaction, including same-issuer client changes.
	token, err := m.createSession(ctx, grant.user.ID, userAgent, &oidcBinding{issuer: grant.issuer, clientID: grant.clientID})
	if errors.Is(err, ErrOIDCStateInvalid) {
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	return token, grant.user, nil
}
