package ds4

import (
	"io"
	"os"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// StderrCapture pumps libds4's redirected diagnostic stream into an io.Writer
// until Close restores the native stderr. It is created by CaptureStderr.
type StderrCapture struct {
	lib  *ds4api.Library
	w, r *os.File
	done chan struct{}
}

// CaptureStderr redirects libds4's diagnostic output into dst and returns a
// handle that restores the native stderr when closed.
//
// dst receives the raw bytes libds4 writes — line splitting and any leveling
// are the caller's concern. This is the io.Writer counterpart to SetStderr,
// implemented with an os.Pipe and a background pump; use SetStderr directly when
// the sink is already a file or the null device. The redirect target is
// process-global inside libds4, so only one capture (or SetStderr target) is
// active at a time; install it once during startup, before generation.
// The capture retains the library it redirects, so clearing or replacing the
// default library does not change which library Close restores.
//
// Not supported on Windows; see SetStderrFd. On failure no redirect is
// installed and the pipe is released.
func CaptureStderr(dst io.Writer) (*StderrCapture, error) {
	lib, err := ds4api.DefaultLibrary()
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// libds4 dups w's descriptor internally, so the pipe survives until both
	// libds4's dup and our own w are closed (see Close).
	if err := lib.SetStderrFd(int(w.Fd())); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, err
	}
	c := &StderrCapture{lib: lib, w: w, r: r, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		_, _ = io.Copy(dst, r)
	}()
	return c, nil
}

// Close restores the native stderr and waits for the pump to drain. Diagnostics
// libds4 already wrote are flushed to dst before Close returns. Close is
// idempotent only in the sense that the underlying files tolerate a double
// close; call it exactly once per CaptureStderr.
//
// Close restores the library captured at creation, even if the default library
// has since been cleared or replaced.
func (c *StderrCapture) Close() error {
	// Restore first so libds4 stops writing and closes its dup of the write
	// end; then close ours so the reader observes EOF and the pump exits.
	err := c.lib.SetStderrFd(-1)
	_ = c.w.Close()
	if err != nil {
		// libds4's dup stays open, so EOF never comes: unblock the pump by
		// closing the read end under it. Its pending Read returns an error
		// and io.Copy exits. In the normal path the read end is closed only
		// after the pump has drained, so no buffered output is lost there.
		_ = c.r.Close()
		<-c.done
		return err
	}
	<-c.done
	_ = c.r.Close()
	return nil
}
