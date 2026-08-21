// Package sysinfo provides lightweight, cross-platform runtime information
// used by Matea's health checks and startup warnings (Phase 2.5, C-22).
package sysinfo

import (
	"fmt"
	"path/filepath"
)

// DefaultDiskWarnPercent is the used-space threshold above which a partition
// is flagged as a warning.
const DefaultDiskWarnPercent = 85.0

// DiskInfo describes free/used space on the filesystem that physically holds
// Path (the directory is resolved to an absolute path before probing).
type DiskInfo struct {
	Path        string  `json:"path"`
	FreeBytes   uint64  `json:"free_bytes"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
	Warning     bool    `json:"warning"`
}

// platformFree returns the free and total bytes available on the filesystem
// that contains path. Its implementation is OS-specific and provided by the
// build-tagged files in this package (exactly one compiles per platform).
//
// CheckDisk reports disk usage for the filesystem holding dir. warnPct flags
// the result as a warning when used space meets or exceeds the given
// percentage. An empty dir resolves to the current working directory.
func CheckDisk(dir string, warnPct float64) (DiskInfo, error) {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	free, total, err := platformFree(abs)
	if err != nil {
		return DiskInfo{Path: abs}, err
	}
	var used uint64
	if total > free {
		used = total - free
	}
	var pct float64
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	return DiskInfo{
		Path:        abs,
		FreeBytes:   free,
		TotalBytes:  total,
		UsedBytes:   used,
		UsedPercent: pct,
		Warning:     pct >= warnPct,
	}, nil
}

// HumanBytes renders a byte count as a short, human-readable string using
// 1024-based (IEC) units: 512 -> "512 B", 5 GiB -> "5 GiB".
func HumanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	// div is 1024^(exp+1), so the matching label is units[exp+1].
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	v := float64(b) / float64(div)
	s := fmt.Sprintf("%.1f", v)
	if s[len(s)-2:] == ".0" {
		s = s[:len(s)-2]
	}
	return s + " " + units[exp+1]
}
