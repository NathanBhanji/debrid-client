package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

const settingsKey = "settings"

// Settings are the runtime-editable settings stored in the database (as
// opposed to config.Config which is file/env/flags and needs a restart).
type Settings struct {
	// TorrentDefaults are applied to new torrents unless overridden per add.
	TorrentDefaults domain.TorrentSettings `json:"torrent_defaults"`
	// Categories are known category names (sub-folders under the download dir).
	Categories []string `json:"categories"`
	// UnpackMaxDepth bounds nested archive extraction.
	UnpackMaxDepth int `json:"unpack_max_depth"`
}

// DefaultSettings returns the built-in settings.
func DefaultSettings() Settings {
	return Settings{TorrentDefaults: domain.DefaultTorrentSettings(), Categories: []string{}, UnpackMaxDepth: 1}
}

// GetSettings loads settings, falling back to defaults.
func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	raw, err := s.store.GetSetting(ctx, settingsKey)
	if err != nil {
		if store.IsNotFound(err) {
			return DefaultSettings(), nil
		}
		return Settings{}, err
	}
	st := DefaultSettings()
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return Settings{}, err
	}
	if st.Categories == nil {
		st.Categories = []string{}
	}
	return st, nil
}

// ValidateTorrentSettings checks filters, enums, durations and counters.
// Used for defaults, per-add overrides and per-torrent updates alike.
func ValidateTorrentSettings(ts domain.TorrentSettings) error {
	if err := domain.ValidateFilters(ts); err != nil {
		return validationErr("%v", err)
	}
	if ts.DownloadRetries < 0 || ts.TorrentRetries < 0 {
		return validationErr("retries must be >= 0")
	}
	switch ts.FinishedAction {
	case "", domain.FinishedKeep, domain.FinishedRemoveFromProvider:
	default:
		return validationErr("finished_action %q must be keep or remove_from_provider", ts.FinishedAction)
	}
	if ts.FinishedDelay < 0 || ts.DeleteOnError < 0 || ts.Lifetime < 0 {
		return validationErr("durations must be >= 0")
	}
	return nil
}

// UpdateSettings validates and stores settings (full replacement).
func (s *Service) UpdateSettings(ctx context.Context, st Settings) (Settings, error) {
	if err := ValidateTorrentSettings(st.TorrentDefaults); err != nil {
		return Settings{}, err
	}
	if st.TorrentDefaults.FinishedAction == "" {
		st.TorrentDefaults.FinishedAction = domain.FinishedKeep
	}
	if st.UnpackMaxDepth < 0 || st.UnpackMaxDepth > 5 {
		return Settings{}, validationErr("unpack_max_depth must be between 0 and 5")
	}
	seen := map[string]bool{}
	cats := make([]string, 0, len(st.Categories))
	for _, c := range st.Categories {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		if err := validateCategory(c); err != nil {
			return Settings{}, err
		}
		seen[c] = true
		cats = append(cats, c)
	}
	sort.Strings(cats)
	st.Categories = cats
	b, err := json.Marshal(st)
	if err != nil {
		return Settings{}, err
	}
	if err := s.store.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{Key: settingsKey, Value: string(b), UpdatedAt: store.FormatTime(s.now())}); err != nil {
		return Settings{}, err
	}
	s.events.Publish(events.Event{Type: events.SettingsChanged})
	return st, nil
}

// GetRaw / SetRaw store arbitrary string settings (e.g. the generated API key).
func (s *Service) GetRaw(ctx context.Context, key string) (string, bool, error) {
	v, err := s.store.GetSetting(ctx, key)
	if err != nil {
		if store.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

// SetRaw stores a raw setting. The key used for structured settings is reserved.
func (s *Service) SetRaw(ctx context.Context, key, value string) error {
	if key == settingsKey {
		return validationErr("%q is a reserved setting key", key)
	}
	return s.store.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{Key: key, Value: value, UpdatedAt: store.FormatTime(s.now())})
}

// validateCategory requires a plain folder name: what SanitizeName would
// produce unchanged, not hidden, not a path.
func validateCategory(c string) error {
	if c == "" || strings.HasPrefix(c, ".") || SanitizeName(c) != c {
		return validationErr("invalid category %q: must be a plain folder name", c)
	}
	return nil
}
