//go:build windows

package ds4

import "errors"

// ErrStderrUnsupportedOnWindows is returned by SetStderr, SetStderrFd, and
// DiscardLogs on Windows. libds4's ds4_set_stderr_fd is a C-runtime function
// that calls _dup on a CRT file descriptor, but os.File.Fd returns a Win32
// HANDLE; passing it across that boundary fails with EBADF, and there is no
// portable way to bridge the two from Go. In-process diagnostic redirection is
// therefore unavailable on Windows.
var ErrStderrUnsupportedOnWindows = errors.New("ds4: stderr redirection is not supported on Windows")

// SetStderrFd reports that libds4 diagnostic redirection is unavailable on
// Windows. See ErrStderrUnsupportedOnWindows.
func SetStderrFd(fd int) error {
	return ErrStderrUnsupportedOnWindows
}
