//go:build ds4_integration

// Real-library checks for Generator's speculative-block handling. Run with:
//
//	DS4_LIB=~/.ds4/lib/libds4.dylib DS4_MODEL=~/.ds4/models/ds4flash.gguf \
//	DS4_MTP_MODEL=~/.ds4/models/DeepSeek-V4-Flash-MTP-Q4K-Q8_0-F32.gguf \
//	  go test -tags ds4_integration -timeout 30m . -run TestRealLibrary
//
// DS4_GLM_MODEL (a GLM 5.3 GGUF) runs the same invariants with the embedded
// MTP block instead of an external support model.

package ds4

import (
	"os"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// After every Generator.Continue the session position must equal the prompt
// length plus the tokens returned, and the session's token history must end
// with exactly those tokens: no draft evaluated past a stop, a cancel, or a
// resampled boundary may linger (upstream 5b3cc8b, 930ab73).
func checkSpeculativeInvariants(t *testing.T, eng *Engine, name string, opts GenerateOptions) {
	t.Helper()
	sess, err := eng.NewSession(4096)
	if err != nil {
		t.Fatalf("%s: NewSession: %v", name, err)
	}
	defer sess.Close()
	toks, err := eng.TokenizeText("Write one short sentence about the sea.")
	if err != nil {
		t.Fatalf("%s: TokenizeText: %v", name, err)
	}
	defer toks.Free()
	prompt := toks.Slice()

	out, err := (Generator{Engine: eng, Session: sess}).GenerateTokens(toks, opts)
	if err != nil {
		t.Fatalf("%s: GenerateTokens: %v", name, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s: generated no tokens", name)
	}
	var text string
	for _, tok := range out {
		if piece, err := eng.TokenText(tok); err == nil {
			text += piece
		}
	}
	t.Logf("%s: %d tokens: %q", name, len(out), text)

	if got, want := sess.Pos(), len(prompt)+len(out); got != want {
		t.Errorf("%s: Pos() = %d, want %d (prompt %d + generated %d)", name, got, want, len(prompt), len(out))
	}
	hist := sess.Tokens().Slice()
	if len(hist) != len(prompt)+len(out) {
		t.Fatalf("%s: session history has %d tokens, want %d", name, len(hist), len(prompt)+len(out))
	}
	for i, tok := range out {
		if hist[len(prompt)+i] != tok {
			t.Fatalf("%s: history[%d] = %d, want generated token %d", name, len(prompt)+i, hist[len(prompt)+i], tok)
		}
	}
	if sess.Argmax() < 0 {
		t.Errorf("%s: session left without a valid checkpoint", name)
	}
	// A continuation must be able to keep going from that state.
	more, err := (Generator{Engine: eng, Session: sess}).Continue(GenerateOptions{MaxTokens: 4, StopOnEOS: true, Temperature: opts.Temperature})
	if err != nil {
		t.Errorf("%s: Continue after generation: %v", name, err)
	}
	if got, want := sess.Pos(), len(prompt)+len(out)+len(more); got != want {
		t.Errorf("%s: Pos() after Continue = %d, want %d", name, got, want)
	}
}

func runSpeculativeSuite(t *testing.T, eng *Engine) {
	if got := eng.MTPDraftTokens(); got <= 1 {
		t.Fatalf("MTPDraftTokens() = %d, want > 1 so speculation engages", got)
	}
	t.Logf("model %q glm=%v hasMTP=%v draft=%d exact=%v", eng.ModelName(), eng.IsGLMDSA(), eng.HasMTP(), eng.MTPDraftTokens(), eng.MTPExactSampling())

	checkSpeculativeInvariants(t, eng, "greedy", GenerateOptions{MaxTokens: 24, StopOnEOS: true})
	checkSpeculativeInvariants(t, eng, "sampled", GenerateOptions{MaxTokens: 24, StopOnEOS: true, Temperature: 0.7, Seed: 7})

	// Mode flip inside a block: greedy for the first 5 tokens, sampled after.
	seen := 0
	checkSpeculativeInvariants(t, eng, "mode-flip", GenerateOptions{
		MaxTokens:     24,
		StopOnEOS:     true,
		Temperature:   0.7,
		Seed:          7,
		SampleControl: func() bool { return seen < 5 },
		OnToken:       func(int) { seen++ },
	})
}

func TestRealLibrarySpeculativeInvariantsDeepSeek(t *testing.T) {
	libPath, model, mtp := os.Getenv("DS4_LIB"), os.Getenv("DS4_MODEL"), os.Getenv("DS4_MTP_MODEL")
	if libPath == "" || model == "" || mtp == "" {
		t.Skip("DS4_LIB, DS4_MODEL, and DS4_MTP_MODEL must be set")
	}
	lib, err := ds4api.Load(libPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, exact := range []bool{false, true} {
		eng, err := lib.NewEngine(ds4api.EngineOptions{
			ModelPath:           model,
			MTPPath:             mtp,
			MTPDraftTokens:      4,
			Backend:             ds4api.BackendMetal,
			DsparkExactSampling: exact,
		})
		if err != nil {
			t.Fatalf("NewEngine(exact=%v): %v", exact, err)
		}
		t.Run(map[bool]string{false: "opportunistic", true: "exact"}[exact], func(t *testing.T) {
			runSpeculativeSuite(t, eng)
		})
		eng.Close()
	}
}

func TestRealLibrarySpeculativeInvariantsGLM(t *testing.T) {
	libPath, model := os.Getenv("DS4_LIB"), os.Getenv("DS4_GLM_MODEL")
	if libPath == "" || model == "" {
		t.Skip("DS4_LIB and DS4_GLM_MODEL must be set")
	}
	lib, err := ds4api.Load(libPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, exact := range []bool{false, true} {
		eng, err := lib.NewEngine(ds4api.EngineOptions{
			ModelPath:           model,
			GLMMTP:              true,
			Backend:             ds4api.BackendMetal,
			DsparkExactSampling: exact,
		})
		if err != nil {
			t.Fatalf("NewEngine(exact=%v): %v", exact, err)
		}
		if eng.HasMTP() {
			t.Error("HasMTP() = true for embedded GLM MTP, want false")
		}
		t.Run(map[bool]string{false: "opportunistic", true: "exact"}[exact], func(t *testing.T) {
			runSpeculativeSuite(t, eng)
		})
		eng.Close()
	}
}
