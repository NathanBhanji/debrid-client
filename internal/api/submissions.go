package api

import (
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/domain"
)

const submissionContract = `Optional Idempotency-Key identifies one immutable add intent (UUIDv4, bare or quoted). A committed replay returns 201 with the current torrent and X-Debrid-Submission-Replayed: true, without requeueing. Receipt and torrent commit atomically. Reuse with different payload/account/category/settings returns 422 and X-Debrid-Error-Code: idempotency_mismatch. A removed accepted torrent returns 409/submission_removed and is never recreated. Other active duplicates return 409/active_torrent plus X-Debrid-Existing-Torrent-ID. Existing unkeyed clients keep ordinary add semantics. Receipts persist across restart/deletion. GET torrent-submissions/{key} reconciles timeouts; 404 does not mean an original request has finished. This is local enqueue deduplication, not a guarantee of exactly-once provider execution.`

func decodeUploadSettings(text string, schema *huma.Schema, registry huma.Registry) (domain.TorrentSettings, error) {
	if len(text) > 16*1024 {
		return domain.TorrentSettings{}, huma.Error422UnprocessableEntity("settings exceeds the 16 KiB limit")
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return domain.TorrentSettings{}, huma.Error422UnprocessableEntity("settings must be a JSON TorrentSettings object")
	}
	result := &huma.ValidateResult{}
	huma.Validate(registry, schema, huma.NewPathBuffer([]byte("body.settings"), len("body.settings")), huma.ModeWriteToServer, value, result)
	if len(result.Errors) > 0 {
		return domain.TorrentSettings{}, huma.Error422UnprocessableEntity("settings must match the TorrentSettings schema")
	}
	var settings apimodel.TorrentSettings
	if err := json.Unmarshal([]byte(text), &settings); err != nil {
		return domain.TorrentSettings{}, huma.Error422UnprocessableEntity("settings must be a JSON TorrentSettings object")
	}
	converted, err := settings.ToDomain()
	if err != nil {
		return domain.TorrentSettings{}, huma.Error422UnprocessableEntity("settings: " + err.Error())
	}
	return converted, nil
}
