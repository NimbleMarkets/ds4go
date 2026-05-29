package lsp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type diagSnapshot struct {
	serverVersion int32
	diags         []Diagnostic
	seq           uint64 // per-URI publish counter, monotonic within a URI
}

// diagBuffer stores the latest diagnostics per URI and supports version-
// correlated and time-settle waiting plus an optional change callback.
//
// Entries are keyed by the normalized filesystem path (see pathFromURI), not
// the raw URI string, so a server that echoes back a differently percent-
// encoded URI than the client sent still resolves to the same entry.
type diagBuffer struct {
	mu       sync.Mutex
	byKey    map[string]diagSnapshot
	callback func(uri string, version int32, diags []Diagnostic)

	firstWait  time.Duration
	settleWait time.Duration
}

func newDiagBuffer(firstWait, settleWait time.Duration) *diagBuffer {
	if firstWait <= 0 {
		firstWait = DefaultFirstWait
	}
	if settleWait <= 0 {
		settleWait = DefaultSettleWait
	}
	b := &diagBuffer{
		byKey:      make(map[string]diagSnapshot),
		firstWait:  firstWait,
		settleWait: settleWait,
	}
	return b
}

// publish records a publishDiagnostics payload. Called from the notification
// handler goroutine.
func (b *diagBuffer) publish(p protocol.PublishDiagnosticsParams) {
	diags := make([]Diagnostic, 0, len(p.Diagnostics))
	for _, d := range p.Diagnostics {
		diags = append(diags, convertDiagnostic(d))
	}
	key := pathFromURI(string(p.URI))
	// Report a canonical URI (re-encoded from the normalized path) to callbacks
	// so the value they receive matches what the buffer keys on, regardless of
	// how the server percent-encoded the URI it echoed back.
	canonicalURI := uriFor("", key)

	b.mu.Lock()
	prev := b.byKey[key]
	b.byKey[key] = diagSnapshot{serverVersion: p.Version, diags: diags, seq: prev.seq + 1}
	cb := b.callback
	b.mu.Unlock()

	if cb != nil {
		// Hand the callback its own copy so it can never mutate stored state.
		cb(canonicalURI, p.Version, cloneDiags(diags))
	}
}

// forget drops any buffered diagnostics for uri. The Client calls this when a
// document is opened, edited, or closed so stale diagnostics from a previous
// version (or a previous open of the same URI) are never returned as current.
func (b *diagBuffer) forget(uri string) {
	b.mu.Lock()
	delete(b.byKey, pathFromURI(uri))
	b.mu.Unlock()
}

func (b *diagBuffer) onChange(cb func(uri string, version int32, diags []Diagnostic)) {
	b.mu.Lock()
	b.callback = cb
	b.mu.Unlock()
}

// snapshot returns a copy of the current diagnostics for uri (nil if none seen).
func (b *diagBuffer) snapshot(uri string) []Diagnostic {
	b.mu.Lock()
	defer b.mu.Unlock()
	return cloneDiags(b.byKey[pathFromURI(uri)].diags)
}

// wait blocks until the server publishes diagnostics for uri at document
// version wantVersion (version-correlated; honors the full timeout), or — for
// servers that do not report a version — until publishes for uri settle, or
// until ctx/timeout elapses. Returns a best-effort snapshot; err satisfies
// errors.Is(err, ErrDiagnosticsTimeout) on both the timeout and ctx-cancellation
// paths (the latter also wraps ctx.Err()).
//
// All bookkeeping is keyed on a per-URI publish sequence, so concurrent
// publishes for unrelated documents never disturb this wait.
func (b *diagBuffer) wait(ctx context.Context, uri string, wantVersion int, timeout time.Duration) ([]Diagnostic, error) {
	deadline := nowFunc().Add(timeout)
	firstDeadline := nowFunc().Add(b.firstWait)
	key := pathFromURI(uri)

	b.mu.Lock()
	startSeq := b.byKey[key].seq
	b.mu.Unlock()

	lastSeq := startSeq
	var settleDeadline time.Time // zero until we enter the time-settle path

	for {
		b.mu.Lock()
		snap := b.byKey[key]
		b.mu.Unlock()

		// Definitive: the server reported diagnostics for the exact version we
		// asked about. Worth waiting the whole timeout for.
		if snap.serverVersion != 0 && int(snap.serverVersion) == wantVersion {
			return cloneDiags(snap.diags), nil
		}

		// Read the clock once per iteration so every deadline comparison below
		// uses a single consistent "now".
		now := nowFunc()
		published := snap.seq != startSeq
		switch {
		case !published:
			// Nothing has published for this URI since we started. After
			// firstWait, assume the server is silent/clean for this version
			// and return best-effort (usually empty).
			if !now.Before(firstDeadline) {
				return cloneDiags(snap.diags), nil
			}
		case snap.serverVersion != 0:
			// Server reports versions but this publish is for a different
			// (older) version; keep waiting up to the deadline for ours.
			settleDeadline = time.Time{}
		default:
			// Unversioned publish: time-settle. Restart the settle window each
			// time a new publish for this URI arrives.
			if snap.seq != lastSeq || settleDeadline.IsZero() {
				settleDeadline = now.Add(b.settleWait)
			}
			if !now.Before(settleDeadline) {
				return cloneDiags(snap.diags), nil // settled
			}
		}
		lastSeq = snap.seq

		if !now.Before(deadline) {
			return cloneDiags(snap.diags), ErrDiagnosticsTimeout
		}
		select {
		case <-ctx.Done():
			// Preserve the documented ErrDiagnosticsTimeout contract while still
			// surfacing the underlying cause (Canceled / DeadlineExceeded).
			return cloneDiags(snap.diags), fmt.Errorf("%w: %w", ErrDiagnosticsTimeout, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// cloneDiags returns an independent copy of diags (nil for empty) so callers
// can never mutate the buffer's stored slice.
func cloneDiags(diags []Diagnostic) []Diagnostic {
	if len(diags) == 0 {
		return nil
	}
	return append([]Diagnostic(nil), diags...)
}

// nowFunc indirects time access for the wait loop.
var nowFunc = time.Now
