package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

// Cancellation stops local orchestration; it neither pauses nor deletes the
// remote provider copy, and does not remove files/records.
type CancellationReview struct {
	TorrentID           string `json:"torrent_id"`
	Revision            string `json:"revision"`
	Allowed             bool   `json:"allowed"`
	ProviderMayContinue bool   `json:"provider_may_continue"`
}
type CancellationAcceptance struct {
	TorrentID     string    `json:"torrent_id"`
	AcceptedAt    time.Time `json:"accepted_at"`
	TorrentExists bool      `json:"torrent_exists"`
}

// Reuse the receipt primitives, with a disjoint key/fingerprint namespace.
// Cancellation can never replay an add/retry mutation or vice versa.
func cancellationIdentity(id, key, revision string) (*retryAttempt, error) {
	attempt, err := retryIdentity(RetryWholeTorrent, id, key, revision)
	if err != nil {
		return nil, err
	}
	base, err := submissionKeyHash(key)
	if err != nil {
		return nil, err
	}
	keyDigest := sha256.Sum256([]byte("native-cancellation-v1:" + base))
	payload, _ := json.Marshal([]string{"native-cancellation-v1", id, revision})
	requestDigest := sha256.Sum256(payload)
	attempt.keyHash = hex.EncodeToString(keyDigest[:])
	attempt.requestHash = hex.EncodeToString(requestDigest[:])
	return attempt, nil
}
func cancellationReceipt(a *retryAttempt) CancellationAcceptance {
	return CancellationAcceptance{TorrentID: a.acceptance.TorrentID, AcceptedAt: a.acceptance.AcceptedAt, TorrentExists: a.acceptance.TorrentExists}
}
func cancellationReview(ctx context.Context, q *sqlcgen.Queries, id string) (CancellationReview, error) {
	row, err := q.GetTorrent(ctx, id)
	if err != nil {
		return CancellationReview{}, err
	}
	t, err := store.TorrentFromRow(row)
	if err != nil {
		return CancellationReview{}, err
	}
	fence, err := q.CountTorrentSubmissions(ctx, t.ID)
	if err != nil {
		return CancellationReview{}, err
	}
	// This is an incarnation fence, deliberately not a progress/timestamp digest.
	// Draining fetch/unpack/add workers changes child states and provider metadata.
	payload, _ := json.Marshal(struct {
		Version             int
		ID, AccountID, Hash string
		AddedAt             time.Time
		RetryCount          int
		Fence               int64
	}{1, t.ID, t.AccountID, t.Hash, t.AddedAt, t.RetryCount, fence})
	digest := sha256.Sum256(payload)
	return CancellationReview{TorrentID: t.ID, Revision: hex.EncodeToString(digest[:]), Allowed: t.Status != domain.TorrentCancelled && t.Status.CanTransition(domain.TorrentCancelled), ProviderMayContinue: true}, nil
}
func cancellationError(err error) error {
	if store.IsNotFound(err) {
		return fmt.Errorf("%w: cancellation transfer", ErrNotFound)
	}
	return err
}
func (s *Service) GetCancellationReview(ctx context.Context, id string) (CancellationReview, error) {
	var view CancellationReview
	err := s.store.WithTx(ctx, func(q *sqlcgen.Queries) error { var err error; view, err = cancellationReview(ctx, q, id); return err })
	return view, cancellationError(err)
}
func (s *Service) GetCancellationAcceptance(ctx context.Context, id, key, revision string) (CancellationAcceptance, error) {
	attempt, err := cancellationIdentity(id, key, revision)
	if err != nil {
		return CancellationAcceptance{}, err
	}
	found, err := attempt.lookup(ctx, s.store.Queries)
	if err != nil {
		return CancellationAcceptance{}, err
	}
	if !found {
		return CancellationAcceptance{}, fmt.Errorf("%w: no accepted cancellation", ErrNotFound)
	}
	return cancellationReceipt(attempt), nil
}
func (s *Service) CancelReviewed(ctx context.Context, id, key, revision string) (CancellationAcceptance, error) {
	attempt, err := cancellationIdentity(id, key, revision)
	if err != nil {
		return CancellationAcceptance{}, err
	}
	if found, err := attempt.lookup(ctx, s.store.Queries); err != nil {
		return CancellationAcceptance{}, err
	} else if found {
		return cancellationReceipt(attempt), nil
	}
	changed := false
	err = s.engine.WithTorrentOperation(ctx, id, func() error {
		if found, err := attempt.lookup(ctx, s.store.Queries); err != nil {
			return err
		} else if found {
			return nil
		}
		review, err := s.GetCancellationReview(ctx, id)
		if err != nil || !review.Allowed || review.Revision != revision {
			if found, lookupErr := attempt.lookup(ctx, s.store.Queries); lookupErr != nil {
				return lookupErr
			} else if found {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: cancellation review changed", ErrConflict)
		}
		if err := s.engine.CancelTorrent(ctx, id); err != nil {
			return fmt.Errorf("stop transfer work: %w", err)
		}
		err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
			if found, err := attempt.lookup(ctx, q); err != nil {
				return err
			} else if found {
				return errRetryReplayed
			}
			current, err := cancellationReview(ctx, q, id)
			if err != nil {
				return err
			}
			if !current.Allowed || current.Revision != revision {
				return fmt.Errorf("%w: transfer finished or cancellation incarnation changed while stopping", ErrConflict)
			}
			row, err := q.GetTorrent(ctx, id)
			if err != nil {
				return err
			}
			t, err := store.TorrentFromRow(row)
			if err != nil {
				return err
			}
			now := s.now()
			if err := t.Transition(domain.TorrentCancelled, "cancelled locally; provider activity may continue"); err != nil {
				return err
			}
			t.Speed = 0
			t.RetryAt = nil
			t.CompletedAt = &now
			t.UpdatedAt = now
			downloads, err := q.ListDownloadsForTorrent(ctx, id)
			if err != nil {
				return err
			}
			for _, row := range downloads {
				d, err := store.DownloadFromRow(row)
				if err != nil {
					return err
				}
				if d.State.IsTerminal() {
					continue
				}
				if err := d.Transition(domain.DownloadCancelled); err != nil {
					return err
				}
				d.CompletedAt = &now
				d.UpdatedAt = now
				if err := q.UpdateDownload(ctx, store.DownloadUpdateParams(d)); err != nil {
					return err
				}
			}
			params, err := store.TorrentUpdateParams(t)
			if err != nil {
				return err
			}
			if err := q.UpdateTorrent(ctx, params); err != nil {
				return err
			}
			return attempt.record(ctx, q, id, now)
		})
		if errors.Is(err, errRetryReplayed) {
			return nil
		}
		if err == nil {
			changed = true
		}
		return err
	})
	// Independent SQLite callers can commit the same key between validation and
	// our transaction. A durable receipt resolves that race before a conflict.
	if err != nil {
		if found, lookupErr := attempt.lookup(ctx, s.store.Queries); lookupErr == nil && found {
			return cancellationReceipt(attempt), nil
		}
		return CancellationAcceptance{}, cancellationError(err)
	}
	if changed {
		s.events.Publish(events.Event{Type: events.TorrentUpdated, TorrentID: id})
	}
	return cancellationReceipt(attempt), nil
}
