//go:build windows

package ds4api

// dupFd reports that no duplicate was made; the mock then never closes the
// caller's handle.
func dupFd(fd int) (int, bool) { return fd, false }
