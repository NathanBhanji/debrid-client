package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NathanBhanji/debrid-client/internal/api"
	"github.com/NathanBhanji/debrid-client/internal/apiclient"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func newAPIClient(t *testing.T) *apiclient.ClientWithResponses {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := providertest.New(domain.ProviderTorBox)
	factory := func(domain.ProviderKind, domain.Credentials, provider.Options) (provider.Provider, error) {
		return fake, nil
	}
	svc := service.New(st, service.NewProviders(st, factory, provider.Options{}), nil, events.New(), service.Config{DownloadDir: filepath.Join(dir, "dl")}, nil)
	srv := httptest.NewServer(api.New(svc, api.Options{APIKey: "k"}))
	t.Cleanup(srv.Close)
	cl, err := apiclient.NewClientWithResponses(srv.URL, apiclient.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer k")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

// session connects an in-memory MCP client to our server.
func session(t *testing.T, cl *apiclient.ClientWithResponses) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srvT, cliT := mcp.NewInMemoryTransports()
	srv := New(cl)
	if _, err := srv.Connect(ctx, srvT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, cliT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	var out map[string]any
	if r.StructuredContent != nil {
		b, _ := json.Marshal(r.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return r, out
}

func text(r *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestToolsAreListedWithSchemas(t *testing.T) {
	cs := session(t, newAPIClient(t))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range res.Tools {
		names[tl.Name] = true
		if tl.Description == "" {
			t.Errorf("tool %s lacks description", tl.Name)
		}
	}
	for _, want := range []string{"system_status", "list_torrents", "get_torrent", "add_torrent", "delete_torrent", "retry_torrent", "select_files", "update_torrent", "retry_download", "list_accounts", "add_account", "test_account", "get_settings", "update_settings"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
}

func TestToolFlow(t *testing.T) {
	cs := session(t, newAPIClient(t))

	r, out := call(t, cs, "system_status", nil)
	if r.IsError || out["accounts"] != float64(0) {
		t.Fatalf("status: %v %v", text(r), out)
	}
	r, out = call(t, cs, "add_account", map[string]any{"kind": "torbox", "api_key": "x", "name": "main"})
	if r.IsError || out["is_default"] != true {
		t.Fatalf("add_account: %s %v", text(r), out)
	}
	r, out = call(t, cs, "add_torrent", map[string]any{"magnet": "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Alpha", "category": "tv"})
	if r.IsError || out["status"] != "queued" || out["name"] != "Alpha" {
		t.Fatalf("add_torrent: %s %v", text(r), out)
	}
	id := out["id"].(string)

	// Duplicate → tool error with API detail, not a protocol error.
	r, _ = call(t, cs, "add_torrent", map[string]any{"magnet": "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if !r.IsError || !strings.Contains(text(r), "already exists") {
		t.Fatalf("duplicate should be a tool error with detail: %v %s", r.IsError, text(r))
	}
	// Schema validation: missing required magnet → protocol-level or tool error; either way not success.
	if res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "add_torrent", Arguments: map[string]any{}}); err == nil && !res.IsError {
		t.Fatal("missing magnet should fail")
	}

	r, out = call(t, cs, "list_torrents", map[string]any{"category": "tv"})
	if r.IsError || out["count"] != float64(1) {
		t.Fatalf("list_torrents: %s %v", text(r), out)
	}
	r, out = call(t, cs, "get_torrent", map[string]any{"id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if r.IsError || out["id"] != id {
		t.Fatalf("get_torrent by hash: %s %v", text(r), out)
	}
	r, out = call(t, cs, "update_torrent", map[string]any{"id": id, "category": "movies"})
	if r.IsError || out["category"] != "movies" {
		t.Fatalf("update_torrent: %s %v", text(r), out)
	}
	r, _ = call(t, cs, "retry_torrent", map[string]any{"id": id})
	if !r.IsError {
		t.Fatal("retry of queued torrent should be a tool error (409)")
	}
	r, out = call(t, cs, "get_settings", nil)
	if r.IsError || out["torrent_defaults"] == nil {
		t.Fatalf("get_settings: %s", text(r))
	}
	settings := out
	settings["categories"] = []string{"tv", "movies"}
	r, out = call(t, cs, "update_settings", map[string]any{"settings": settings})
	if r.IsError || len(out["categories"].([]any)) != 2 {
		t.Fatalf("update_settings: %s %v", text(r), out)
	}
	r, out = call(t, cs, "delete_torrent", map[string]any{"id": id, "delete_files": true})
	if r.IsError || out["ok"] != true {
		t.Fatalf("delete_torrent: %s %v", text(r), out)
	}
	r, _ = call(t, cs, "get_torrent", map[string]any{"id": id})
	if !r.IsError || !strings.Contains(text(r), "not found") {
		t.Fatalf("deleted torrent should be not found: %s", text(r))
	}
	r, out = call(t, cs, "test_account", map[string]any{"id": "main"})
	if r.IsError || out["premium"] != true {
		t.Fatalf("test_account: %s %v", text(r), out)
	}
	r, out = call(t, cs, "list_accounts", nil)
	if r.IsError || len(out["accounts"].([]any)) != 1 {
		t.Fatalf("list_accounts: %s %v", text(r), out)
	}
}

func TestHTTPHandlerMounts(t *testing.T) {
	cl := newAPIClient(t)
	h := NewHTTPHandler(cl)
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	r, out := call(t, cs, "system_status", nil)
	if r.IsError || out["version"] == nil {
		t.Fatalf("status over http: %s %v", text(r), out)
	}
}
