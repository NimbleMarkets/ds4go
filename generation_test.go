package ds4

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

func mockEngine(t *testing.T) *Engine {
	t.Helper()
	lib := ds4api.NewMockLibrary()
	eng, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng
}

func TestGenerateTokensBasic(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	toks, err := eng.TokenizeText("hello world")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer toks.Free()

	var emitted []int
	got, err := (Generator{Engine: eng, Session: sess}).GenerateTokens(toks, GenerateOptions{
		MaxTokens: 5,
		StopOnEOS: true,
		OnToken: func(token int) {
			emitted = append(emitted, token)
		},
	})
	if err != nil {
		t.Fatalf("GenerateTokens: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 tokens, got %d", len(got))
	}
	if len(emitted) != 5 {
		t.Fatalf("expected 5 emitted tokens, got %d", len(emitted))
	}
}

func TestGenerateTokensContextCancellation(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	toks, err := eng.TokenizeText("hello world")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer toks.Free()

	ctx, cancel := context.WithCancel(context.Background())

	var emitted []int
	go func() {
		// Cancel after a short delay so at least a few tokens are generated.
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	got, err := (Generator{Engine: eng, Session: sess}).GenerateTokens(toks, GenerateOptions{
		MaxTokens: 100,
		StopOnEOS: true,
		Context:   ctx,
		OnToken: func(token int) {
			emitted = append(emitted, token)
		},
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected partial output, got none")
	}
	if len(emitted) != len(got) {
		t.Fatalf("emitted %d tokens but got %d", len(emitted), len(got))
	}
}

func TestGenerateTokensContextAlreadyCancelled(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	toks, err := eng.TokenizeText("x")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer toks.Free()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	got, err := (Generator{Engine: eng, Session: sess}).GenerateTokens(toks, GenerateOptions{
		MaxTokens: 10,
		Context:   ctx,
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no tokens when already cancelled, got %d", len(got))
	}
}

func TestGenerateString(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	text, err := (Generator{Engine: eng, Session: sess}).GenerateString("hello", GenerateOptions{
		MaxTokens: 3,
	})
	if err != nil {
		t.Fatalf("GenerateString: %v", err)
	}
	if text == "" {
		t.Fatal("expected non-empty generated text")
	}
}

func TestContinueNilSession(t *testing.T) {
	g := Generator{}
	_, err := g.Continue(GenerateOptions{})
	if err == nil {
		t.Fatal("expected error for nil session")
	}
}

func TestContinueMaxTokensDefault(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, _ := eng.NewSession(128)
	defer sess.Close()

	toks, _ := eng.TokenizeText("hi")
	defer toks.Free()

	// With MaxTokens == 0, the default is 128.
	// Our mock generates forever, so cancel early to keep the test fast.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := (Generator{Engine: eng, Session: sess}).GenerateTokens(toks, GenerateOptions{
		MaxTokens: 0,
		Context:   ctx,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got: %v", err)
	}
}

func TestContinueContextFull(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	// Small context so generation reaches the cap quickly.
	sess, err := eng.NewSession(8)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	toks, err := eng.TokenizeText("hello world")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer toks.Free()

	// Sync consumes 2 of the 8 context slots, leaving room = 6. MaxTokens far
	// exceeds the room, so generation is capped at room-1 = 5 tokens and the
	// call returns those tokens together with ErrContextFull.
	got, err := (Generator{Engine: eng, Session: sess}).GenerateTokens(toks, GenerateOptions{
		MaxTokens: 100,
	})

	if !errors.Is(err, ErrContextFull) {
		t.Fatalf("expected ErrContextFull, got: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 generated tokens (room-1), got %d", len(got))
	}
}
