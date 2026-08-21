//go:build unix

package sysinfo

import "golang.org/x/sys/unix"

// platformFree uses statfs(2) to report free and total bytes for the
// filesystem holding path.
func platformFree(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err = unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return st.Bavail * bsize, st.Blocks * bsize, nil
}
