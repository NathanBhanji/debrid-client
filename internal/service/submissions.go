package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

type TorrentConflictCode string

const (
	ActiveTorrent      TorrentConflictCode = "active_torrent"
	SubmissionMismatch TorrentConflictCode = "idempotency_mismatch"
	SubmissionRemoved  TorrentConflictCode = "submission_removed"
)

type TorrentConflict struct {
	Code      TorrentConflictCode
	TorrentID string
}

func (e *TorrentConflict) Error() string {
	switch e.Code {
	case ActiveTorrent:
		return "an active torrent already exists on this account"
	case SubmissionMismatch:
		return "this submission key was already accepted with different input"
	case SubmissionRemoved:
		return "this submission was accepted, but its torrent has been removed"
	default:
		return "torrent conflict"
	}
}
func (e *TorrentConflict) Unwrap() error {
	if e.Code == SubmissionMismatch {
		return ErrValidation
	}
	return ErrConflict
}

// TorrentSubmission is an accepted local enqueue, not proof of provider success.
// Receipts survive deletion and are never implicitly expired/reused.
type TorrentSubmission struct {
	TorrentID     string
	CreatedAt     time.Time
	TorrentExists bool
}

func submissionKeyHash(key string) (string, error) {
	if len(key) == 38 && key[0] == '"' && key[37] == '"' {
		key = key[1:37]
	}
	if len(key) != 36 {
		return "", validationErr("submission key must be a UUIDv4")
	}
	id, err := uuid.Parse(key)
	if err != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 {
		return "", validationErr("submission key must be a UUIDv4")
	}
	digest := sha256.Sum256([]byte(id.String()))
	return hex.EncodeToString(digest[:]), nil
}
func submissionIdentity(in AddTorrentInput, meta domain.Torrent) (string, string, error) {
	if in.SubmissionKey == "" {
		return "", "", nil
	}
	key, err := submissionKeyHash(in.SubmissionKey)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(meta.Payload)
	// Stable submitted intent, independent of default/account changes after acceptance.
	// The payload digest covers all original bytes, not just the BitTorrent info hash.
	intent := struct {
		Version  int
		Kind     string
		Payload  string
		Account  string
		Category string
		Settings *domain.TorrentSettings
	}{1, string(meta.PayloadKind), hex.EncodeToString(digest[:]), in.Account, in.Category, in.Settings}
	data, err := json.Marshal(intent)
	if err != nil {
		return "", "", err
	}
	fingerprint := sha256.Sum256(data)
	return key, hex.EncodeToString(fingerprint[:]), nil
}
func (s *Service) GetTorrentSubmission(ctx context.Context, key string) (TorrentSubmission, error) {
	hash, err := submissionKeyHash(key)
	if err != nil {
		return TorrentSubmission{}, err
	}
	row, err := s.store.GetTorrentSubmission(ctx, hash)
	if store.IsNotFound(err) {
		return TorrentSubmission{}, fmt.Errorf("%w: no committed submission", ErrNotFound)
	}
	if err != nil {
		return TorrentSubmission{}, err
	}
	at, err := store.ParseTime(row.CreatedAt)
	if err != nil {
		return TorrentSubmission{}, err
	}
	return TorrentSubmission{TorrentID: row.TorrentID, CreatedAt: at, TorrentExists: row.TorrentExists}, nil
}
func checkActiveTorrent(ctx context.Context, q *sqlcgen.Queries, account, hash, ignore string) error {
	rows, err := q.ListTorrentDuplicates(ctx, sqlcgen.ListTorrentDuplicatesParams{AccountID: account, Hash: strings.ToLower(hash)})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != ignore && !domain.TorrentStatus(row.Status).IsTerminal() {
			return &TorrentConflict{Code: ActiveTorrent, TorrentID: row.ID}
		}
	}
	return nil
}
func torrentDetailWithQueries(ctx context.Context, q *sqlcgen.Queries, id string) (TorrentDetail, error) {
	row, err := q.GetTorrent(ctx, id)
	if err != nil {
		return TorrentDetail{}, err
	}
	t, err := store.TorrentFromRow(row)
	if err != nil {
		return TorrentDetail{}, err
	}
	rows, err := q.ListDownloadsForTorrent(ctx, id)
	if err != nil {
		return TorrentDetail{}, err
	}
	out := TorrentDetail{Torrent: t, Downloads: make([]domain.Download, 0, len(rows))}
	for _, row := range rows {
		d, err := store.DownloadFromRow(row)
		if err != nil {
			return TorrentDetail{}, err
		}
		out.Downloads = append(out.Downloads, d)
	}
	return out, nil
}
