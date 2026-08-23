package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/apiclient"
	"github.com/NathanBhanji/debrid-client/internal/config"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

// clientFlags are shared by all commands that talk to a running server.
type clientFlags struct {
	server string
	apiKey string
	json   bool
}

func (c *clientFlags) bind(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&c.server, "server", "", "API server URL (default http://<server.listen> from config, or $DEBRID_SERVER)")
	cmd.PersistentFlags().StringVar(&c.apiKey, "api-key", "", "API key (default $DEBRID_API_KEY, config server.api_key, or the key stored in the local database)")
	cmd.PersistentFlags().BoolVar(&c.json, "json", false, "print raw JSON responses")
}

// resolve builds an API client, discovering server URL and key from flags,
// env, config and — for a local server — the database.
func (c *clientFlags) resolve(cmd *cobra.Command, g *globalFlags) (*apiclient.ClientWithResponses, error) {
	cfg, cfgErr := config.Load(config.Options{File: g.configFile, FileExplicit: g.configFile != ""})
	if cfgErr != nil && g.configFile != "" {
		return nil, cfgErr // an explicitly requested config file must load
	}

	server := c.server
	if server == "" {
		server = os.Getenv("DEBRID_SERVER")
	}
	if server == "" && cfgErr == nil {
		server = "http://" + localListen(cfg.Server.Listen) + cfg.Server.BasePath
	}
	if server == "" {
		server = "http://127.0.0.1:8080"
	}

	key := c.apiKey
	if key == "" {
		key = os.Getenv("DEBRID_API_KEY")
	}
	if key == "" && cfgErr == nil {
		key = cfg.Server.APIKey
	}
	var dbErr error
	if key == "" && cfgErr == nil {
		key, dbErr = keyFromDB(cmd.Context(), cfg)
	}
	if key == "" {
		msg := "no API key: pass --api-key, set DEBRID_API_KEY, or run on the machine hosting the server"
		switch {
		case cfgErr != nil:
			msg += fmt.Sprintf(" (config: %v)", cfgErr)
		case dbErr != nil:
			msg += fmt.Sprintf(" (local database: %v)", dbErr)
		}
		return nil, errors.New(msg)
	}
	return apiclient.NewClientWithResponses(server, apiclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+key)
		return nil
	}), apiclient.WithHTTPClient(&http.Client{Timeout: 2 * time.Minute, Transport: &hintTransport{}}))
}

// localListen turns a server listen address into something a local client can dial.
func localListen(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// hintTransport adds a "is the server running?" hint to connection errors.
type hintTransport struct{}

func (hintTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("%w (is the server running? see --server)", err)
		}
	}
	return resp, err
}

// keyFromDB reads the generated API key from the local database, if present.
// The database is opened read-only with a short busy timeout so the CLI can
// never migrate, lock or otherwise disturb a running server's data.
func keyFromDB(ctx context.Context, cfg config.Config) (string, error) {
	path := filepath.Join(cfg.DataDir, "debrid.db")
	if _, err := os.Stat(path); err != nil {
		return "", nil // no local server data; not an error
	}
	key, err := store.ReadSettingReadOnly(ctx, path, "server.api_key")
	if err != nil {
		return "", err
	}
	return key, nil
}

// apiError extracts a readable message from a non-2xx response body.
func apiError(status int, body []byte) error {
	var em apiclient.ErrorModel
	if json.Unmarshal(body, &em) == nil && (em.Detail != nil || em.Title != nil) {
		msg := ""
		if em.Title != nil {
			msg = *em.Title
		}
		if em.Detail != nil {
			if strings.HasPrefix(strings.ToLower(*em.Detail), strings.ToLower(msg)) { // "Not Found: not found: …" → once
				msg = *em.Detail
			} else {
				msg += ": " + *em.Detail
			}
		}
		if em.Errors != nil {
			for _, e := range *em.Errors {
				if e.Message != nil {
					msg += "\n  - " + *e.Message
					if e.Location != nil {
						msg += " (" + *e.Location + ")"
					}
				}
			}
		}
		return fmt.Errorf("%s", strings.TrimPrefix(msg, ": "))
	}
	if len(body) > 0 {
		return fmt.Errorf("http %d: %s", status, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("http %d", status)
}

// respond checks the status and prints either JSON or the result of render.
// Use respondJSON for responses with a decoded body; this variant is for
// bodiless responses (204) and raw passthrough.
func (c *clientFlags) respond(w io.Writer, status int, body []byte, want int, render func(w io.Writer) error) error {
	if status != want {
		return apiError(status, body)
	}
	if c.json || render == nil {
		return writeJSON(w, body)
	}
	return render(w)
}

// respondJSON is respond for responses the generated client decoded into v.
// A 2xx without a decoded body (wrong host serving HTML, proxy error page)
// is reported instead of dereferenced.
func respondJSON[T any](c *clientFlags, w io.Writer, status int, body []byte, want int, v *T, render func(w io.Writer, v T) error) error {
	if status != want {
		return apiError(status, body)
	}
	if v == nil {
		return fmt.Errorf("unexpected response from server (status %d, not JSON): %s — is --server pointing at debrid-client?", status, firstLine(strings.TrimSpace(string(body))))
	}
	if c.json || render == nil {
		return writeJSON(w, body)
	}
	return render(w, *v)
}

func writeJSON(w io.Writer, body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err != nil {
		_, err = w.Write(body)
		return err
	}
	_, err := fmt.Fprintln(w, buf.String())
	return err
}

func table(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	return tw.Flush()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func pct(f float64) string { return fmt.Sprintf("%3.0f%%", f*100) }

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func ptr[T any](v T) *T { return &v }
