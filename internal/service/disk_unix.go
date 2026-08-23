//go:build !windows

package service

import "golang.org/x/sys/unix"

func diskUsage(path string) (free, total int64) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0
	}
	// Statfs_t field types differ per platform; go through float64 so the
	// conversions are always required (keeps unconvert quiet everywhere).
	bsize := float64(st.Bsize)
	return int64(float64(st.Bavail) * bsize), int64(float64(st.Blocks) * bsize)
}
