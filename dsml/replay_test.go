package dsml

import (
	"sync"
	"testing"
)

// replaySampleBlock builds a minimal well-formed tool_calls block for tests.
func replaySampleBlock(name string) string {
	return "\n\n<" + dsmlMarker + toolCallsBlockName + ">\n" +
		invokeStartToken + " name=\"" + name + "\">\n" +
		invokeEndToken + "\n" +
		toolCallsEndToken
}

func TestReplayStoreRememberLookup(t *testing.T) {
	s := NewReplayStore(8)
	exact := replaySampleBlock("add")
	if err := s.Remember("call_1", exact); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	got, ok := s.Lookup("call_1")
	if !ok || got != exact {
		t.Errorf("Lookup = %q, %v; want %q, true", got, ok, exact)
	}
	if _, ok := s.Lookup("missing"); ok {
		t.Error("Lookup of an unknown id returned ok")
	}
}

func TestReplayStoreEviction(t *testing.T) {
	s := NewReplayStore(2)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Remember(id, replaySampleBlock(id)); err != nil {
			t.Fatalf("Remember %s: %v", id, err)
		}
	}
	if _, ok := s.Lookup("a"); ok {
		t.Error(`oldest entry "a" should have been evicted`)
	}
	for _, id := range []string{"b", "c"} {
		if _, ok := s.Lookup(id); !ok {
			t.Errorf("entry %q should still be present", id)
		}
	}
}

func TestReplayStoreRejectsNonInvoke(t *testing.T) {
	s := NewReplayStore(4)
	if err := s.Remember("bad", "not a tool_calls block"); err == nil {
		t.Error("Remember of a non-tool_calls block = nil error, want rejection")
	}
}

func TestReplayStoreNilReceiver(t *testing.T) {
	var s *ReplayStore
	if err := s.Remember("x", replaySampleBlock("x")); err != nil {
		t.Errorf("nil ReplayStore.Remember = %v, want nil", err)
	}
	if got, ok := s.Lookup("x"); ok || got != "" {
		t.Errorf(`nil ReplayStore.Lookup = %q, %v; want "", false`, got, ok)
	}
}

func TestReplayStoreConcurrent(t *testing.T) {
	s := NewReplayStore(1000)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "call_" + string(rune('a'+n))
			for range 50 {
				_ = s.Remember(id, replaySampleBlock(id))
				s.Lookup(id)
			}
		}(i)
	}
	wg.Wait()
}

func TestReplayStoreLookupBlockRequiresSameBlock(t *testing.T) {
	s := NewReplayStore(8)
	block := replaySampleBlock("add")
	if err := s.Remember("call_1", block); err != nil {
		t.Fatalf("Remember call_1: %v", err)
	}
	if err := s.Remember("call_2", block); err != nil {
		t.Fatalf("Remember call_2: %v", err)
	}
	got, ok := s.LookupBlock([]string{"call_1", "call_2"})
	if !ok || got != block {
		t.Fatalf("LookupBlock = %q, %v; want %q, true", got, ok, block)
	}
	if err := s.Remember("call_3", replaySampleBlock("other")); err != nil {
		t.Fatalf("Remember call_3: %v", err)
	}
	if got, ok := s.LookupBlock([]string{"call_1", "call_3"}); ok || got != "" {
		t.Fatalf("LookupBlock mismatched blocks = %q, %v; want empty false", got, ok)
	}
}
