package dsml

import (
	"sync"
	"testing"
)

// replaySampleInvoke builds a minimal well-formed invoke block for tests.
func replaySampleInvoke(name string) string {
	return invokeStartToken + " name=\"" + name + "\">\n" + invokeEndToken + ">"
}

func TestReplayStoreRememberLookup(t *testing.T) {
	s := NewReplayStore(8)
	exact := replaySampleInvoke("add")
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
		if err := s.Remember(id, replaySampleInvoke(id)); err != nil {
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
	if err := s.Remember("bad", "not an invoke block"); err == nil {
		t.Error("Remember of a non-invoke block = nil error, want rejection")
	}
}

func TestReplayStoreNilReceiver(t *testing.T) {
	var s *ReplayStore
	if err := s.Remember("x", replaySampleInvoke("x")); err != nil {
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
				_ = s.Remember(id, replaySampleInvoke(id))
				s.Lookup(id)
			}
		}(i)
	}
	wg.Wait()
}
