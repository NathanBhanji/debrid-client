package store

import (
	"encoding/json"
	"fmt"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

// Conversions between sqlc row types and domain types. All timestamps are
// stored as RFC 3339 strings; JSON columns hold domain structs verbatim.

// AccountFromRow converts a provider_accounts row.
func AccountFromRow(r sqlcgen.ProviderAccount) (domain.ProviderAccount, error) {
	var creds domain.Credentials
	if err := json.Unmarshal([]byte(r.Credentials), &creds); err != nil {
		return domain.ProviderAccount{}, fmt.Errorf("account %s credentials: %w", r.ID, err)
	}
	created, err := ParseTime(r.CreatedAt)
	if err != nil {
		return domain.ProviderAccount{}, err
	}
	updated, err := ParseTime(r.UpdatedAt)
	if err != nil {
		return domain.ProviderAccount{}, err
	}
	return domain.ProviderAccount{
		ID: r.ID, Kind: domain.ProviderKind(r.Kind), Name: r.Name, Credentials: creds,
		Enabled: r.Enabled != 0, IsDefault: r.IsDefault != 0, CreatedAt: created, UpdatedAt: updated,
	}, nil
}

// AccountInsertParams builds the insert params for an account.
func AccountInsertParams(a domain.ProviderAccount) (sqlcgen.InsertProviderAccountParams, error) {
	creds, err := json.Marshal(utcCreds(a.Credentials))
	if err != nil {
		return sqlcgen.InsertProviderAccountParams{}, err
	}
	return sqlcgen.InsertProviderAccountParams{
		ID: a.ID, Kind: string(a.Kind), Name: a.Name, Credentials: string(creds),
		Enabled: b2i(a.Enabled), IsDefault: b2i(a.IsDefault),
		CreatedAt: FormatTime(a.CreatedAt), UpdatedAt: FormatTime(a.UpdatedAt),
	}, nil
}

// AccountUpdateParams builds the update params for an account (name/credentials/enabled).
func AccountUpdateParams(a domain.ProviderAccount) (sqlcgen.UpdateProviderAccountParams, error) {
	creds, err := json.Marshal(utcCreds(a.Credentials))
	if err != nil {
		return sqlcgen.UpdateProviderAccountParams{}, err
	}
	return sqlcgen.UpdateProviderAccountParams{
		Name: a.Name, Credentials: string(creds), Enabled: b2i(a.Enabled), UpdatedAt: FormatTime(a.UpdatedAt), ID: a.ID,
	}, nil
}

// TorrentFromRow converts a torrents row.
func TorrentFromRow(r sqlcgen.Torrent) (domain.Torrent, error) {
	var files []domain.File
	if err := json.Unmarshal([]byte(r.Files), &files); err != nil {
		return domain.Torrent{}, fmt.Errorf("torrent %s files: %w", r.ID, err)
	}
	var tracked []string
	if r.TrackedPaths != "" {
		if err := json.Unmarshal([]byte(r.TrackedPaths), &tracked); err != nil {
			return domain.Torrent{}, fmt.Errorf("torrent %s tracked_paths: %w", r.ID, err)
		}
	}
	// Start from defaults so rows with partial/empty settings JSON ({} is the
	// column default) don't silently mean "never retry, never unpack".
	settings := domain.DefaultTorrentSettings()
	if err := json.Unmarshal([]byte(r.Settings), &settings); err != nil {
		return domain.Torrent{}, fmt.Errorf("torrent %s settings: %w", r.ID, err)
	}
	if settings.FinishedAction == "" {
		settings.FinishedAction = domain.FinishedKeep
	}
	t := domain.Torrent{
		ID: r.ID, AccountID: r.AccountID, Hash: r.Hash, Name: r.Name, DirName: r.DirName, Organized: r.Organized != 0, Category: r.Category,
		Status: domain.TorrentStatus(r.Status), StatusReason: r.StatusReason, Error: r.Error,
		Progress: r.Progress, Size: r.Size, Speed: r.Speed, Seeders: int(r.Seeders),
		ProviderID: r.ProviderID, ProviderStatus: r.ProviderStatus, Files: files, Settings: settings,
		PayloadKind: domain.PayloadKind(r.PayloadKind), Payload: r.Payload, RetryCount: int(r.RetryCount),
		TrackedPaths: tracked,
	}
	var err error
	if t.AddedAt, err = ParseTime(r.AddedAt); err != nil {
		return domain.Torrent{}, err
	}
	if t.UpdatedAt, err = ParseTime(r.UpdatedAt); err != nil {
		return domain.Torrent{}, err
	}
	if t.ProviderAddedAt, err = ParseNullTime(r.ProviderAddedAt); err != nil {
		return domain.Torrent{}, err
	}
	if t.ProviderEndedAt, err = ParseNullTime(r.ProviderEndedAt); err != nil {
		return domain.Torrent{}, err
	}
	if t.FilesSelectedAt, err = ParseNullTime(r.FilesSelectedAt); err != nil {
		return domain.Torrent{}, err
	}
	if t.CompletedAt, err = ParseNullTime(r.CompletedAt); err != nil {
		return domain.Torrent{}, err
	}
	if t.RetryAt, err = ParseNullTime(r.RetryAt); err != nil {
		return domain.Torrent{}, err
	}
	return t, nil
}

// TorrentInsertParams builds the insert params for a torrent.
func TorrentInsertParams(t domain.Torrent) (sqlcgen.InsertTorrentParams, error) {
	files, settings, err := torrentJSON(t)
	if err != nil {
		return sqlcgen.InsertTorrentParams{}, err
	}
	if t.Payload == nil {
		t.Payload = []byte{} // column is NOT NULL; an empty payload is a caller bug but shouldn't fail the insert
	}
	return sqlcgen.InsertTorrentParams{
		ID: t.ID, AccountID: t.AccountID, Hash: t.Hash, Name: t.Name, DirName: t.DirName, Organized: b2i(t.Organized), TrackedPaths: pathsJSON(t.TrackedPaths), Category: t.Category,
		Status: string(t.Status), StatusReason: t.StatusReason, Error: t.Error,
		Progress: t.Progress, Size: t.Size, Speed: t.Speed, Seeders: int64(t.Seeders),
		ProviderID: t.ProviderID, ProviderStatus: t.ProviderStatus, Files: files, Settings: settings,
		PayloadKind: string(t.PayloadKind), Payload: t.Payload, RetryCount: int64(t.RetryCount),
		AddedAt: FormatTime(t.AddedAt), ProviderAddedAt: NullTime(t.ProviderAddedAt),
		ProviderEndedAt: NullTime(t.ProviderEndedAt), FilesSelectedAt: NullTime(t.FilesSelectedAt),
		CompletedAt: NullTime(t.CompletedAt), RetryAt: NullTime(t.RetryAt), UpdatedAt: FormatTime(t.UpdatedAt),
	}, nil
}

// TorrentUpdateParams builds the update params for a torrent.
func TorrentUpdateParams(t domain.Torrent) (sqlcgen.UpdateTorrentParams, error) {
	files, settings, err := torrentJSON(t)
	if err != nil {
		return sqlcgen.UpdateTorrentParams{}, err
	}
	return sqlcgen.UpdateTorrentParams{
		Name: t.Name, DirName: t.DirName, Organized: b2i(t.Organized), TrackedPaths: pathsJSON(t.TrackedPaths), Category: t.Category, Status: string(t.Status), StatusReason: t.StatusReason, Error: t.Error,
		Progress: t.Progress, Size: t.Size, Speed: t.Speed, Seeders: int64(t.Seeders),
		ProviderID: t.ProviderID, ProviderStatus: t.ProviderStatus, Files: files, Settings: settings,
		RetryCount: int64(t.RetryCount), ProviderAddedAt: NullTime(t.ProviderAddedAt),
		ProviderEndedAt: NullTime(t.ProviderEndedAt), FilesSelectedAt: NullTime(t.FilesSelectedAt),
		CompletedAt: NullTime(t.CompletedAt), RetryAt: NullTime(t.RetryAt), UpdatedAt: FormatTime(t.UpdatedAt), ID: t.ID,
	}, nil
}

func torrentJSON(t domain.Torrent) (files, settings string, err error) {
	fs := t.Files
	if fs == nil {
		fs = []domain.File{}
	}
	fb, err := json.Marshal(fs)
	if err != nil {
		return "", "", err
	}
	sb, err := json.Marshal(t.Settings)
	if err != nil {
		return "", "", err
	}
	return string(fb), string(sb), nil
}

// DownloadFromRow converts a downloads row.
func DownloadFromRow(r sqlcgen.Download) (domain.Download, error) {
	d := domain.Download{
		ID: r.ID, TorrentID: r.TorrentID, FileID: r.FileID, ProviderLink: r.ProviderLink, DirectURL: r.DirectUrl,
		RelPath: r.RelPath, Filename: r.Filename, Size: r.Size, BytesDone: r.BytesDone,
		State: domain.DownloadState(r.State), Error: r.Error, RetryCount: int(r.RetryCount),
	}
	if r.ExtractedPaths != "" {
		if err := json.Unmarshal([]byte(r.ExtractedPaths), &d.ExtractedPaths); err != nil {
			return domain.Download{}, fmt.Errorf("download %s extracted_paths: %w", r.ID, err)
		}
	}
	var err error
	if d.QueuedAt, err = ParseTime(r.QueuedAt); err != nil {
		return domain.Download{}, err
	}
	if d.UpdatedAt, err = ParseTime(r.UpdatedAt); err != nil {
		return domain.Download{}, err
	}
	if d.StartedAt, err = ParseNullTime(r.StartedAt); err != nil {
		return domain.Download{}, err
	}
	if d.FinishedAt, err = ParseNullTime(r.FinishedAt); err != nil {
		return domain.Download{}, err
	}
	if d.UnpackStartedAt, err = ParseNullTime(r.UnpackStartedAt); err != nil {
		return domain.Download{}, err
	}
	if d.UnpackFinishedAt, err = ParseNullTime(r.UnpackFinishedAt); err != nil {
		return domain.Download{}, err
	}
	if d.CompletedAt, err = ParseNullTime(r.CompletedAt); err != nil {
		return domain.Download{}, err
	}
	return d, nil
}

// DownloadInsertParams builds the insert params for a download.
func DownloadInsertParams(d domain.Download) sqlcgen.InsertDownloadParams {
	return sqlcgen.InsertDownloadParams{
		ID: d.ID, TorrentID: d.TorrentID, FileID: d.FileID, ProviderLink: d.ProviderLink, DirectUrl: d.DirectURL,
		RelPath: d.RelPath, Filename: d.Filename, Size: d.Size, BytesDone: d.BytesDone,
		State: string(d.State), Error: d.Error, RetryCount: int64(d.RetryCount), ExtractedPaths: pathsJSON(d.ExtractedPaths),
		QueuedAt: FormatTime(d.QueuedAt), StartedAt: NullTime(d.StartedAt), FinishedAt: NullTime(d.FinishedAt),
		UnpackStartedAt: NullTime(d.UnpackStartedAt), UnpackFinishedAt: NullTime(d.UnpackFinishedAt),
		CompletedAt: NullTime(d.CompletedAt), UpdatedAt: FormatTime(d.UpdatedAt),
	}
}

// DownloadUpdateParams builds the update params for a download.
func DownloadUpdateParams(d domain.Download) sqlcgen.UpdateDownloadParams {
	return sqlcgen.UpdateDownloadParams{
		DirectUrl: d.DirectURL, RelPath: d.RelPath, Filename: d.Filename, Size: d.Size, BytesDone: d.BytesDone,
		State: string(d.State), Error: d.Error, RetryCount: int64(d.RetryCount), ExtractedPaths: pathsJSON(d.ExtractedPaths),
		StartedAt: NullTime(d.StartedAt), FinishedAt: NullTime(d.FinishedAt),
		UnpackStartedAt: NullTime(d.UnpackStartedAt), UnpackFinishedAt: NullTime(d.UnpackFinishedAt),
		CompletedAt: NullTime(d.CompletedAt), UpdatedAt: FormatTime(d.UpdatedAt), ID: d.ID,
	}
}

// utcCreds normalises the optional expiry to UTC like every other stored timestamp.
func utcCreds(c domain.Credentials) domain.Credentials {
	if c.ExpiresAt != nil {
		u := c.ExpiresAt.UTC()
		c.ExpiresAt = &u
	}
	return c
}

// pathsJSON serialises a path list; empty stays "" (column default) so
// unmodified rows keep their compact form.
func pathsJSON(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	b, err := json.Marshal(paths)
	if err != nil {
		return ""
	}
	return string(b)
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
