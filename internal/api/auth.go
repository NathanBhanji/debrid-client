package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NathanBhanji/debrid-client/internal/auth"
)

// SessionCookie is the browser session cookie name.
const SessionCookie = "debrid_session"

// ctxKey namespaces context values set by the auth middleware.
type ctxKey string

const ctxUser ctxKey = "auth_user"

// sessionUser returns the session user for the request, if any (API-key
// requests have none).
func sessionUser(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(ctxUser).(auth.User)
	return u, ok
}

// authorize validates the request against the Bearer key, the ?api_key=
// query (SSE only), or the session cookie, in that order. It returns the
// possibly-augmented context and whether the request may proceed.
func (h *Handler) authorize(ctx huma.Context, keyOnly bool) (huma.Context, bool) {
	token, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer ")
	if !ok {
		if op := ctx.Operation(); op != nil && op.Metadata[metaQueryKey] == true {
			token = ctx.Query("api_key") // EventSource can't set headers
		}
	}
	if token != "" && constantTimeEq(token, h.opts.APIKey) {
		return ctx, true
	}
	if keyOnly || h.opts.Auth == nil {
		return ctx, false
	}
	c, err := huma.ReadCookie(ctx, SessionCookie)
	if err != nil {
		return ctx, false
	}
	u, err := h.opts.Auth.ValidateSession(ctx.Context(), c.Value)
	if err != nil {
		return ctx, false
	}
	// Cookie-authenticated writes must originate from our own UI: a
	// cross-site form or fetch carries a foreign Origin (CSRF).
	if ctx.Method() != http.MethodGet && ctx.Method() != http.MethodHead {
		if !sameOrigin(ctx) {
			return ctx, false
		}
	}
	return huma.WithValue(ctx, ctxUser, u), true
}

// sameOrigin reports whether the request's Origin (when present) matches the
// host the request was sent to. Absent Origin means a non-browser client.
func sameOrigin(ctx huma.Context) bool {
	origin := ctx.Header("Origin")
	if origin == "" || origin == "null" {
		return origin == ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, ctx.Host())
}

// cookieSecure decides the cookie Secure flag: on for TLS-terminated
// requests (direct or via a proxy that sets X-Forwarded-Proto).
func cookieSecure(ctx huma.Context) bool {
	if ctx.TLS() != nil {
		return true
	}
	return strings.EqualFold(ctx.Header("X-Forwarded-Proto"), "https")
}

func newSessionCookie(value string, secure bool, maxAge int) http.Cookie {
	return http.Cookie{
		Name: SessionCookie, Value: value, Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		MaxAge: maxAge,
	}
}

// clientIP keys login rate limiting. The direct peer address is used —
// X-Forwarded-For is spoofable and this server usually faces its user
// directly or via a single trusted proxy.
func clientIP(ctx huma.Context) string {
	addr := ctx.RemoteAddr()
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

// --- request/response bodies -------------------------------------------

type authStatusOut struct {
	Body struct {
		Configured bool   `json:"configured" doc:"Whether onboarding has completed"`
		Mode       string `json:"mode" doc:"Configured auth mode: password or oidc; empty until onboarding"`
	}
}

type setupPasswordIn struct {
	Body struct {
		Username string `json:"username" minLength:"2" maxLength:"64"`
		Password string `json:"password" minLength:"10" maxLength:"512"`
	}
}

type loginIn struct {
	Body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
}

type sessionOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		Username string `json:"username"`
	}
}

type logoutOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type meOut struct {
	Body struct {
		Username string `json:"username,omitempty" doc:"Set for session requests; empty for API-key requests"`
		Mode     string `json:"mode"`
		APIKey   bool   `json:"api_key" doc:"True when authenticated with the API key"`
	}
}

type changePasswordIn struct {
	Body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password" minLength:"10" maxLength:"512"`
	}
}

// registerAuthRoutes wires the auth endpoints. p is the /api/v1 prefix.
func (h *Handler) registerAuthRoutes(p string) {
	if h.opts.Auth == nil {
		return
	}
	mgr := h.opts.Auth

	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-status", Method: http.MethodGet, Path: p + "/auth/status",
		Summary: "Authentication status", Tags: []string{"auth"},
		Metadata: map[string]any{metaPublic: true},
	}, func(ctx context.Context, _ *struct{}) (*authStatusOut, error) {
		mode, err := mgr.Mode(ctx)
		if err != nil {
			return nil, h.mapErr(err)
		}
		out := &authStatusOut{}
		out.Body.Configured = mode != auth.ModeUnconfigured
		out.Body.Mode = string(mode)
		return out, nil
	})

	// Setup requires the API key (proof of server ownership) — sessions are
	// deliberately not accepted, and it only works once.
	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-setup-password", Method: http.MethodPost, Path: p + "/auth/setup/password",
		Summary:  "First-run setup: create the local user (requires the API key)",
		Tags:     []string{"auth"},
		Metadata: map[string]any{metaKeyOnly: true},
	}, func(ctx context.Context, in *setupPasswordIn) (*sessionOut, error) {
		u, err := mgr.SetupPassword(ctx, in.Body.Username, in.Body.Password)
		if err != nil {
			return nil, h.mapAuthErr(err)
		}
		return h.newSession(ctx, mgr, u)
	})

	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-login", Method: http.MethodPost, Path: p + "/auth/login",
		Summary: "Log in with username and password", Tags: []string{"auth"},
		Metadata: map[string]any{metaPublic: true},
	}, func(ctx context.Context, in *loginIn) (*sessionOut, error) {
		hc := humaCtxFrom(ctx)
		u, err := mgr.Login(ctx, in.Body.Username, in.Body.Password, clientIP(hc))
		if err != nil {
			return nil, h.mapAuthErr(err)
		}
		return h.newSession(ctx, mgr, u)
	})

	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-logout", Method: http.MethodPost, Path: p + "/auth/logout",
		Summary: "Log out the current session", Tags: []string{"auth"},
	}, func(ctx context.Context, _ *struct{}) (*logoutOut, error) {
		hc := humaCtxFrom(ctx)
		if c, err := huma.ReadCookie(hc, SessionCookie); err == nil {
			_ = mgr.Logout(ctx, c.Value)
		}
		return &logoutOut{SetCookie: newSessionCookie("", cookieSecure(hc), -1)}, nil
	})

	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-me", Method: http.MethodGet, Path: p + "/auth/me",
		Summary: "Current authentication identity", Tags: []string{"auth"},
	}, func(ctx context.Context, _ *struct{}) (*meOut, error) {
		mode, err := mgr.Mode(ctx)
		if err != nil {
			return nil, h.mapErr(err)
		}
		out := &meOut{}
		out.Body.Mode = string(mode)
		if u, ok := sessionUser(ctx); ok {
			out.Body.Username = u.Username
		} else {
			out.Body.APIKey = true
		}
		return out, nil
	})

	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-change-password", Method: http.MethodPost, Path: p + "/auth/password",
		Summary: "Change the local user's password (revokes all sessions)", Tags: []string{"auth"},
	}, func(ctx context.Context, in *changePasswordIn) (*struct{}, error) {
		u, ok := sessionUser(ctx)
		if !ok {
			return nil, huma.Error403Forbidden("password changes require a browser session")
		}
		if err := mgr.ChangePassword(ctx, u.ID, in.Body.CurrentPassword, in.Body.NewPassword); err != nil {
			return nil, h.mapAuthErr(err)
		}
		return nil, nil
	})
}

// newSession issues a session and its cookie for u.
func (h *Handler) newSession(ctx context.Context, mgr *auth.Manager, u auth.User) (*sessionOut, error) {
	hc := humaCtxFrom(ctx)
	token, err := mgr.CreateSession(ctx, u.ID, hc.Header("User-Agent"))
	if err != nil {
		return nil, h.mapErr(err)
	}
	out := &sessionOut{SetCookie: newSessionCookie(token, cookieSecure(hc), int(auth.SessionTTL.Seconds()))}
	out.Body.Username = u.Username
	return out, nil
}

func (h *Handler) mapAuthErr(err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return huma.Error401Unauthorized(err.Error())
	case errors.Is(err, auth.ErrRateLimited):
		return huma.NewError(http.StatusTooManyRequests, err.Error())
	case errors.Is(err, auth.ErrAlreadyConfigured):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, auth.ErrNotConfigured):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, auth.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return h.mapErr(err)
}

// --- huma.Context plumbing ---------------------------------------------

// Handlers that need the raw request (cookies, client address, TLS state)
// get the huma.Context stashed in the request context by authMiddleware.
const ctxHuma ctxKey = "huma_ctx"

func humaCtxFrom(ctx context.Context) huma.Context {
	hc, _ := ctx.Value(ctxHuma).(huma.Context)
	return hc
}
