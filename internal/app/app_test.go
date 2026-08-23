package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/config"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestRunServesAndShutsDownPromptlyWithSSEClient(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default().Derived()
	cfg.DataDir = dir
	cfg.DownloadDir = filepath.Join(dir, "dl")
	cfg.Server.Listen = freePort(t)
	cfg.Server.APIKey = "k"
	ctx, cancel := context.WithCancel(context.Background())
	a, err := New(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	base := "http://" + cfg.Server.Listen
	// Wait for the server.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/api/v1/health")
		if err == nil && resp.StatusCode == 200 {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Attach an SSE client that would otherwise pin the connection open.
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	req, _ := http.NewRequestWithContext(sseCtx, "GET", base+"/api/v1/events?api_key=k", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("sse: %v %v", err, resp)
	}
	go func() { _, _ = io.Copy(io.Discard, resp.Body) }()

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("shutdown took %s with an SSE client attached (should cancel request contexts)", d)
	}
	// Store is closed: a second Open on the same path must work (no lock held).
	a2, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = a2.Close()
}
