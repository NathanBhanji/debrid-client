package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NathanBhanji/debrid-client/internal/service"
)

type cancellationReviewOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         service.CancellationReview
}
type cancellationAcceptanceOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         service.CancellationAcceptance
}

func (h *Handler) registerCancellationRoutes(p string) {
	root := p + "/torrents/{id}"
	huma.Register(h.Huma, huma.Operation{OperationID: "review-torrent-cancellation", Method: http.MethodGet, Path: root + "/cancellation-review", Summary: "Review local transfer cancellation", Description: "Stops local orchestration and NAS work without removing records or files. Remote provider activity may continue. Revision binds the retry incarnation, not changing progress.", Tags: []string{"torrents"}, Middlewares: huma.Middlewares{retryNoStore}}, func(ctx context.Context, in *retryReviewIn) (*cancellationReviewOut, error) {
		view, err := h.svc.GetCancellationReview(ctx, in.ID)
		if err != nil {
			return nil, h.mapErr(err)
		}
		return &cancellationReviewOut{CacheControl: "no-store", Body: view}, nil
	})
	huma.Register(h.Huma, huma.Operation{OperationID: "request-torrent-cancellation", Method: http.MethodPost, Path: root + "/cancellation-requests", Summary: "Accept one durable local cancellation", Description: "Persist key and original revision before POST. Replays return the original receipt even after retry/removal. Acceptance means local workers drained and stopped state committed; provider activity may continue. Completed/error races return conflict without replacing the terminal result.", Tags: []string{"torrents"}, Middlewares: huma.Middlewares{retryNoStore}}, func(ctx context.Context, in *retryRequestIn) (*cancellationAcceptanceOut, error) {
		receipt, err := h.svc.CancelReviewed(ctx, in.ID, in.Body.Key, in.Body.Revision)
		if err != nil {
			return nil, h.mapErr(err)
		}
		return &cancellationAcceptanceOut{CacheControl: "no-store", Body: receipt}, nil
	})
	huma.Register(h.Huma, huma.Operation{OperationID: "get-torrent-cancellation-request", Method: http.MethodGet, Path: root + "/cancellation-requests/{key}", Summary: "Reconcile a saved cancellation without repeating it", Description: "404 means no committed receipt is visible, not proof an original request failed. Retry only the same immutable intent/key. A receipt remains accepted after later retry or removal and does not claim the transfer is currently stopped.", Tags: []string{"torrents"}, Middlewares: huma.Middlewares{retryNoStore}}, func(ctx context.Context, in *retryAcceptanceIn) (*cancellationAcceptanceOut, error) {
		receipt, err := h.svc.GetCancellationAcceptance(ctx, in.ID, in.Key, in.Revision)
		if err != nil {
			return nil, h.mapErr(err)
		}
		return &cancellationAcceptanceOut{CacheControl: "no-store", Body: receipt}, nil
	})
}
