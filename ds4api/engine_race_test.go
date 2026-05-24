package ds4api

import (
	"sync"
	"testing"
)

// TestEngineOptionsPowerPercentRoundTrips verifies that the PowerPercent
// option passes through to libds4. The cEngineOptions struct layout must
// match the C ds4_engine_options layout exactly; a missing or misplaced
// PowerPercent field silently corrupts neighbouring bool flags.
func TestEngineOptionsPowerPercentRoundTrips(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{PowerPercent: 42})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	got := eng.Power()
	if got != 42 {
		t.Fatalf("Engine.Power() = %d, want 42", got)
	}
}

// TestEngineConcurrentCalls exercises Engine methods from many goroutines.
// Run with -race; it must report no data races.
func TestEngineConcurrentCalls(t *testing.T) {
	lib := NewMockLibrary()
	SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(EngineOptions{ModelPath: "mock"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				toks, err := engine.EncodeChatPrompt("sys", "hello world", ThinkNone)
				if err != nil {
					t.Errorf("EncodeChatPrompt: %v", err)
					return
				}
				toks.Free()
				_, _ = engine.TokenText(42)
				_ = engine.TokenEOS()
				_ = engine.HasMTP()
			}
		}()
	}
	wg.Wait()
}

func TestEngineSetPower(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{PowerPercent: 100})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if err := eng.SetPower(25); err != nil {
		t.Fatalf("SetPower(25): %v", err)
	}
	if got := eng.Power(); got != 25 {
		t.Fatalf("Engine.Power() after SetPower(25) = %d, want 25", got)
	}
	// Boundary values 1 and 100 must be accepted (C contract is 1..100 inclusive).
	for _, good := range []int{1, 100} {
		if err := eng.SetPower(good); err != nil {
			t.Fatalf("SetPower(%d): %v", good, err)
		}
		if got := eng.Power(); got != good {
			t.Fatalf("Engine.Power() after SetPower(%d) = %d, want %d", good, got, good)
		}
	}
	// Out-of-range values are rejected (ds4.c contract: 1..100).
	for _, bad := range []int{0, -1, 101, 200} {
		if err := eng.SetPower(bad); err == nil {
			t.Fatalf("SetPower(%d) = nil, want error", bad)
		}
	}
}

func TestEngineVocabSize(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if got := eng.VocabSize(); got != 129280 {
		// The mock returns a fixed canonical value; this asserts the
		// wrapper actually invokes the registered symbol, not that the
		// real vocab is this size.
		t.Fatalf("Engine.VocabSize() = %d, want 129280", got)
	}
}
