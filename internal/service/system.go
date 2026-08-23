package service

import (
	"context"

	"github.com/NathanBhanji/debrid-client/internal/buildinfo"
	"github.com/NathanBhanji/debrid-client/internal/domain"
)

// SystemStatus is a summary for health/status endpoints.
type SystemStatus struct {
	Version     string                       `json:"version"`
	DownloadDir string                       `json:"download_dir"`
	Accounts    int                          `json:"accounts"`
	Torrents    map[domain.TorrentStatus]int `json:"torrents"`
	Downloads   map[domain.DownloadState]int `json:"downloads"`
	DiskFree    int64                        `json:"disk_free_bytes"`
	DiskTotal   int64                        `json:"disk_total_bytes"`
}

// Status summarises the system.
func (s *Service) Status(ctx context.Context) (SystemStatus, error) {
	st := SystemStatus{
		Version:     buildinfo.Version,
		DownloadDir: s.cfg.DownloadDir,
		Torrents:    map[domain.TorrentStatus]int{},
		Downloads:   map[domain.DownloadState]int{},
	}
	accs, err := s.store.ListProviderAccounts(ctx)
	if err != nil {
		return st, err
	}
	st.Accounts = len(accs)
	ts, err := s.store.ListTorrents(ctx)
	if err != nil {
		return st, err
	}
	for _, t := range ts {
		st.Torrents[domain.TorrentStatus(t.Status)]++
	}
	ds, err := s.store.ListDownloads(ctx)
	if err != nil {
		return st, err
	}
	for _, d := range ds {
		st.Downloads[domain.DownloadState(d.State)]++
	}
	st.DiskFree, st.DiskTotal = diskUsage(s.cfg.DownloadDir)
	return st, nil
}
