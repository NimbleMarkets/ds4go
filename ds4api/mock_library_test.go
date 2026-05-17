package ds4api

import (
	"testing"
)

func TestMockLibraryEngineSessionLifecycle(t *testing.T) {
	lib := NewMockLibrary()

	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if eng == nil {
		t.Fatal("engine is nil")
	}

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sess == nil {
		t.Fatal("session is nil")
	}

	if got := sess.Pos(); got != 0 {
		t.Fatalf("Pos() = %d, want 0", got)
	}

	sess.Close()
	eng.Close()
}

func TestMockLibraryTokenization(t *testing.T) {
	lib := NewMockLibrary()
	eng, _ := lib.NewEngine(EngineOptions{})
	defer eng.Close()

	toks, err := newTokensWithLibrary(lib, []int{10, 20, 30})
	if err != nil {
		t.Fatalf("newTokensWithLibrary: %v", err)
	}
	defer toks.Free()

	if got := toks.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}

	slice := toks.Slice()
	if len(slice) != 3 || slice[0] != 10 || slice[1] != 20 || slice[2] != 30 {
		t.Fatalf("Slice() = %v, want [10 20 30]", slice)
	}
}

func TestMockLibraryGeneration(t *testing.T) {
	lib := NewMockLibrary()
	eng, _ := lib.NewEngine(EngineOptions{})
	defer eng.Close()

	sess, _ := eng.NewSession(128)
	defer sess.Close()

	// Tokenize a prompt.
	toks, err := eng.TokenizeText("hello world")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer toks.Free()

	if toks.Len() == 0 {
		t.Fatal("expected non-empty tokenization")
	}

	// Sync and generate a few tokens.
	if err := sess.SyncTokens(toks); err != nil {
		t.Fatalf("SyncTokens: %v", err)
	}

	for i := 0; i < 5; i++ {
		tok := sess.Argmax()
		if tok == 0 {
			t.Fatalf("Argmax() returned 0 on iteration %d", i)
		}
		if err := sess.Eval(tok); err != nil {
			t.Fatalf("Eval(%d): %v", tok, err)
		}
	}

	if got := sess.Pos(); int(got) != toks.Len()+5 {
		t.Fatalf("Pos() = %d, want %d", got, toks.Len()+5)
	}
}

func TestMockLibrarySessionCloseWhileInUse(t *testing.T) {
	lib := NewMockLibrary()
	eng, _ := lib.NewEngine(EngineOptions{})
	defer eng.Close()

	sess, _ := eng.NewSession(128)

	// Close the session.
	sess.Close()

	// Subsequent calls must return errClosed, not crash.
	if err := sess.Eval(1); err == nil {
		t.Fatal("Eval after Close: expected error")
	}
	if sess.Argmax() != 0 {
		t.Fatal("Argmax after Close: expected 0")
	}
}

func TestMockLibrarySpeculativeArgmax(t *testing.T) {
	lib := NewMockLibrary()
	eng, _ := lib.NewEngine(EngineOptions{})
	defer eng.Close()

	sess, _ := eng.NewSession(128)
	defer sess.Close()

	toks, _ := eng.TokenizeText("hi")
	defer toks.Free()
	sess.SyncTokens(toks)

	first := sess.Argmax()
	accepted, err := sess.EvalSpeculativeArgmax(first, 3, -1)
	if err != nil {
		t.Fatalf("EvalSpeculativeArgmax: %v", err)
	}
	if len(accepted) == 0 {
		t.Fatal("expected at least one accepted token")
	}
	if len(accepted) > 3 {
		t.Fatalf("expected at most 3 accepted tokens, got %d", len(accepted))
	}
}
