// Package sysinfo provides lightweight, cross-platform runtime information
// used by Matea's health checks and startup warnings (Phase 2.5, C-22).
package sysinfo

import "path/filepath"

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

// HumanBytes renders a byte count as a short, human-readable string.
func HumanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return "0 B"
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	return trimFloat(float64(b)/float64(div)) + " " + units[exp]
}

func trimFloat(f float64) string {
	s := ""
	// 1 decimal, dropped if .0
	d := int(f*10 + 0.5)
	if d%10 == 0 {
		s = itoa(d / 10)
	} else {
		s = itoa(d/10) + "." + itoa(d%10)
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
