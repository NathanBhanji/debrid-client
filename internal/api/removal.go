package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

const metaRemovalRequired = "removal_review_required"

type reviewedRemovalIn struct {
	ID       string `path:"id" maxLength:"256"`
	Files    bool   `query:"files"`
	Provider bool   `query:"provider"`
	IfMatch  string `header:"If-Match" required:"true" minLength:"66" maxLength:"66"`
}
type removalReviewOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         service.RemovalReview
}

func parseRemovalIfMatch(value string) (string, error) {
	if len(value) != 66 || value[0] != '"' || value[65] != '"' || !domain.ValidSelectionRevision(value[1:65]) {
		return "", huma.Error422UnprocessableEntity("If-Match requires one strong quoted removal revision")
	}
	return value[1:65], nil
}
func (h *Handler) registerRemovalReviewRoute(p string) {
	huma.Register(h.Huma, huma.Operation{OperationID: "review-torrent-removal", Method: http.MethodGet, Path: p + "/torrents/{id}/removal-review", Summary: "Review exact transfer incarnation before removal", Description: "Returns exists:false explicitly for a missing transfer. A missing HTTP route is not transfer absence. Send the original revision as a single strong quoted If-Match with DELETE;412 refuses a changed incarnation before provider/filesystem removal.204 proves record removal, not that unknown/shared/untracked provider copies or files disappeared.", Tags: []string{"torrents"}, Middlewares: huma.Middlewares{retryNoStore}}, func(ctx context.Context, in *retryReviewIn) (*removalReviewOut, error) {
		view, err := h.svc.GetRemovalReview(ctx, in.ID)
		if err != nil {
			return nil, h.mapErr(err)
		}
		return &removalReviewOut{CacheControl: "no-store", Body: view}, nil
	})
	huma.Register(h.Huma, huma.Operation{OperationID: "remove-reviewed-torrent", Method: http.MethodDelete, Path: p + "/torrents/{id}/removal", Summary: "Remove an exact transfer with a required incarnation precondition", Description: "Requires one strong quoted If-Match from removal-review. Missing/empty/weak/duplicate headers are refused, never downgraded to unconditional legacy deletion.412 refuses a changed incarnation before cleanup.204 proves record removal only; provider/filesystem cleanup may be partial or intentionally preserved.", Tags: []string{"torrents"}, DefaultStatus: http.StatusNoContent, Metadata: map[string]any{metaRemovalRequired: true}, Middlewares: huma.Middlewares{retryNoStore, h.validateRemovalIfMatch}}, func(ctx context.Context, in *reviewedRemovalIn) (*struct{}, error) {
		revision, err := parseRemovalIfMatch(in.IfMatch)
		if err != nil {
			return nil, err
		}
		return nil, h.mapErr(h.svc.DeleteTorrent(ctx, in.ID, service.DeleteOptions{ExpectedRevision: revision, DeleteFiles: in.Files, DeleteFromProvider: in.Provider}))
	})
}

// Typed header decoding exposes only the first value and cannot distinguish
// absent from present-empty. Validate raw cardinality before legacy fallback.
func (h *Handler) validateRemovalIfMatch(ctx huma.Context, next func(huma.Context)) {
	count := 0
	valid := true
	ctx.EachHeader(func(name, value string) {
		if !strings.EqualFold(name, "If-Match") {
			return
		}
		count++
		if _, err := parseRemovalIfMatch(value); err != nil {
			valid = false
		}
	})
	required := ctx.Operation() != nil && ctx.Operation().Metadata[metaRemovalRequired] == true
	if count > 1 || !valid || required && count != 1 {
		_ = huma.WriteErr(h.Huma, ctx, http.StatusUnprocessableEntity, "If-Match requires exactly one nonempty strong quoted removal revision")
		return
	}
	next(ctx)
}
