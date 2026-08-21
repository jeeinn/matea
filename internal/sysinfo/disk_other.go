//go:build !unix && !windows

package sysinfo

import "fmt"

// platformFree is the fallback for platforms without a supported statfs
// binding. It reports unsupported rather than returning bogus numbers.
func platformFree(path string) (free, total uint64, err error) {
	return 0, 0, fmt.Errorf("disk free space is not supported on this platform")
}
