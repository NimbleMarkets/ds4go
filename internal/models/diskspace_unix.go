//go:build !windows

package models

import "golang.org/x/sys/unix"

// availableBytes reports the free space on path's volume that is usable by an
// unprivileged process.
//
// Bavail rather than Bfree: Bfree includes the filesystem's reserved blocks,
// which only root may consume, so using it would promise space a download
// cannot actually have.
func availableBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
