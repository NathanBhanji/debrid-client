package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

// Opaque per-write metadata lives in the existing settings KV table. All
// supported structured-settings writers rotate it atomically with the value.
const settingsRevisionKey = "_settings_revision_v1"

type SettingsReview struct {
	Settings Settings
	Revision string
}

func settingsReviewWithQueries(ctx context.Context, q *sqlcgen.Queries) (SettingsReview, error) {
	st, err := settingsWithQueries(ctx, q)
	if err != nil {
		return SettingsReview{}, err
	}
	nonce, err := q.GetSetting(ctx, settingsRevisionKey)
	if err != nil && !store.IsNotFound(err) {
		return SettingsReview{}, err
	}
	if err == nil && !domain.ValidSelectionRevision(nonce) {
		return SettingsReview{}, fmt.Errorf("invalid settings revision metadata")
	}
	b, err := json.Marshal(st)
	if err != nil {
		return SettingsReview{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("debrid-settings-review-v1\x00" + nonce + "\x00"))
	_, _ = hash.Write(b)
	return SettingsReview{Settings: st, Revision: hex.EncodeToString(hash.Sum(nil))}, nil
}

// GetSettingsReview takes one snapshot of settings and their write metadata.
// Reading an uninitialized database returns a stable default revision, with no
// inserted rows or nonce allocation.
func (s *Service) GetSettingsReview(ctx context.Context) (SettingsReview, error) {
	var out SettingsReview
	err := s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		out, err = settingsReviewWithQueries(ctx, q)
		return err
	})
	return out, err
}

// UpdateSettings preserves the legacy full-replacement contract, while every
// successful web/CLI/MCP write now invalidates an older native review.
func (s *Service) UpdateSettings(ctx context.Context, st Settings) (Settings, error) {
	out, err := s.updateSettings(ctx, st, "")
	return out.Settings, err
}
func (s *Service) UpdateSettingsReviewed(ctx context.Context, st Settings, revision string) (SettingsReview, error) {
	if !domain.ValidSelectionRevision(revision) {
		return SettingsReview{}, validationErr("invalid settings revision")
	}
	return s.updateSettings(ctx, st, revision)
}
func (s *Service) updateSettings(ctx context.Context, st Settings, expected string) (SettingsReview, error) {
	st, err := normalizeSettings(st)
	if err != nil {
		return SettingsReview{}, err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return SettingsReview{}, err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return SettingsReview{}, err
	}
	nonce := hex.EncodeToString(random[:])
	var out SettingsReview
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		if expected != "" {
			current, err := settingsReviewWithQueries(ctx, q)
			if err != nil {
				return err
			}
			if current.Revision != expected {
				return fmt.Errorf("%w: runtime settings changed; reload before saving", ErrPrecondition)
			}
		}
		now := store.FormatTime(s.now())
		if err := q.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{Key: settingsKey, Value: string(data), UpdatedAt: now}); err != nil {
			return err
		}
		if err := q.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{Key: settingsRevisionKey, Value: nonce, UpdatedAt: now}); err != nil {
			return err
		}
		var err error
		out, err = settingsReviewWithQueries(ctx, q)
		return err
	})
	if err != nil {
		return SettingsReview{}, err
	}
	s.events.Publish(events.Event{Type: events.SettingsChanged})
	return out, nil
}
