// Package auth manages web UI authentication: a single local user with a
// password (argon2id) or an OIDC identity, and cookie sessions backed by the
// store. The Bearer API key is unrelated and always accepted by the API for
// programmatic access (CLI, MCP, automation).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

// Mode is how the web UI authenticates.
type Mode string

const (
	// ModeUnconfigured means onboarding has not run; the UI must offer setup.
	ModeUnconfigured Mode = ""
	// ModePassword uses a local username + password.
	ModePassword Mode = "password"
	// ModeOIDC delegates to an OpenID Connect provider (e.g. Pocket ID).
	ModeOIDC Mode = "oidc"
)

const (
	modeKey = "auth.mode"

	// SessionTTL is the sliding session lifetime.
	SessionTTL = 30 * 24 * time.Hour
	// touchInterval throttles session-row writes on validation.
	touchInterval = 5 * time.Minute
)

// Errors returned by Manager methods.
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrRateLimited        = errors.New("too many attempts; try again later")
	ErrNotConfigured      = errors.New("authentication is not configured")
	ErrAlreadyConfigured  = errors.New("authentication is already configured")
	ErrNoSession          = errors.New("no valid session")
)

// User is the authenticated local user.
type User struct {
	ID       string
	Username string
}

// Manager implements setup, login and session validation.
type Manager struct {
	store   *store.Store
	limiter *loginLimiter
	now     func() time.Time
}

// New builds a Manager on the store.
func New(st *store.Store) *Manager {
	return &Manager{
		store:   st,
		limiter: newLoginLimiter(10, 5*time.Minute),
		now:     time.Now,
	}
}

// Mode returns the configured authentication mode.
func (m *Manager) Mode(ctx context.Context) (Mode, error) {
	v, err := m.store.GetSetting(ctx, modeKey)
	if errors.Is(err, sql.ErrNoRows) {
		return ModeUnconfigured, nil
	}
	if err != nil {
		return ModeUnconfigured, err
	}
	return Mode(v), nil
}

// SetupPassword performs first-run configuration with a local user.
func (m *Manager) SetupPassword(ctx context.Context, username, password string) (User, error) {
	if err := validateUsername(username); err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	u, err := m.createSoleUser(ctx, username, hash, "", "")
	if err != nil {
		return User{}, err
	}
	return u, m.setMode(ctx, ModePassword)
}

// createSoleUser inserts the single local user; setup is once-only.
func (m *Manager) createSoleUser(ctx context.Context, username, hash, sub, email string) (User, error) {
	now := store.FormatTime(m.now())
	id := uuid.Must(uuid.NewV7()).String()
	err := m.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		mode, err := q.GetSetting(ctx, modeKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if mode != "" {
			return ErrAlreadyConfigured
		}
		n, err := q.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrAlreadyConfigured
		}
		return q.InsertUser(ctx, sqlcgen.InsertUserParams{
			ID: id, Username: username, PasswordHash: hash,
			OidcSubject: sub, OidcEmail: email,
			CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username}, nil
}

func (m *Manager) setMode(ctx context.Context, mode Mode) error {
	return m.store.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{
		Key: modeKey, Value: string(mode), UpdatedAt: store.FormatTime(m.now()),
	})
}

// Login verifies a password login. clientKey identifies the caller for rate
// limiting (client IP).
func (m *Manager) Login(ctx context.Context, username, password, clientKey string) (User, error) {
	if !m.limiter.allow(clientKey, m.now()) {
		return User{}, ErrRateLimited
	}
	mode, err := m.Mode(ctx)
	if err != nil {
		return User{}, err
	}
	if mode != ModePassword {
		return User{}, ErrNotConfigured
	}
	u, err := m.store.GetUserByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		// Burn comparable time so an unknown username is not distinguishable
		// from a wrong password by response latency.
		VerifyPassword(dummyHash, password)
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if u.PasswordHash == "" || !VerifyPassword(u.PasswordHash, password) {
		return User{}, ErrInvalidCredentials
	}
	m.limiter.reset(clientKey)
	return User{ID: u.ID, Username: u.Username}, nil
}

// dummyHash is a hash of an unguessable random value, used to equalise timing
// for unknown usernames.
var dummyHash = func() string {
	h, err := HashPassword(uuid.Must(uuid.NewV7()).String())
	if err != nil {
		panic(err)
	}
	return h
}()

// ChangePassword replaces the user's password after verifying the current one
// and revokes every other session.
func (m *Manager) ChangePassword(ctx context.Context, userID, current, next string) error {
	if err := ValidatePassword(next); err != nil {
		return err
	}
	u, err := m.store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.PasswordHash == "" || !VerifyPassword(u.PasswordHash, current) {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	now := store.FormatTime(m.now())
	return m.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
			PasswordHash: hash, UpdatedAt: now, ID: userID,
		}); err != nil {
			return err
		}
		return q.DeleteSessionsForUser(ctx, userID)
	})
}

// CreateSession issues a new session token for the user. The returned token
// goes into the cookie; only its hash is stored.
func (m *Manager) CreateSession(ctx context.Context, userID, userAgent string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := m.now()
	if len(userAgent) > 256 {
		userAgent = userAgent[:256]
	}
	err := m.store.InsertSession(ctx, sqlcgen.InsertSessionParams{
		ID: hashToken(token), UserID: userID,
		CreatedAt: store.FormatTime(now),
		ExpiresAt: store.FormatTime(now.Add(SessionTTL)),
		LastSeen:  store.FormatTime(now),
		UserAgent: userAgent,
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// ValidateSession resolves a session token to its user, sliding the expiry.
func (m *Manager) ValidateSession(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNoSession
	}
	id := hashToken(token)
	s, err := m.store.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSession
	}
	if err != nil {
		return User{}, err
	}
	now := m.now()
	exp, err := store.ParseTime(s.ExpiresAt)
	if err != nil || now.After(exp) {
		_ = m.store.DeleteSession(ctx, id)
		return User{}, ErrNoSession
	}
	if last, err := store.ParseTime(s.LastSeen); err == nil && now.Sub(last) > touchInterval {
		_ = m.store.TouchSession(ctx, sqlcgen.TouchSessionParams{
			LastSeen: store.FormatTime(now), ExpiresAt: store.FormatTime(now.Add(SessionTTL)), ID: id,
		})
	}
	u, err := m.store.GetUser(ctx, s.UserID)
	if err != nil {
		return User{}, ErrNoSession
	}
	return User{ID: u.ID, Username: u.Username}, nil
}

// Logout revokes the session for token (no-op if absent).
func (m *Manager) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return m.store.DeleteSession(ctx, hashToken(token))
}

// PruneExpired removes expired sessions; call opportunistically.
func (m *Manager) PruneExpired(ctx context.Context) error {
	return m.store.DeleteExpiredSessions(ctx, store.FormatTime(m.now()))
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidatePassword enforces a minimum bar without composition rules.
func ValidatePassword(p string) error {
	if len(p) < 10 {
		return fmt.Errorf("%w: password must be at least 10 characters", ErrValidation)
	}
	if len(p) > 512 {
		return fmt.Errorf("%w: password too long", ErrValidation)
	}
	return nil
}

func validateUsername(u string) error {
	if len(u) < 2 || len(u) > 64 {
		return fmt.Errorf("%w: username must be 2-64 characters", ErrValidation)
	}
	return nil
}

// ErrValidation marks user-correctable input problems.
var ErrValidation = errors.New("validation")
