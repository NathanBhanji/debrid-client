// Package config loads the application configuration from defaults, an
// optional YAML file, environment variables (DEBRID_*) and command-line flags,
// in that order of increasing precedence.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/pflag"
)

// EnvPrefix is the prefix for environment variable overrides, e.g.
// DEBRID_SERVER__LISTEN=0.0.0.0:8080. A double underscore separates nesting
// levels so that keys containing single underscores remain unambiguous.
const EnvPrefix = "DEBRID_"

const delim = "."

// Config is the fully resolved application configuration.
type Config struct {
	// DataDir holds the SQLite database and other state.
	DataDir string `koanf:"data_dir" yaml:"data_dir"`
	// DownloadDir is where completed files are written. Defaults to <data_dir>/downloads.
	DownloadDir string `koanf:"download_dir" yaml:"download_dir"`

	Server Server `koanf:"server" yaml:"server"`
	Log    Log    `koanf:"log" yaml:"log"`
	Engine Engine `koanf:"engine" yaml:"engine"`
}

// Server configures the HTTP API server.
type Server struct {
	// Listen is the host:port the API listens on.
	Listen string `koanf:"listen" yaml:"listen"`
	// BasePath is an optional URL prefix (e.g. "/debrid") for reverse proxies.
	BasePath string `koanf:"base_path" yaml:"base_path"`
	// APIKey protects the API. If empty, one is generated on first start and
	// persisted in the database.
	APIKey string `koanf:"api_key" yaml:"api_key"`
}

// Log configures logging.
type Log struct {
	Level  string `koanf:"level" yaml:"level"`   // debug|info|warn|error
	Format string `koanf:"format" yaml:"format"` // text|json
}

// Engine configures the download engine.
type Engine struct {
	// DownloadLimit is the max number of files downloading concurrently.
	DownloadLimit int `koanf:"download_limit" yaml:"download_limit"`
	// UnpackLimit is the max number of archives extracting concurrently (0 disables unpacking).
	UnpackLimit int `koanf:"unpack_limit" yaml:"unpack_limit"`
	// PollInterval is how often each provider account is polled while torrents are active.
	PollInterval time.Duration `koanf:"poll_interval" yaml:"poll_interval"`
	// IdlePollInterval is the poll interval when nothing is active.
	IdlePollInterval time.Duration `koanf:"idle_poll_interval" yaml:"idle_poll_interval"`
	// ConnectionsPerDownload is the number of parallel HTTP connections per file.
	ConnectionsPerDownload int `koanf:"connections_per_download" yaml:"connections_per_download"`
	// MaxSpeed caps total download bandwidth in bytes/sec (0 = unlimited).
	MaxSpeed int64 `koanf:"max_speed" yaml:"max_speed"`
}

// Default returns the built-in defaults. Paths are resolved relative to the
// user's XDG directories (or /data when running in a container with DATA_DIR unset
// is intentionally NOT assumed — set data_dir explicitly in containers).
func Default() Config {
	return Config{
		DataDir: defaultDataDir(),
		// DownloadDir is derived from DataDir when empty; see Config.Derived.
		Server: Server{
			Listen: "127.0.0.1:8080",
		},
		Log: Log{Level: "info", Format: "text"},
		Engine: Engine{
			DownloadLimit:          2,
			UnpackLimit:            1,
			PollInterval:           10 * time.Second,
			IdlePollInterval:       30 * time.Second,
			ConnectionsPerDownload: 8,
		},
	}
}

// DefaultConfigPath returns the default location of the YAML config file:
// $DEBRID_CONFIG if set, else $XDG_CONFIG_HOME/debrid/config.yaml
// (~/.config/debrid/config.yaml).
func DefaultConfigPath() string {
	p, _ := defaultConfigPath(os.Environ)
	return p
}

// defaultConfigPath also reports whether the path was explicitly chosen via
// DEBRID_CONFIG (in which case a missing file is an error).
func defaultConfigPath(environ func() []string) (path string, explicit bool) {
	if v := lookupEnv(environ, EnvPrefix+"CONFIG"); v != "" {
		return v, true
	}
	return filepath.Join(userConfigDir(), "debrid", "config.yaml"), false
}

func lookupEnv(environ func() []string, key string) string {
	for _, kv := range environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func (o Options) environ() func() []string {
	if o.Environ != nil {
		return o.Environ
	}
	return os.Environ
}

// stripNils removes nil-valued entries recursively.
func stripNils(m map[string]any) {
	for k, v := range m {
		switch vv := v.(type) {
		case nil:
			delete(m, k)
		case map[string]any:
			stripNils(vv)
		}
	}
}

func defaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "debrid")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "./data"
	}
	return filepath.Join(home, ".local", "share", "debrid")
}

// userConfigDir follows XDG on every platform (consistent with defaultDataDir),
// so config and data live side by side under ~/.config and ~/.local/share.
func userConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return filepath.Join(home, ".config")
}

// Options controls how Load resolves configuration.
type Options struct {
	// File is the YAML file to load. Empty means DefaultConfigPath(); a missing
	// default file is ignored, a missing explicitly-set file is an error.
	File string
	// FileExplicit marks File as user-provided (so a missing file is an error).
	FileExplicit bool
	// Flags, if non-nil, overrides values from flags that were explicitly set.
	// Flag names must match koanf keys with "." replaced by "-" (e.g. server-listen).
	Flags *pflag.FlagSet
	// Environ overrides os.Environ (for tests).
	Environ func() []string
}

// Load resolves the configuration: defaults → file → env → flags, then validates.
func Load(opts Options) (Config, error) {
	k := koanf.New(delim)

	if err := k.Load(structs.Provider(Default(), "koanf"), nil); err != nil {
		return Config{}, fmt.Errorf("defaults: %w", err)
	}

	path := opts.File
	explicit := opts.FileExplicit
	if path == "" {
		path, explicit = defaultConfigPath(opts.environ())
	}
	if raw, err := os.ReadFile(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) || explicit {
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		}
	} else {
		m, err := yaml.Parser().Unmarshal(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		}
		// A section left as `engine:` with all children commented out parses as
		// nil; dropping nils keeps the defaults instead of wiping the section.
		stripNils(m)
		if err := k.Load(confmap.Provider(m, delim), nil); err != nil {
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		}
	}

	if err := k.Load(env.Provider(delim, env.Opt{
		Prefix:      EnvPrefix,
		EnvironFunc: opts.environ(),
		TransformFunc: func(key, value string) (string, any) {
			key = strings.TrimPrefix(key, EnvPrefix)
			switch key {
			case "CONFIG", "API_KEY", "SERVER":
				// DEBRID_CONFIG selects the file; DEBRID_API_KEY / DEBRID_SERVER are
				// CLI client settings (which server to talk to), not server config.
				return "", nil
			}
			key = strings.ToLower(strings.ReplaceAll(key, "__", delim))
			return key, value
		},
	}), nil); err != nil {
		return Config{}, fmt.Errorf("env: %w", err)
	}

	if opts.Flags != nil {
		if err := k.Load(posflag.ProviderWithFlag(opts.Flags, delim, k, func(f *pflag.Flag) (string, any) {
			key, ok := flagKeys[f.Name]
			if !ok {
				return "", nil // not a config flag (e.g. --config, --help)
			}
			return key, posflag.FlagVal(opts.Flags, f)
		}), nil); err != nil {
			return Config{}, fmt.Errorf("flags: %w", err)
		}
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{Tag: "koanf", DecoderConfig: &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
			mapstructure.TextUnmarshallerHookFunc(),
		),
		Metadata:         nil,
		Result:           &cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      true, // reject typos like dowload_limit or DEBRID_ENGINE__DOWNLOD_LIMIT
	}}); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	cfg = cfg.Derived()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Derived fills in values that default relative to other values
// (currently DownloadDir → <DataDir>/downloads).
func (c Config) Derived() Config {
	if c.DownloadDir == "" {
		c.DownloadDir = filepath.Join(c.DataDir, "downloads")
	}
	return c
}

// Validate checks the configuration for invalid values.
func (c Config) Validate() error {
	var errs []error
	if c.DataDir == "" {
		errs = append(errs, errors.New("data_dir must not be empty"))
	}
	if c.Server.Listen == "" {
		errs = append(errs, errors.New("server.listen must not be empty"))
	}
	if c.Server.BasePath != "" && !strings.HasPrefix(c.Server.BasePath, "/") {
		errs = append(errs, errors.New("server.base_path must start with /"))
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level %q must be one of debug|info|warn|error", c.Log.Level))
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("log.format %q must be text|json", c.Log.Format))
	}
	if c.Engine.DownloadLimit < 1 {
		errs = append(errs, errors.New("engine.download_limit must be >= 1"))
	}
	if c.Engine.UnpackLimit < 0 {
		errs = append(errs, errors.New("engine.unpack_limit must be >= 0"))
	}
	if c.Engine.PollInterval < time.Second {
		errs = append(errs, errors.New("engine.poll_interval must be >= 1s"))
	}
	if c.Engine.IdlePollInterval < c.Engine.PollInterval {
		errs = append(errs, errors.New("engine.idle_poll_interval must be >= engine.poll_interval"))
	}
	if c.Engine.ConnectionsPerDownload < 1 {
		errs = append(errs, errors.New("engine.connections_per_download must be >= 1"))
	}
	if c.Engine.MaxSpeed < 0 {
		errs = append(errs, errors.New("engine.max_speed must be >= 0"))
	}
	return errors.Join(errs...)
}

// Redacted returns a copy with secrets masked, for display.
func (c Config) Redacted() Config {
	if c.Server.APIKey != "" {
		c.Server.APIKey = "********"
	}
	return c
}

// flagKeys maps command-line flag names to koanf keys. Only these flags are
// read by Load; BindFlags registers exactly this set.
var flagKeys = map[string]string{
	"data-dir":         "data_dir",
	"download-dir":     "download_dir",
	"server-listen":    "server.listen",
	"server-base-path": "server.base_path",
	"log-level":        "log.level",
	"log-format":       "log.format",
}

// BindFlags registers the subset of settings that make sense as command-line
// flags (see flagKeys).
func BindFlags(fs *pflag.FlagSet) {
	d := Default()
	fs.String("data-dir", d.DataDir, "directory for the database and state")
	fs.String("download-dir", "", "directory for downloaded files (default <data-dir>/downloads)")
	fs.String("server-listen", d.Server.Listen, "address for the API server")
	fs.String("server-base-path", d.Server.BasePath, "URL prefix for the API server")
	fs.String("log-level", d.Log.Level, "log level (debug|info|warn|error)")
	fs.String("log-format", d.Log.Format, "log format (text|json)")
}

// WriteDefaultFile writes a commented default config to path, failing if it exists.
func WriteDefaultFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(defaultYAML()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func defaultYAML() string {
	d := Default().Derived()
	return fmt.Sprintf(`# debrid-client configuration.
# Every key can also be set via environment variables: DEBRID_<SECTION>__<KEY>,
# e.g. DEBRID_SERVER__LISTEN=0.0.0.0:8080, DEBRID_ENGINE__DOWNLOAD_LIMIT=4.

# Directory for the SQLite database and state (default: XDG data dir).
# data_dir: %q
# Directory for downloaded files (default: <data_dir>/downloads).
# download_dir: %q

server:
  listen: %q
  # base_path: /debrid
  # api_key: ""   # generated on first start if empty

log:
  level: %s      # debug|info|warn|error
  format: %s     # text|json

engine:
  download_limit: %d            # concurrent file downloads
  unpack_limit: %d              # concurrent archive extractions (0 disables)
  poll_interval: %s            # provider polling while active
  idle_poll_interval: %s       # provider polling while idle
  connections_per_download: %d  # parallel connections per file
  max_speed: %d                 # bytes/sec, 0 = unlimited
`, d.DataDir, d.DownloadDir, d.Server.Listen, d.Log.Level, d.Log.Format,
		d.Engine.DownloadLimit, d.Engine.UnpackLimit, d.Engine.PollInterval, d.Engine.IdlePollInterval,
		d.Engine.ConnectionsPerDownload, d.Engine.MaxSpeed)
}
