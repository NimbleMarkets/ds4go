package ds4api

import (
	"sync"
	"testing"
)

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
