package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func noEnv() []string { return nil }

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(Options{File: filepath.Join(t.TempDir(), "missing.yaml"), Environ: noEnv})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	d := Default()
	if cfg.Server.Listen != d.Server.Listen || cfg.Engine.DownloadLimit != 2 || cfg.Engine.PollInterval != 10*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.DownloadDir != filepath.Join(cfg.DataDir, "downloads") {
		t.Fatalf("download dir should derive from data dir, got %s", cfg.DownloadDir)
	}
}

func TestLoadExplicitMissingFileIsError(t *testing.T) {
	_, err := Load(Options{File: filepath.Join(t.TempDir(), "nope.yaml"), FileExplicit: true, Environ: noEnv})
	if err == nil {
		t.Fatal("expected error for missing explicit config file")
	}
}

func TestPrecedenceFileEnvFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
data_dir: /tmp/from-file
server:
  listen: "file:1"
log:
  level: debug
engine:
  download_limit: 5
  poll_interval: 15s
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	environ := func() []string {
		return []string{
			"DEBRID_SERVER__LISTEN=env:2",
			"DEBRID_LOG__LEVEL=warn",
			"DEBRID_ENGINE__IDLE_POLL_INTERVAL=1m",
			"DEBRID_CONFIG=/should/be/ignored",
			"UNRELATED=1",
		}
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	BindFlags(fs)
	if err := fs.Parse([]string{"--server-listen=flag:3"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Options{File: path, Environ: environ, Flags: fs})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DataDir != "/tmp/from-file" {
		t.Errorf("file value lost: %s", cfg.DataDir)
	}
	if cfg.DownloadDir != "/tmp/from-file/downloads" {
		t.Errorf("download dir should derive from file data_dir, got %s", cfg.DownloadDir)
	}
	if cfg.Engine.DownloadLimit != 5 || cfg.Engine.PollInterval != 15*time.Second {
		t.Errorf("file engine values lost: %+v", cfg.Engine)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("env should override file: %s", cfg.Log.Level)
	}
	if cfg.Engine.IdlePollInterval != time.Minute {
		t.Errorf("env duration not parsed: %s", cfg.Engine.IdlePollInterval)
	}
	if cfg.Server.Listen != "flag:3" {
		t.Errorf("flag should override env and file: %s", cfg.Server.Listen)
	}
	// Flags not explicitly set must not clobber file/env values with their defaults.
	if cfg.Log.Level == Default().Log.Level {
		t.Errorf("unset flag default clobbered env value")
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]func(*Config){
		"bad level":       func(c *Config) { c.Log.Level = "loud" },
		"bad format":      func(c *Config) { c.Log.Format = "xml" },
		"download limit":  func(c *Config) { c.Engine.DownloadLimit = 0 },
		"poll too short":  func(c *Config) { c.Engine.PollInterval = 100 * time.Millisecond },
		"idle < poll":     func(c *Config) { c.Engine.IdlePollInterval = time.Second },
		"base path slash": func(c *Config) { c.Server.BasePath = "debrid" },
		"empty listen":    func(c *Config) { c.Server.Listen = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default().Derived()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
	if err := Default().Derived().Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestWriteDefaultFileRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := WriteDefaultFile(path); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefaultFile(path); err == nil {
		t.Fatal("second write should fail (file exists)")
	}
	cfg, err := Load(Options{File: path, FileExplicit: true, Environ: noEnv})
	if err != nil {
		t.Fatalf("generated file must load: %v", err)
	}
	if want := Default().Derived(); cfg != want {
		t.Fatalf("generated file should equal defaults:\n got %+v\nwant %+v", cfg, want)
	}
}

func TestRedacted(t *testing.T) {
	c := Default()
	c.Server.APIKey = "secret"
	if r := c.Redacted(); r.Server.APIKey == "secret" || !strings.Contains(r.Server.APIKey, "*") {
		t.Fatalf("api key not redacted: %q", r.Server.APIKey)
	}
}
