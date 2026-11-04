package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

type retryReviewIn struct {
	ID string `path:"id" maxLength:"256" doc:"Exact resource id returned by the API (not a prefix/hash alias)"`
}
type retryRequestIn struct {
	ID   string `path:"id" maxLength:"256"`
	Body struct {
		Key      string `json:"key" minLength:"36" maxLength:"38" doc:"Fresh UUIDv4 persisted before POST; reuse only for this immutable retry intent"`
		Revision string `json:"revision" minLength:"64" maxLength:"64" pattern:"^[a-f0-9]{64}$"`
	}
}
type retryAcceptanceIn struct {
	ID       string `path:"id" maxLength:"256"`
	Key      string `path:"key" maxLength:"38"`
	Revision string `query:"revision" required:"true" minLength:"64" maxLength:"64" pattern:"^[a-f0-9]{64}$" doc:"Original review revision bound to this request key"`
}
type retryReviewOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         apimodel.RetryReview
}
type retryAcceptanceOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         apimodel.RetryAcceptance
}

func (h *Handler) registerRetryRoutes(p string) {
	for _, resource := range []struct {
		path, singular string
		kind           service.RetryKind
	}{{"torrents", "torrent", service.RetryWholeTorrent}, {"downloads", "download", service.RetryOneDownload}} {
		kind := resource.kind
		root := p + "/" + resource.path + "/{id}"
		huma.Register(h.Huma, huma.Operation{OperationID: "review-" + resource.singular + "-retry", Method: http.MethodGet, Path: root + "/retry-review", Summary: "Review retry eligibility and immutable revision", Tags: []string{"torrents"}, Middlewares: huma.Middlewares{retryNoStore}}, func(ctx context.Context, in *retryReviewIn) (*retryReviewOut, error) {
			view, err := h.svc.GetRetryReview(ctx, kind, in.ID)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &retryReviewOut{CacheControl: "no-store", Body: apimodel.RetryReview(view)}, nil
		})
		huma.Register(h.Huma, huma.Operation{OperationID: "request-" + resource.singular + "-retry", Method: http.MethodPost, Path: root + "/retry-requests", Summary: "Accept one revision-checked durable retry request", Tags: []string{"torrents"}, Middlewares: huma.Middlewares{retryNoStore}}, func(ctx context.Context, in *retryRequestIn) (*retryAcceptanceOut, error) {
			receipt, err := h.svc.RetryReviewed(ctx, kind, in.ID, in.Body.Key, in.Body.Revision)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &retryAcceptanceOut{CacheControl: "no-store", Body: apimodel.RetryAcceptance(receipt)}, nil
		})
		huma.Register(h.Huma, huma.Operation{OperationID: "get-" + resource.singular + "-retry-request", Method: http.MethodGet, Path: root + "/retry-requests/{key}", Summary: "Check a saved retry without repeating the mutation", Tags: []string{"torrents"}, Middlewares: huma.Middlewares{retryNoStore}}, func(ctx context.Context, in *retryAcceptanceIn) (*retryAcceptanceOut, error) {
			receipt, err := h.svc.GetRetryAcceptance(ctx, kind, in.ID, in.Key, in.Revision)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &retryAcceptanceOut{CacheControl: "no-store", Body: apimodel.RetryAcceptance(receipt)}, nil
		})
	}
}

func retryNoStore(ctx huma.Context, next func(huma.Context)) {
	ctx.SetHeader("Cache-Control", "no-store")
	next(ctx)
}
