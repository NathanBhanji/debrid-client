package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"

	"github.com/NathanBhanji/debrid-client/internal/auth"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func nativeAPIFixture(t *testing.T) (*httptest.Server, *auth.Manager) {
	t.Helper()
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var idp *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": idp.URL, "authorization_endpoint": idp.URL + "/authorize", "token_endpoint": idp.URL + "/token", "jwks_uri": idp.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &signingKey.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, (&jose.SignerOptions{}).WithHeader("kid", "fixture"))
		if e != nil {
			t.Error(e)
			http.Error(w, "fixture", 500)
			return
		}
		claims, _ := json.Marshal(map[string]any{"iss": idp.URL, "aud": "native-fixture", "sub": "sole-user", "preferred_username": "Fixture User", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()})
		signed, e := signer.Sign(claims)
		if e != nil {
			t.Error(e)
			http.Error(w, "fixture", 500)
			return
		}
		token, e := signed.CompactSerialize()
		if e != nil {
			t.Error(e)
			http.Error(w, "fixture", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture-provider-token", "token_type": "Bearer", "id_token": token})
	})
	idp = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := auth.New(st)
	if err := mgr.ConfigureOIDC(ctx, auth.OIDCConfig{Issuer: idp.URL, ClientID: "native-fixture"}); err != nil {
		t.Fatal(err)
	}
	setup, err := mgr.StartOIDC(ctx, "https://server.example.test/api/v1/auth/oidc/callback", true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(setup)
	if _, err := mgr.CompleteOIDC(ctx, parsed.Query().Get("state"), "fixture-code"); err != nil {
		t.Fatal(err)
	}
	svc := service.New(st, service.NewProviders(st, nil, provider.Options{}), nil, events.New(), service.Config{}, nil)
	srv := httptest.NewServer(New(svc, Options{APIKey: key, Auth: mgr}))
	t.Cleanup(srv.Close)
	return srv, mgr
}

func TestNativeAPIRoundTripAndDenial(t *testing.T) {
	srv, mgr := nativeAPIFixture(t)
	verifier, clientState := oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
	headers := map[string]string{"X-Forwarded-Proto": "https"}
	body, _ := json.Marshal(map[string]string{"challenge": oauth2.S256ChallengeFromVerifier(verifier), "state": clientState})
	resp := req(t, srv, "POST", "/api/v1/auth/native/start", string(body), headers)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	var started struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(started.AuthorizationURL)
	expectedRedirect := "https://" + strings.TrimPrefix(srv.URL, "http://") + "/api/v1/auth/oidc/callback"
	if authorization.Query().Get("redirect_uri") != expectedRedirect {
		t.Fatal("provider redirect registration changed")
	}
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	callback, err := client.Get(srv.URL + "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(authorization.Query().Get("state")) + "&code=fixture-code")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = callback.Body.Close() }()
	if callback.StatusCode != 302 || callback.Header.Get("Set-Cookie") != "" || callback.Header.Get("Cache-Control") != "no-store" || callback.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("native callback leaked cookie or lacks protection")
	}
	appURL, _ := url.Parse(callback.Header.Get("Location"))
	if appURL.Scheme != "dev.bhanji.debrid" || appURL.Query().Get("state") != clientState || len(appURL.Query()) != 2 {
		t.Fatal("invalid app callback")
	}
	exchange, _ := json.Marshal(map[string]string{"code": appURL.Query().Get("code"), "verifier": verifier})
	resp = req(t, srv, "POST", "/api/v1/auth/native/exchange", string(exchange), headers)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("exchange: %d", resp.StatusCode)
	}
	var session struct {
		Token    string `json:"session_token"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ValidateSession(context.Background(), session.Token); err != nil || session.Username != "Fixture User" {
		t.Fatal("no authenticated session")
	}
	if authenticated := req(t, srv, "GET", "/api/v1/auth/me", "", map[string]string{"Cookie": SessionCookie + "=" + session.Token}); authenticated.StatusCode != 200 {
		t.Fatal("native session rejected by existing middleware")
	}
	if resp = req(t, srv, "POST", "/api/v1/auth/native/exchange", string(exchange), headers); resp.StatusCode != 401 {
		t.Fatal("replay accepted")
	}
	resp = req(t, srv, "GET", "/api/v1/auth/status", "", nil)
	var status struct {
		Native bool `json:"native_sso"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&status)
	if !status.Native {
		t.Fatal("capability missing")
	}

	// Provider-supplied messages never enter a native callback, and denial consumes state.
	resp = req(t, srv, "POST", "/api/v1/auth/native/start", string(body), headers)
	_ = json.NewDecoder(resp.Body).Decode(&started)
	authorization, _ = url.Parse(started.AuthorizationURL)
	denied, err := client.Get(srv.URL + "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(authorization.Query().Get("state")) + "&error=access_denied&error_description=private-provider-detail")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = denied.Body.Close() }()
	location := denied.Header.Get("Location")
	if !strings.HasPrefix(location, auth.NativeCallback+"?") || !strings.Contains(location, "error=sign_in_failed") || strings.Contains(location, "private-provider-detail") {
		t.Fatal("denial not safely delivered")
	}
}
func TestNativeAPIRequiresHTTPSAndConfiguredOIDC(t *testing.T) {
	srv := setupAuth(t)
	body, _ := json.Marshal(map[string]string{"challenge": oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier()), "state": oauth2.GenerateVerifier()})
	if resp := req(t, srv, "POST", "/api/v1/auth/native/start", string(body), nil); resp.StatusCode != 400 {
		t.Fatalf("HTTP start: %d", resp.StatusCode)
	}
	headers := map[string]string{"X-Forwarded-Proto": "https"}
	if resp := req(t, srv, "POST", "/api/v1/auth/native/start", string(body), headers); resp.StatusCode != 409 {
		t.Fatalf("unconfigured: %d", resp.StatusCode)
	}
	if resp := req(t, srv, "POST", "/api/v1/auth/native/start", `{"challenge":"bad","state":"bad"}`, headers); resp.StatusCode != 422 {
		t.Fatalf("malformed: %d", resp.StatusCode)
	}
	exchange, _ := json.Marshal(map[string]string{"code": oauth2.GenerateVerifier(), "verifier": oauth2.GenerateVerifier()})
	if resp := req(t, srv, "POST", "/api/v1/auth/native/exchange", string(exchange), nil); resp.StatusCode != 400 {
		t.Fatalf("HTTP exchange: %d", resp.StatusCode)
	}
}
