//go:build !windows

package ds4

import "github.com/NimbleMarkets/ds4go/ds4api"

// SetStderrFd redirects libds4's diagnostic output to the file descriptor fd for
// the default library. Pass -1 to restore the native stderr.
//
// libds4 dups fd internally and writes its diagnostics there unbuffered, so the
// caller may close its own descriptor after this call. The redirect target is
// process-global inside libds4; install it once at startup, before generation.
func SetStderrFd(fd int) error {
	return ds4api.SetStderrFd(fd)
}
