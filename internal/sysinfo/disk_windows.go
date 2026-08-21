//go:build windows

package sysinfo

import "golang.org/x/sys/windows"

// platformFree uses GetDiskFreeSpaceEx to report free and total bytes for the
// volume that contains path.
func platformFree(path string) (free, total uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var freeBytes, totalBytes uint64
	if err = windows.GetDiskFreeSpaceEx(p, &freeBytes, &totalBytes, nil); err != nil {
		return 0, 0, err
	}
	return freeBytes, totalBytes, nil
}
