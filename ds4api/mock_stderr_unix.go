//go:build !windows

package ds4api

import "syscall"

// dupFd duplicates fd so the mock owns its own descriptor, as libds4 does
// for ds4_set_stderr_fd.
func dupFd(fd int) (int, bool) {
	dup, err := syscall.Dup(fd)
	if err != nil {
		return fd, false
	}
	return dup, true
}
