package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// FileSelectionReceipt binds one immutable choice to a provider incarnation.
// It lives in the existing settings JSON, not in a new database schema.
type FileSelectionReceipt struct {
	Version         int      `json:"version"`
	ProviderID      string   `json:"provider_id"`
	RetryCount      int      `json:"retry_count"`
	ProviderSelects bool     `json:"provider_selects"`
	Revision        string   `json:"revision"`
	FileIDs         []string `json:"file_ids"`
}

// SelectionSourceMatches rejects provider ID reuse for different known content.
// Some providers omit hashes; those sources remain bound by IDs and metadata.
func (t Torrent) SelectionSourceMatches(id, hash string) bool {
	return id == t.ProviderID && (hash == "" || strings.EqualFold(hash, t.Hash))
}

func (t Torrent) SelectionPending() bool {
	return t.Settings.WaitForSelection && (t.ProviderID == "" || t.Settings.Selection == nil || t.Settings.Selection.ProviderID != t.ProviderID || t.Settings.Selection.RetryCount != t.RetryCount)
}

// SelectionReady also rejects malformed internal receipts rather than releasing a hold.
func (t Torrent) SelectionReady() bool {
	if !t.Settings.WaitForSelection {
		return true
	}
	r := t.Settings.Selection
	if t.SelectionPending() {
		return false
	}
	return r.Version == 1 && validCanonicalIDs(r.FileIDs) && ValidSelectionRevision(r.Revision) && slices.Equal(r.FileIDs, t.Settings.ManualFiles)
}

func ValidSelectionRevision(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// CanonicalFileIDs rejects duplicates and bounds provider-supplied/path-based IDs.
func CanonicalFileIDs(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > 20_000 {
		return nil, errors.New("select between 1 and 20000 files")
	}
	out := slices.Clone(ids)
	slices.Sort(out)
	if !validCanonicalIDs(out) {
		return nil, errors.New("file ids must be nonempty, unique and within supported limits")
	}
	return out, nil
}
func validCanonicalIDs(ids []string) bool {
	if len(ids) == 0 || len(ids) > 20_000 {
		return false
	}
	bytes := 0
	for i, id := range ids {
		bytes += len(id)
		if id == "" || len(id) > 8192 || !utf8.ValidString(id) || bytes > 2<<20 || (i > 0 && id <= ids[i-1]) {
			return false
		}
	}
	return true
}

// FileSelectionRevision uses authoritative provider metadata only. Progress,
// selected flags and restricted links do not invalidate a user's review.
func FileSelectionRevision(t Torrent, files []File) (string, error) {
	if t.ProviderID == "" || len(files) == 0 || len(files) > 20_000 {
		return "", errors.New("provider file metadata is unavailable or exceeds supported limits")
	}
	type entry struct {
		ID   string
		Path string
		Size int64
	}
	entries := make([]entry, len(files))
	ids := make([]string, len(files))
	for i, f := range files {
		if len(f.Path) > 8192 || !utf8.ValidString(f.Path) || f.Size < 0 {
			return "", errors.New("provider file metadata cannot be reviewed safely")
		}
		entries[i] = entry{f.ID, f.Path, f.Size}
		ids[i] = f.ID
	}
	if _, err := CanonicalFileIDs(ids); err != nil {
		return "", err
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.ID, b.ID) })
	data, err := json.Marshal(struct {
		Version    int
		Torrent    string
		Hash       string
		Account    string
		Provider   string
		RetryCount int
		Files      []entry
	}{1, t.ID, t.Hash, t.AccountID, t.ProviderID, t.RetryCount, entries})
	if err != nil || len(data) > 2<<20 {
		return "", errors.New("provider file metadata exceeds supported limits")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// StrictSelectedFiles requires every requested ID to map exactly once. The
// legacy filter deliberately permits partial matches; manual review cannot.
func StrictSelectedFiles(files []File, ids []string) ([]File, error) {
	want, err := CanonicalFileIDs(ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]File, len(files))
	for _, f := range files {
		if _, duplicate := byID[f.ID]; duplicate {
			return nil, errors.New("provider file ids are ambiguous")
		}
		byID[f.ID] = f
	}
	out := make([]File, 0, len(want))
	for _, id := range want {
		f, ok := byID[id]
		if !ok {
			return nil, errors.New("selected files no longer match provider metadata")
		}
		out = append(out, f)
	}
	return out, nil
}
