package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

// Explicit absence is a typed successful response. A missing HTTP route is not
// evidence that a transfer disappeared or that an earlier removal succeeded.
type RemovalReview struct {
	TorrentID         string `json:"torrent_id"`
	Exists            bool   `json:"exists"`
	Revision          string `json:"revision,omitempty"`
	ProviderCopyKnown bool   `json:"provider_copy_known"`
}

func (s *Service) GetRemovalReview(ctx context.Context, id string) (RemovalReview, error) {
	if id == "" || len(id) > 256 || strings.ContainsAny(id, "\r\n\x00") {
		return RemovalReview{}, validationErr("invalid exact transfer id")
	}
	result := RemovalReview{TorrentID: id}
	err := s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		view, err := cancellationReview(ctx, q, id)
		if store.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		row, err := q.GetTorrent(ctx, id)
		if err != nil {
			return err
		}
		result.Exists = true
		result.Revision = view.Revision
		result.ProviderCopyKnown = row.ProviderID != ""
		return nil
	})
	return result, err
}
func (s *Service) requireRemovalRevision(ctx context.Context, id, revision string) error {
	if revision == "" {
		return nil
	}
	if !domain.ValidSelectionRevision(revision) {
		return validationErr("removal revision must be64lowercase hexadecimal characters")
	}
	view, err := s.GetRemovalReview(ctx, id)
	if err != nil {
		return err
	}
	if !view.Exists {
		return fmt.Errorf("%w: removal transfer", ErrNotFound)
	}
	if view.Revision != revision {
		return fmt.Errorf("%w: removal incarnation changed; review again", ErrPrecondition)
	}
	return nil
}
