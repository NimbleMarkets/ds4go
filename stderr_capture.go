package ds4

import (
	"io"
	"os"
)

// StderrCapture pumps libds4's redirected diagnostic stream into an io.Writer
// until Close restores the native stderr. It is created by CaptureStderr.
type StderrCapture struct {
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
//
// Not supported on Windows; see SetStderrFd. On failure no redirect is
// installed and the pipe is released.
func CaptureStderr(dst io.Writer) (*StderrCapture, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// libds4 dups w's descriptor internally, so the pipe survives until both
	// libds4's dup and our own w are closed (see Close).
	if err := SetStderr(w); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, err
	}
	c := &StderrCapture{w: w, r: r, done: make(chan struct{})}
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
func (c *StderrCapture) Close() error {
	// Restore first so libds4 stops writing and closes its dup of the write
	// end; then close ours so the reader observes EOF and the pump exits.
	err := SetStderr(nil)
	_ = c.w.Close()
	<-c.done
	_ = c.r.Close()
	return err
}
