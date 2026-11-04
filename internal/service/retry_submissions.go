package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

type RetryKind string

const (
	RetryWholeTorrent RetryKind = "torrent"
	RetryOneDownload  RetryKind = "download"
)

type RetryReview struct {
	Kind             RetryKind `json:"kind"`
	ResourceID       string    `json:"resource_id"`
	TorrentID        string    `json:"torrent_id"`
	Revision         string    `json:"revision"`
	Allowed          bool      `json:"allowed"`
	WaitForSelection bool      `json:"wait_for_selection"`
}

// Acceptance is a durable local mutation receipt, not proof of eventual success.
type RetryAcceptance struct {
	Kind          RetryKind `json:"kind"`
	ResourceID    string    `json:"resource_id"`
	TorrentID     string    `json:"torrent_id"`
	AcceptedAt    time.Time `json:"accepted_at"`
	TorrentExists bool      `json:"torrent_exists"`
}
type retryAttempt struct {
	kind                                       RetryKind
	resourceID, revision, keyHash, requestHash string
	acceptance                                 RetryAcceptance
}

var errRetryReplayed = errors.New("retry receipt replayed")

func retryIdentity(kind RetryKind, id, key, revision string) (*retryAttempt, error) {
	if kind != RetryWholeTorrent && kind != RetryOneDownload {
		return nil, validationErr("invalid retry kind")
	}
	if id == "" || len(id) > 256 || strings.ContainsAny(id, "\r\n\x00") {
		return nil, validationErr("invalid retry resource")
	}
	if !domain.ValidSelectionRevision(revision) {
		return nil, validationErr("retry revision must be64lowercase hexadecimal characters")
	}
	base, err := submissionKeyHash(key)
	if err != nil {
		return nil, err
	}
	// A separate key domain cannot be read/replayed as an add submission.
	keyDigest := sha256.Sum256([]byte("native-retry-v1:" + base))
	data, _ := json.Marshal([]string{"native-retry-v1", string(kind), id, revision})
	requestDigest := sha256.Sum256(data)
	return &retryAttempt{kind: kind, resourceID: id, revision: revision, keyHash: hex.EncodeToString(keyDigest[:]), requestHash: hex.EncodeToString(requestDigest[:])}, nil
}
func (a *retryAttempt) lookup(ctx context.Context, q *sqlcgen.Queries) (bool, error) {
	row, err := q.GetTorrentSubmission(ctx, a.keyHash)
	if store.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.RequestHash != a.requestHash {
		return false, &TorrentConflict{Code: SubmissionMismatch}
	}
	at, err := store.ParseTime(row.CreatedAt)
	if err != nil {
		return false, err
	}
	a.acceptance = RetryAcceptance{Kind: a.kind, ResourceID: a.resourceID, TorrentID: row.TorrentID, AcceptedAt: at, TorrentExists: row.TorrentExists}
	return true, nil
}
func (a *retryAttempt) record(ctx context.Context, q *sqlcgen.Queries, parent string, now time.Time) error {
	if err := q.InsertTorrentSubmission(ctx, sqlcgen.InsertTorrentSubmissionParams{KeyHash: a.keyHash, RequestHash: a.requestHash, TorrentID: parent, CreatedAt: store.FormatTime(now)}); err != nil {
		return err
	}
	a.acceptance = RetryAcceptance{Kind: a.kind, ResourceID: a.resourceID, TorrentID: parent, AcceptedAt: now, TorrentExists: true}
	return nil
}

// Including a receipt count prevents a stale review becoming valid again when
// a download's automatic retry counter resets or timestamps happen to repeat.
// Receipts are never implicitly pruned. Any accepted retry on this parent
// conservatively invalidates its other outstanding reviews.
func retryReview(ctx context.Context, q *sqlcgen.Queries, kind RetryKind, id string) (RetryReview, error) {
	var t domain.Torrent
	var d *domain.Download
	switch kind {
	case RetryWholeTorrent:
		row, err := q.GetTorrent(ctx, id)
		if err != nil {
			return RetryReview{}, err
		}
		value, err := store.TorrentFromRow(row)
		if err != nil {
			return RetryReview{}, err
		}
		t = value
	case RetryOneDownload:
		row, err := q.GetDownload(ctx, id)
		if err != nil {
			return RetryReview{}, err
		}
		value, err := store.DownloadFromRow(row)
		if err != nil {
			return RetryReview{}, err
		}
		d = &value
		parent, err := q.GetTorrent(ctx, value.TorrentID)
		if err != nil {
			return RetryReview{}, err
		}
		t, err = store.TorrentFromRow(parent)
		if err != nil {
			return RetryReview{}, err
		}
	default:
		return RetryReview{}, validationErr("invalid retry kind")
	}
	fence, err := q.CountTorrentSubmissions(ctx, t.ID)
	if err != nil {
		return RetryReview{}, err
	}
	// Only a digest is exposed: provider URLs/errors and stored settings stay private.
	payload := struct {
		Version                          int
		Kind                             RetryKind
		ResourceID                       string
		Fence                            int64
		TorrentID, AccountID, ProviderID string
		Status                           domain.TorrentStatus
		RetryCount                       int
		UpdatedAt                        time.Time
		CompletedAt                      *time.Time
		Settings                         domain.TorrentSettings
		Download                         *domain.Download
	}{1, kind, id, fence, t.ID, t.AccountID, t.ProviderID, t.Status, t.RetryCount, t.UpdatedAt, t.CompletedAt, t.Settings, d}
	data, err := json.Marshal(payload)
	if err != nil {
		return RetryReview{}, err
	}
	digest := sha256.Sum256(data)
	allowed := t.Status.IsTerminal()
	if d != nil {
		allowed = d.State == domain.DownloadError && (t.Status == domain.TorrentError || t.Status == domain.TorrentFinished) && t.SelectionReady()
	}
	if allowed {
		if err := checkActiveTorrent(ctx, q, t.AccountID, t.Hash, t.ID); err != nil {
			if !errors.Is(err, ErrConflict) {
				return RetryReview{}, err
			}
			allowed = false
		}
	}
	return RetryReview{Kind: kind, ResourceID: id, TorrentID: t.ID, Revision: hex.EncodeToString(digest[:]), Allowed: allowed, WaitForSelection: t.Settings.WaitForSelection}, nil
}
func (s *Service) GetRetryReview(ctx context.Context, kind RetryKind, id string) (RetryReview, error) {
	var view RetryReview
	err := s.store.WithTx(ctx, func(q *sqlcgen.Queries) error { var err error; view, err = retryReview(ctx, q, kind, id); return err })
	if store.IsNotFound(err) {
		return RetryReview{}, fmt.Errorf("%w: retry resource", ErrNotFound)
	}
	return view, err
}
func (s *Service) GetRetryAcceptance(ctx context.Context, kind RetryKind, id, key, revision string) (RetryAcceptance, error) {
	attempt, err := retryIdentity(kind, id, key, revision)
	if err != nil {
		return RetryAcceptance{}, err
	}
	found, err := attempt.lookup(ctx, s.store.Queries)
	if err != nil {
		return RetryAcceptance{}, err
	}
	if !found {
		return RetryAcceptance{}, fmt.Errorf("%w: no accepted retry", ErrNotFound)
	}
	return attempt.acceptance, nil
}
func (a *retryAttempt) validate(ctx context.Context, q *sqlcgen.Queries) error {
	if found, err := a.lookup(ctx, q); err != nil {
		return err
	} else if found {
		return errRetryReplayed
	}
	view, err := retryReview(ctx, q, a.kind, a.resourceID)
	if err != nil {
		return err
	}
	if !view.Allowed || view.Revision != a.revision {
		return fmt.Errorf("%w: retry review changed; review again", ErrConflict)
	}
	return nil
}
func (s *Service) RetryReviewed(ctx context.Context, kind RetryKind, id, key, revision string) (RetryAcceptance, error) {
	attempt, err := retryIdentity(kind, id, key, revision)
	if err != nil {
		return RetryAcceptance{}, err
	}
	if found, err := attempt.lookup(ctx, s.store.Queries); err != nil {
		return RetryAcceptance{}, err
	} else if found {
		return attempt.acceptance, nil
	}
	view, err := s.GetRetryReview(ctx, kind, id)
	if err != nil {
		if found, lookupErr := attempt.lookup(ctx, s.store.Queries); lookupErr != nil {
			return RetryAcceptance{}, lookupErr
		} else if found {
			return attempt.acceptance, nil
		}
		return RetryAcceptance{}, err
	}
	err = s.engine.WithTorrentOperation(ctx, view.TorrentID, func() error {
		// Receipt replay precedes all resource/state checks and engine cancellation.
		if found, err := attempt.lookup(ctx, s.store.Queries); err != nil {
			return err
		} else if found {
			return nil
		}
		current, err := s.GetRetryReview(ctx, kind, id)
		if err != nil || !current.Allowed || current.Revision != revision {
			// A concurrent caller may have committed this same key between the
			// preceding lookup and review. Resolve that receipt before rejecting.
			if found, lookupErr := attempt.lookup(ctx, s.store.Queries); lookupErr != nil {
				return lookupErr
			} else if found {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: retry review changed; review again", ErrConflict)
		}
		if kind == RetryWholeTorrent {
			if err := s.engine.CancelTorrent(ctx, view.TorrentID); err != nil {
				return err
			}
			_, err = s.retryTorrentStopped(ctx, id, attempt)
		} else {
			_, err = s.retryDownload(ctx, id, attempt)
		}
		if errors.Is(err, errRetryReplayed) {
			return nil
		}
		return err
	})
	if store.IsNotFound(err) {
		return RetryAcceptance{}, fmt.Errorf("%w: retry resource", ErrNotFound)
	}
	return attempt.acceptance, err
}

// Legacy file retries also advance the fence: resetting their public counter
// must not make an earlier native review valid for a later failure.
func recordLegacyRetryFence(ctx context.Context, q *sqlcgen.Queries, parent, id string, now time.Time) error {
	key := sha256.Sum256([]byte("legacy-download-retry-v1:" + uuid.NewString()))
	request := sha256.Sum256([]byte("legacy-download-retry-v1:" + id))
	return q.InsertTorrentSubmission(ctx, sqlcgen.InsertTorrentSubmissionParams{KeyHash: hex.EncodeToString(key[:]), RequestHash: hex.EncodeToString(request[:]), TorrentID: parent, CreatedAt: store.FormatTime(now)})
}
