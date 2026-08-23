package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NathanBhanji/debrid-client/internal/auth"
)

type setupOIDCIn struct {
	Body struct {
		Issuer       string `json:"issuer" doc:"OIDC issuer URL, e.g. https://id.example.com"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret,omitempty" doc:"Optional for public clients"`
	}
}

type setupOIDCOut struct {
	Body struct {
		AuthURL string `json:"auth_url" doc:"Navigate the browser here to complete setup at the provider"`
	}
}

type oidcRedirectOut struct {
	Status   int
	Location string `header:"Location"`
}

type oidcCallbackIn struct {
	State    string `query:"state"`
	Code     string `query:"code"`
	AuthErr  string `query:"error"`
	ErrDescr string `query:"error_description"`
}

type oidcCallbackOut struct {
	Status    int
	Location  string      `header:"Location"`
	SetCookie http.Cookie `header:"Set-Cookie"`
}

// callbackURL builds the redirect URI for this server as the browser sees it.
func (h *Handler) callbackURL(hc huma.Context) string {
	scheme := "http"
	if cookieSecure(hc) {
		scheme = "https"
	}
	prefix := ""
	if h.opts.BasePath != "" && h.opts.BasePath != "/" {
		prefix = "/" + url.PathEscape(trimSlashes(h.opts.BasePath))
	}
	return scheme + "://" + hc.Host() + prefix + "/api/v1/auth/oidc/callback"
}

func trimSlashes(s string) string {
	for len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// uiError sends the browser back to the UI with a message it can display.
func uiError(msg string) string {
	return "/?auth_error=" + url.QueryEscape(msg)
}

// registerOIDCRoutes wires the OIDC endpoints. p is the /api/v1 prefix.
func (h *Handler) registerOIDCRoutes(p string) {
	mgr := h.opts.Auth

	// Configure the provider and get the authorization URL to finish setup.
	// Key-only like password setup: proof of server ownership. The mode flips
	// to oidc only when the callback pins the first subject, so a wrong
	// configuration can simply be corrected and retried.
	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-setup-oidc", Method: http.MethodPost, Path: p + "/auth/setup/oidc",
		Summary:  "First-run setup: configure OIDC and begin the pinning sign-in (requires the API key)",
		Tags:     []string{"auth"},
		Metadata: map[string]any{metaKeyOnly: true},
	}, func(ctx context.Context, in *setupOIDCIn) (*setupOIDCOut, error) {
		if err := mgr.ConfigureOIDC(ctx, auth.OIDCConfig{
			Issuer: in.Body.Issuer, ClientID: in.Body.ClientID, ClientSecret: in.Body.ClientSecret,
		}); err != nil {
			return nil, h.mapAuthErr(err)
		}
		hc := humaCtxFrom(ctx)
		authURL, err := mgr.StartOIDC(ctx, h.callbackURL(hc), true)
		if err != nil {
			return nil, h.mapAuthErr(err)
		}
		out := &setupOIDCOut{}
		out.Body.AuthURL = authURL
		return out, nil
	})

	// Browser navigation target for signing in once OIDC is the active mode.
	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-oidc-start", Method: http.MethodGet, Path: p + "/auth/oidc/start",
		Summary: "Redirect to the OIDC provider to sign in", Tags: []string{"auth"},
		Metadata:      map[string]any{metaPublic: true},
		DefaultStatus: http.StatusFound,
	}, func(ctx context.Context, _ *struct{}) (*oidcRedirectOut, error) {
		hc := humaCtxFrom(ctx)
		authURL, err := mgr.StartOIDC(ctx, h.callbackURL(hc), false)
		if err != nil {
			return nil, h.mapAuthErr(err)
		}
		return &oidcRedirectOut{Status: http.StatusFound, Location: authURL}, nil
	})

	// Provider callback for both setup and login flows. Errors redirect back
	// to the UI with a display message instead of a bare JSON problem.
	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-oidc-callback", Method: http.MethodGet, Path: p + "/auth/oidc/callback",
		Summary: "OIDC provider callback", Tags: []string{"auth"},
		Metadata:      map[string]any{metaPublic: true},
		DefaultStatus: http.StatusFound,
	}, func(ctx context.Context, in *oidcCallbackIn) (*oidcCallbackOut, error) {
		hc := humaCtxFrom(ctx)
		fail := func(msg string) (*oidcCallbackOut, error) {
			return &oidcCallbackOut{Status: http.StatusFound, Location: uiError(msg)}, nil
		}
		if in.AuthErr != "" {
			msg := in.AuthErr
			if in.ErrDescr != "" {
				msg += ": " + in.ErrDescr
			}
			return fail(msg)
		}
		if in.State == "" || in.Code == "" {
			return fail("missing state or code")
		}
		u, err := mgr.CompleteOIDC(ctx, in.State, in.Code)
		if err != nil {
			h.opts.Logger.Warn("oidc callback failed", "err", err)
			return fail(publicOIDCError(err))
		}
		token, err := mgr.CreateSession(ctx, u.ID, hc.Header("User-Agent"))
		if err != nil {
			return fail("session creation failed")
		}
		return &oidcCallbackOut{
			Status: http.StatusFound, Location: "/",
			SetCookie: newSessionCookie(token, cookieSecure(hc), int(auth.SessionTTL.Seconds())),
		}, nil
	})
}

// publicOIDCError maps callback failures to a safe display string; provider
// exchange details stay in the log.
func publicOIDCError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, auth.ErrOIDCStateInvalid):
		return "sign-in expired; try again"
	case errors.Is(err, auth.ErrOIDCWrongSubject):
		return "this identity is not authorized for this server"
	case errors.Is(err, auth.ErrAlreadyConfigured):
		return "authentication is already configured"
	}
	return "sign-in failed; check the server log"
}
