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
			"DEBRID_API_KEY=cli-only-must-be-ignored",
			"DEBRID_SERVER=http://cli-only",
			"UNRELATED=1",
		}
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	BindFlags(fs)
	fs.String("config", "", "unrelated persistent flag must be ignored")
	if err := fs.Parse([]string{"--server-listen=flag:3", "--config=/x.yaml"}); err != nil {
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

func TestEveryFlagLands(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	BindFlags(fs)
	args := []string{"--data-dir=/fd", "--download-dir=/fdl", "--server-listen=h:1", "--server-base-path=/bp", "--log-level=debug", "--log-format=json"}
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Options{File: filepath.Join(t.TempDir(), "none.yaml"), Environ: noEnv, Flags: fs})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/fd" || cfg.DownloadDir != "/fdl" || cfg.Server.Listen != "h:1" || cfg.Server.BasePath != "/bp" || cfg.Log.Level != "debug" || cfg.Log.Format != "json" {
		t.Fatalf("flags not applied: %+v", cfg)
	}
	// Every bound flag must be in the table and vice versa.
	fs.VisitAll(func(f *pflag.Flag) {
		if _, ok := flagKeys[f.Name]; !ok {
			t.Errorf("flag %s not in flagKeys", f.Name)
		}
	})
	for name := range flagKeys {
		if fs.Lookup(name) == nil {
			t.Errorf("flagKeys entry %s not bound", name)
		}
	}
}

func TestEmptySectionKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("engine:\n  # download_limit: 4\nserver:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Options{File: path, FileExplicit: true, Environ: noEnv})
	if err != nil {
		t.Fatalf("empty sections should not wipe defaults: %v", err)
	}
	if cfg.Engine.DownloadLimit != 2 || cfg.Server.Listen != Default().Server.Listen {
		t.Fatalf("defaults lost: %+v", cfg)
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	_ = os.WriteFile(path, []byte("engine:\n  dwnload_limit: 4\n"), 0o600)
	if _, err := Load(Options{File: path, FileExplicit: true, Environ: noEnv}); err == nil || !strings.Contains(err.Error(), "dwnload_limit") {
		t.Fatalf("typo in file should be rejected, got %v", err)
	}
	env := func() []string { return []string{"DEBRID_ENGINE__DOWNLOD_LIMIT=4"} }
	if _, err := Load(Options{File: filepath.Join(t.TempDir(), "none.yaml"), Environ: env}); err == nil {
		t.Fatal("typo in env should be rejected")
	}
}

func TestEnvConfigPathIsExplicit(t *testing.T) {
	env := func() []string { return []string{"DEBRID_CONFIG=/definitely/not/here.yaml"} }
	if _, err := Load(Options{Environ: env}); err == nil {
		t.Fatal("missing DEBRID_CONFIG file should be an error")
	}
	t.Setenv("DEBRID_CONFIG", "/from/env.yaml")
	if DefaultConfigPath() != "/from/env.yaml" {
		t.Fatal("DefaultConfigPath should honour DEBRID_CONFIG")
	}
	t.Setenv("DEBRID_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if DefaultConfigPath() != filepath.Join("/xdg", "debrid", "config.yaml") {
		t.Fatalf("xdg path: %s", DefaultConfigPath())
	}
}
