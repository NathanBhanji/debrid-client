//go:build !windows

package service

import "golang.org/x/sys/unix"

func diskUsage(path string) (free, total int64) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0
	}
	//nolint:unconvert,gosec // Statfs_t field types differ per platform; conversions are needed on some
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize)
}
