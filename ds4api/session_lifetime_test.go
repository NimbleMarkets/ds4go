package ds4api

import (
	"errors"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// SetLogits and CopyLogits consult ds4_engine_vocab_size on the session's
// engine. Once Engine.Close has run, that engine pointer is freed C memory, so
// both must report the closed engine instead of calling into the library.
func TestSessionLogitsAfterEngineCloseReportClosed(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	eng.Close()

	var vocabCalls, setCalls, copyCalls int
	lib.raw.ds4EngineVocabSize = func(e uintptr) int32 {
		vocabCalls++
		return mockVocabSize
	}
	lib.raw.ds4SessionSetLogits = func(s uintptr, logits unsafe.Pointer, n int32) int32 {
		setCalls++
		return 0
	}
	lib.raw.ds4SessionCopyLogits = func(s uintptr, out unsafe.Pointer, cap int32) int32 {
		copyCalls++
		return cap
	}

	if err := sess.SetLogits(make([]float32, mockVocabSize)); !errors.Is(err, errClosed) {
		t.Errorf("SetLogits after Engine.Close: err = %v, want %v", err, errClosed)
	}
	if out, err := sess.CopyLogits(); !errors.Is(err, errClosed) || out != nil {
		t.Errorf("CopyLogits after Engine.Close: (len %d, %v), want (nil, %v)", len(out), err, errClosed)
	}
	if vocabCalls != 0 || setCalls != 0 || copyCalls != 0 {
		t.Errorf("library called with a closed engine: vocab=%d set=%d copy=%d, want 0", vocabCalls, setCalls, copyCalls)
	}
}

// leakEngineAndSession opens an engine and a session and drops both without
// closing them, so only their runtime cleanups can release them. It is kept
// out of line so no stale reference lingers in the caller's frame.
//
//go:noinline
func leakEngineAndSession(t *testing.T, lib *Library) {
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if _, err := eng.NewSession(128); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
}

// ds4_session_free dereferences the session's engine, so a leaked session's
// cleanup must run before its engine's cleanup even when both become
// unreachable in the same GC cycle. runtime.AddCleanup orders nothing by
// itself; the guarantee comes from the session cleanup argument keeping the
// *Engine reachable until that cleanup has run, which defers the engine's
// collection to a later cycle. With automatic GC disabled, that later cycle
// can only be the explicit one this test triggers, so the order is observable.
func TestSessionCleanupRunsBeforeEngineCleanup(t *testing.T) {
	lib := NewMockLibrary()
	var mu sync.Mutex
	var events []string
	record := func(ev string) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(events)
	}
	origFree, origClose := lib.raw.ds4SessionFree, lib.raw.ds4EngineClose
	lib.raw.ds4SessionFree = func(s uintptr) {
		record("session_free")
		origFree(s)
	}
	lib.raw.ds4EngineClose = func(e uintptr) {
		record("engine_close")
		origClose(e)
	}

	prev := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prev)

	leakEngineAndSession(t, lib)

	// waitFor triggers one GC cycle at a time and gives the cleanup goroutine
	// time to drain before triggering another, so the events observed after
	// a cycle are exactly the cleanups that cycle queued.
	waitFor := func(ev string) bool {
		for attempt := 0; attempt < 20; attempt++ {
			runtime.GC()
			settle := time.Now().Add(250 * time.Millisecond)
			for time.Now().Before(settle) {
				if slices.Contains(snapshot(), ev) {
					return true
				}
				time.Sleep(time.Millisecond)
			}
		}
		return false
	}
	if !waitFor("session_free") {
		t.Fatalf("session cleanup never ran; events = %v", snapshot())
	}
	// No further cycle has run, so the engine, kept reachable through the
	// session's cleanup argument, cannot have been closed yet.
	settle := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(settle) {
		if got := snapshot(); !slices.Equal(got, []string{"session_free"}) {
			t.Fatalf("engine closed before or alongside the session cleanup: events = %v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waitFor("engine_close") {
		t.Fatalf("engine cleanup never ran after the session was freed; events = %v", snapshot())
	}
	if got, want := snapshot(), []string{"session_free", "engine_close"}; !slices.Equal(got, want) {
		t.Fatalf("cleanup order = %v, want %v", got, want)
	}
}
