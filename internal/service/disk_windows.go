//go:build windows

package service

import "golang.org/x/sys/windows"

func diskUsage(path string) (free, total int64) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	var avail, tot, totFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &tot, &totFree); err != nil {
		return 0, 0
	}
	return int64(avail), int64(tot) //nolint:gosec // sizes fit
}
