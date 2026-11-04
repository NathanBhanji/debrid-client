package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/domain"
)

type settingsReviewBody struct {
	Settings apimodel.Settings `json:"settings"`
	Revision string            `json:"revision" minLength:"64" maxLength:"64" pattern:"^[a-f0-9]{64}$"`
}
type settingsReviewOut struct {
	CacheControl string `header:"Cache-Control"`
	ETag         string `header:"ETag"`
	Body         settingsReviewBody
}
type settingsReviewedIn struct {
	IfMatch string `header:"If-Match" required:"true" minLength:"66" maxLength:"66"`
	Body    apimodel.Settings
}

func parseSettingsIfMatch(value string) (string, error) {
	if len(value) != 66 || value[0] != '"' || value[65] != '"' || !domain.ValidSelectionRevision(value[1:65]) {
		return "", huma.Error422UnprocessableEntity("If-Match requires one strong quoted settings revision")
	}
	return value[1:65], nil
}

func (h *Handler) validateSettingsIfMatch(ctx huma.Context, next func(huma.Context)) {
	count := 0
	valid := true
	ctx.EachHeader(func(name, value string) {
		if strings.EqualFold(name, "If-Match") {
			count++
			if _, err := parseSettingsIfMatch(value); err != nil {
				valid = false
			}
		}
	})
	if count != 1 || !valid {
		_ = huma.WriteErr(h.Huma, ctx, http.StatusUnprocessableEntity, "If-Match requires exactly one strong quoted settings revision")
		return
	}
	next(ctx)
}
func (h *Handler) registerSettingsReview(p string) {
	huma.Register(h.Huma, huma.Operation{OperationID: "review-settings", Method: http.MethodGet, Path: p + "/settings/review", Summary: "Read runtime settings with an opaque write revision", Tags: []string{"settings"}, Middlewares: huma.Middlewares{retryNoStore}}, func(ctx context.Context, _ *struct{}) (*settingsReviewOut, error) {
		view, err := h.svc.GetSettingsReview(ctx)
		if err != nil {
			return nil, h.mapErr(err)
		}
		return &settingsReviewOut{CacheControl: "no-store", ETag: "\"" + view.Revision + "\"", Body: settingsReviewBody{Settings: apimodel.FromSettings(view.Settings), Revision: view.Revision}}, nil
	})
	huma.Register(h.Huma, huma.Operation{OperationID: "update-reviewed-settings", Method: http.MethodPut, Path: p + "/settings/review", Summary: "Replace runtime settings only if the reviewed revision is current", Description: "Requires exactly one strong quoted If-Match.412 refuses stale state atomically, including changes through legacy clients and ABA changes. Missing/empty/weak/list/duplicate headers are rejected; no unconditional fallback. A lost response is not an acceptance receipt: read current settings and require fresh review, never automatically replay.", Tags: []string{"settings"}, Middlewares: huma.Middlewares{retryNoStore, h.validateSettingsIfMatch}}, func(ctx context.Context, in *settingsReviewedIn) (*settingsReviewOut, error) {
		revision, err := parseSettingsIfMatch(in.IfMatch)
		if err != nil {
			return nil, err
		}
		st, err := in.Body.ToService()
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		view, err := h.svc.UpdateSettingsReviewed(ctx, st, revision)
		if err != nil {
			return nil, h.mapErr(err)
		}
		return &settingsReviewOut{CacheControl: "no-store", ETag: "\"" + view.Revision + "\"", Body: settingsReviewBody{Settings: apimodel.FromSettings(view.Settings), Revision: view.Revision}}, nil
	})
}
