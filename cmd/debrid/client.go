package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

	server := c.server
	if server == "" {
		server = os.Getenv("DEBRID_SERVER")
	}
	if server == "" && cfgErr == nil {
		server = "http://" + strings.Replace(cfg.Server.Listen, "0.0.0.0", "127.0.0.1", 1) + cfg.Server.BasePath
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
	if key == "" && cfgErr == nil {
		key = keyFromDB(cmd.Context(), cfg)
	}
	if key == "" {
		return nil, errors.New("no API key: pass --api-key, set DEBRID_API_KEY, or run on the machine hosting the server")
	}
	return apiclient.NewClientWithResponses(server, apiclient.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+key)
		return nil
	}), apiclient.WithHTTPClient(&http.Client{Timeout: 2 * time.Minute}))
}

// keyFromDB reads the generated API key from the local database, if present.
func keyFromDB(ctx context.Context, cfg config.Config) string {
	path := filepath.Join(cfg.DataDir, "debrid.db")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	st, err := store.Open(ctx, path)
	if err != nil {
		return ""
	}
	defer func() { _ = st.Close() }()
	v, err := st.GetSetting(ctx, "server.api_key")
	if err != nil {
		return ""
	}
	return v
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
			msg += ": " + *em.Detail
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
func (c *clientFlags) respond(w io.Writer, status int, body []byte, want int, render func(w io.Writer) error) error {
	if status != want {
		return apiError(status, body)
	}
	if c.json || render == nil {
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
	return render(w)
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
