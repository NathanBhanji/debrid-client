package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

// Settings keys for the OIDC client configuration.
const (
	oidcIssuerKey       = "auth.oidc.issuer"
	oidcClientIDKey     = "auth.oidc.client_id"
	oidcClientSecretKey = "auth.oidc.client_secret"

	// flowTTL bounds how long a started authorization flow stays valid.
	flowTTL = 10 * time.Minute
	// maxLiveFlows caps in-memory pending flows (the start endpoint is public).
	maxLiveFlows = 128
)

// OIDC-specific errors.
var (
	ErrOIDCNotConfigured = errors.New("OIDC is not configured")
	ErrOIDCStateInvalid  = errors.New("unknown or expired OIDC state; restart the sign-in")
	ErrOIDCWrongSubject  = errors.New("this identity is not authorized for this server")
)

// OIDCConfig is the stored client configuration.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}

// oidcFlow is one in-progress authorization (state → verifier). Flows are
// in-memory: they live for seconds and a restart just means re-starting the
// sign-in.
type oidcFlow struct {
	verifier    string
	redirectURI string
	setup       bool
	expires     time.Time
	issuer      string
	clientID    string
	native      *nativeFlow
}

type oidcRuntime struct {
	mu       sync.Mutex
	provider *oidc.Provider
	issuer   string
	flows    map[string]oidcFlow
	grants   map[string]nativeGrant
}

// oidcConfig loads the stored client configuration.
func (m *Manager) oidcConfig(ctx context.Context) (OIDCConfig, error) {
	var cfg OIDCConfig
	var err error
	if cfg.Issuer, err = m.getSetting(ctx, oidcIssuerKey); err != nil {
		return cfg, err
	}
	if cfg.ClientID, err = m.getSetting(ctx, oidcClientIDKey); err != nil {
		return cfg, err
	}
	if cfg.ClientSecret, err = m.getSetting(ctx, oidcClientSecretKey); err != nil {
		return cfg, err
	}
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return cfg, ErrOIDCNotConfigured
	}
	return cfg, nil
}

func (m *Manager) getSetting(ctx context.Context, key string) (string, error) {
	v, err := m.store.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// OIDCIssuer returns the configured issuer URL ("" when unset). Safe to show
// to unauthenticated clients (the login page displays it).
func (m *Manager) OIDCIssuer(ctx context.Context) (string, error) {
	return m.getSetting(ctx, oidcIssuerKey)
}

// ConfigureOIDC validates the client configuration against the provider's
// discovery document and stores it. It does not switch the auth mode — the
// mode flips to oidc only when the first sign-in pins a subject, so a broken
// configuration can be retried with the API key.
//
// Changing the issuer while OIDC is active resets pinning entirely (user,
// sessions and mode): OIDC subjects are only unique per issuer, so a pinned
// sub must never survive an issuer change.
func (m *Manager) ConfigureOIDC(ctx context.Context, cfg OIDCConfig) error {
	cfg.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	if !strings.HasPrefix(cfg.Issuer, "https://") && !strings.HasPrefix(cfg.Issuer, "http://") {
		return fmt.Errorf("%w: issuer must be an http(s) URL", ErrValidation)
	}
	if cfg.ClientID == "" {
		return fmt.Errorf("%w: client_id is required", ErrValidation)
	}
	if _, err := oidc.NewProvider(ctx, cfg.Issuer); err != nil {
		return fmt.Errorf("provider discovery failed: %w: %w", ErrValidation, err)
	}
	now := store.FormatTime(m.now())
	return m.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		mode, err := q.GetSetting(ctx, modeKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if Mode(mode) == ModePassword {
			return fmt.Errorf("%w: password auth is already configured", ErrAlreadyConfigured)
		}
		oldIssuer, err := q.GetSetting(ctx, oidcIssuerKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if Mode(mode) == ModeOIDC && oldIssuer != cfg.Issuer {
			if err := q.DeleteAllUsers(ctx); err != nil { // sessions cascade
				return err
			}
			if err := q.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{
				Key: modeKey, Value: string(ModeUnconfigured), UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		for k, v := range map[string]string{
			oidcIssuerKey: cfg.Issuer, oidcClientIDKey: cfg.ClientID, oidcClientSecretKey: cfg.ClientSecret,
		} {
			if err := q.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{Key: k, Value: v, UpdatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
}

// provider returns (caching) the discovered OIDC provider for the configured
// issuer.
func (m *Manager) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	m.oidcRT.mu.Lock()
	defer m.oidcRT.mu.Unlock()
	if m.oidcRT.provider != nil && m.oidcRT.issuer == issuer {
		return m.oidcRT.provider, nil
	}
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	m.oidcRT.provider = p
	m.oidcRT.issuer = issuer
	return p, nil
}

// StartOIDC begins an authorization-code + PKCE flow and returns the provider
// URL to redirect the browser to. redirectURI must be this server's
// /api/v1/auth/oidc/callback as seen by the browser. setup marks a first-run
// flow (caller must have proven ownership with the API key); it is refused
// once auth is configured, and non-setup flows are refused until it is.
func (m *Manager) StartOIDC(ctx context.Context, redirectURI string, setup bool) (string, error) {
	return m.startOIDC(ctx, redirectURI, setup, nil)
}

func (m *Manager) startOIDC(ctx context.Context, redirectURI string, setup bool, native *nativeFlow) (string, error) {
	mode, err := m.Mode(ctx)
	if err != nil {
		return "", err
	}
	if setup && mode != ModeUnconfigured {
		return "", ErrAlreadyConfigured
	}
	if !setup && mode != ModeOIDC {
		return "", ErrOIDCNotConfigured
	}
	cfg, err := m.oidcConfig(ctx)
	if err != nil {
		return "", err
	}
	p, err := m.provider(ctx, cfg.Issuer)
	if err != nil {
		return "", err
	}
	state, err := randomToken()
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()

	m.oidcRT.mu.Lock()
	now := m.now()
	for k, f := range m.oidcRT.flows {
		if now.After(f.expires) {
			delete(m.oidcRT.flows, k)
		}
	}
	// /oidc/start is public, so cap live flows to bound memory: beyond the
	// cap, evict the flow closest to expiry (oldest start).
	for len(m.oidcRT.flows) >= maxLiveFlows {
		var oldestKey string
		var oldest time.Time
		for k, f := range m.oidcRT.flows {
			if oldestKey == "" || f.expires.Before(oldest) {
				oldestKey, oldest = k, f.expires
			}
		}
		delete(m.oidcRT.flows, oldestKey)
	}
	m.oidcRT.flows[state] = oidcFlow{
		verifier: verifier, redirectURI: redirectURI, setup: setup, expires: now.Add(flowTTL),
		issuer: cfg.Issuer, clientID: cfg.ClientID, native: native,
	}
	m.oidcRT.mu.Unlock()

	oc := oauth2.Config{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
		Endpoint: p.Endpoint(), RedirectURL: redirectURI,
		Scopes: []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return oc.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), nil
}

// CompleteOIDC handles the provider callback: it exchanges the code, verifies
// the ID token, and either finishes first-run setup (creating the user and
// pinning the subject) or logs the pinned subject in. Returns the user.
func (m *Manager) CompleteOIDC(ctx context.Context, state, code string) (User, error) {
	flow, err := m.takeOIDCFlow(state)
	if err != nil {
		return User{}, err
	}
	// Browser callers cannot turn a native authorization into a cookie session.
	if flow.native != nil {
		return User{}, ErrOIDCStateInvalid
	}
	return m.completeOIDCFlow(ctx, flow, code)
}

func (m *Manager) takeOIDCFlow(state string) (oidcFlow, error) {
	m.oidcRT.mu.Lock()
	flow, ok := m.oidcRT.flows[state]
	delete(m.oidcRT.flows, state)
	m.oidcRT.mu.Unlock()
	if !ok || !m.now().Before(flow.expires) {
		return oidcFlow{}, ErrOIDCStateInvalid
	}
	return flow, nil
}

func (m *Manager) completeOIDCFlow(ctx context.Context, flow oidcFlow, code string) (User, error) {
	cfg, err := m.oidcConfig(ctx)
	if err != nil {
		return User{}, err
	}
	if cfg.Issuer != flow.issuer || cfg.ClientID != flow.clientID {
		return User{}, ErrOIDCStateInvalid
	}
	p, err := m.provider(ctx, cfg.Issuer)
	if err != nil {
		return User{}, err
	}
	oc := oauth2.Config{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
		Endpoint: p.Endpoint(), RedirectURL: flow.redirectURI,
	}
	tok, err := oc.Exchange(ctx, code, oauth2.VerifierOption(flow.verifier))
	if err != nil {
		return User{}, fmt.Errorf("code exchange failed: %w", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return User{}, errors.New("provider returned no id_token")
	}
	idt, err := p.VerifierContext(ctx, &oidc.Config{ClientID: cfg.ClientID}).Verify(ctx, rawID)
	if err != nil {
		return User{}, fmt.Errorf("id_token verification failed: %w", err)
	}
	var claims struct {
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
	}
	_ = idt.Claims(&claims)

	// No nonce: the single-use server-side state plus PKCE (S256) already
	// binds the callback to this flow for a confidential client, which covers
	// the code-injection/replay attacks nonce defends against.
	if flow.setup {
		username := firstNonEmpty(claims.PreferredUsername, claims.Email, claims.Name, "user")
		if len(username) > 64 {
			username = username[:64]
		}
		return m.createSoleUser(ctx, ModeOIDC, username, "", idt.Subject, claims.Email, &oidcBinding{issuer: flow.issuer, clientID: flow.clientID})
	}
	// Recheck configuration and subject in the same transaction after the remote exchange.
	// An issuer change while the browser is open must never authenticate its replacement user.
	var result User
	err = m.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		if err := checkOIDCBinding(ctx, q, oidcBinding{issuer: flow.issuer, clientID: flow.clientID}, ModeOIDC); err != nil {
			return err
		}
		u, err := q.GetUserByOIDCSubject(ctx, idt.Subject)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrOIDCWrongSubject
		}
		if err != nil {
			return err
		}
		result = User{ID: u.ID, Username: u.Username}
		return nil
	})
	return result, err
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// checkOIDCBinding must run using the same transaction as the user/session write.
type oidcBinding struct{ issuer, clientID string }

func checkOIDCBinding(ctx context.Context, q *sqlcgen.Queries, binding oidcBinding, expectedMode Mode) error {
	mode, err := q.GetSetting(ctx, modeKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	issuer, err := q.GetSetting(ctx, oidcIssuerKey)
	if err != nil {
		return err
	}
	clientID, err := q.GetSetting(ctx, oidcClientIDKey)
	if err != nil {
		return err
	}
	if Mode(mode) != expectedMode || issuer != binding.issuer || clientID != binding.clientID {
		return ErrOIDCStateInvalid
	}
	return nil
}
