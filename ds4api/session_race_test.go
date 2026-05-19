package ds4api

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// TestSessionConcurrentInferenceSerialized verifies that inference calls on
// two sessions sharing one engine never enter libds4 concurrently.
//
// The Metal backend is owned per-engine: concurrent inference encodes into a
// single shared GPU command buffer, and one goroutine attaches a command
// encoder to a buffer another goroutine already committed. Metal aborts with
// "_status < MTLCommandBufferStatusCommitted" -> SIGABRT. The Go race detector
// cannot see this because the race lives in C memory, so the mock instruments
// the libds4 entry points directly.
func TestSessionConcurrentInferenceSerialized(t *testing.T) {
	lib := NewMockLibrary()

	var inFlight, maxInFlight atomic.Int32
	enter := func() {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(200 * time.Microsecond)
	}
	leave := func() { inFlight.Add(-1) }

	syncFn := lib.raw.ds4SessionSync
	lib.raw.ds4SessionSync = func(s uintptr, p *cTokens, e unsafe.Pointer, n uintptr) int32 {
		enter()
		defer leave()
		return syncFn(s, p, e, n)
	}
	evalFn := lib.raw.ds4SessionEval
	lib.raw.ds4SessionEval = func(s uintptr, tok int32, e unsafe.Pointer, n uintptr) int32 {
		enter()
		defer leave()
		return evalFn(s, tok, e, n)
	}
	argmaxFn := lib.raw.ds4SessionArgmax
	lib.raw.ds4SessionArgmax = func(s uintptr) int32 {
		enter()
		defer leave()
		return argmaxFn(s)
	}

	SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(EngineOptions{ModelPath: "mock"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()

	var wg sync.WaitGroup
	for range 2 {
		sess, err := engine.NewSession(256)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			defer s.Close()
			if err := s.Sync([]int{10, 11, 12}); err != nil {
				t.Errorf("Sync: %v", err)
				return
			}
			for range 40 {
				tok := s.Argmax()
				if err := s.Eval(tok); err != nil {
					t.Errorf("Eval: %v", err)
					return
				}
			}
		}(sess)
	}
	wg.Wait()

	if got := maxInFlight.Load(); got > 1 {
		t.Fatalf("libds4 entered concurrently: max in-flight = %d, want 1; "+
			"the per-engine Metal command buffer would be corrupted", got)
	}
}
