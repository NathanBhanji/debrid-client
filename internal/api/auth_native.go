package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type nativeStartIn struct {
	Body struct {
		Challenge string `json:"challenge" minLength:"43" maxLength:"43" pattern:"^[A-Za-z0-9_-]{43}$" doc:"S256 challenge for an app-owned PKCE verifier"`
		State     string `json:"state" minLength:"43" maxLength:"43" pattern:"^[A-Za-z0-9_-]{43}$" doc:"App-generated random 32-byte base64url state"`
	}
}
type nativeStartOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		AuthorizationURL string `json:"authorization_url"`
	}
}
type nativeExchangeIn struct {
	Body struct {
		Code     string `json:"code" minLength:"43" maxLength:"43" pattern:"^[A-Za-z0-9_-]{43}$"`
		Verifier string `json:"verifier" minLength:"43" maxLength:"128" pattern:"^[A-Za-z0-9._~-]+$"`
	}
}
type nativeExchangeOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		SessionToken string `json:"session_token" doc:"Server-bound session secret; store only in protected credential storage"`
		Username     string `json:"username"`
	}
}

func (h *Handler) registerNativeAuthRoutes(p string) {
	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-native-start", Method: http.MethodPost, Path: p + "/auth/native/start",
		Summary: "Start secure native Pocket ID sign-in", Tags: []string{"auth"},
		Metadata: map[string]any{metaPublic: true}, MaxBodyBytes: 2048,
	}, func(ctx context.Context, in *nativeStartIn) (*nativeStartOut, error) {
		hc := humaCtxFrom(ctx)
		if !cookieSecure(hc) {
			return nil, huma.Error400BadRequest("native sign-in requires HTTPS")
		}
		authURL, err := h.opts.Auth.StartNativeOIDC(ctx, h.callbackURL(hc), in.Body.Challenge, in.Body.State, clientIP(hc))
		if err != nil {
			return nil, h.mapAuthErr(err)
		}
		out := &nativeStartOut{CacheControl: "no-store"}
		out.Body.AuthorizationURL = authURL
		return out, nil
	})
	huma.Register(h.Huma, huma.Operation{
		OperationID: "auth-native-exchange", Method: http.MethodPost, Path: p + "/auth/native/exchange",
		Summary: "Exchange one-time native grant with its PKCE verifier", Tags: []string{"auth"},
		Metadata: map[string]any{metaPublic: true}, MaxBodyBytes: 2048,
	}, func(ctx context.Context, in *nativeExchangeIn) (*nativeExchangeOut, error) {
		hc := humaCtxFrom(ctx)
		if !cookieSecure(hc) {
			return nil, huma.Error400BadRequest("native sign-in requires HTTPS")
		}
		token, user, err := h.opts.Auth.ExchangeNativeGrant(ctx, in.Body.Code, in.Body.Verifier, clientIP(hc), hc.Header("User-Agent"))
		if err != nil {
			return nil, h.mapAuthErr(err)
		}
		out := &nativeExchangeOut{CacheControl: "no-store"}
		out.Body.SessionToken = token
		out.Body.Username = user.Username
		return out, nil
	})
}
