//go:build ds4_integration

// Real-library checks for the session and engine semantics ported from
// upstream ds4 6289c51. Run with:
//
//	DS4_LIB=~/.ds4/lib/libds4.dylib DS4_MODEL=~/.ds4/models/ds4flash.gguf \
//	  go test -tags ds4_integration -timeout 30m ./ds4api/ -run TestRealLibrary
//
// DS4_MODEL may be a DeepSeek or a GLM GGUF; the rewind expectations branch on
// the loaded family. DS4_GLM_MODEL (a GLM 5.3 GGUF) additionally enables the
// embedded-MTP check. Model loads are slow and memory-heavy, so each test opens
// one engine and closes it before the next.

package ds4api

import (
	"os"
	"testing"
)

// realBackend picks the graph backend for real-library runs from DS4_BACKEND
// (metal, cuda, rocm, cpu); the default is Metal, the original test host.
func realBackend(t *testing.T) Backend {
	t.Helper()
	switch os.Getenv("DS4_BACKEND") {
	case "", "metal":
		return BackendMetal
	case "cuda":
		return BackendCUDA
	case "cpu":
		return BackendCPU
	}
	t.Fatalf("DS4_BACKEND=%q: want metal, cuda, rocm, or cpu", os.Getenv("DS4_BACKEND"))
	return BackendMetal
}

func realLibrary(t *testing.T) *Library {
	t.Helper()
	libPath := os.Getenv("DS4_LIB")
	if libPath == "" {
		t.Skip("DS4_LIB must be set")
	}
	lib, err := Load(libPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", libPath, err)
	}
	return lib
}

// Upstream 233eeb8: ds4_session_rewind keeps the token prefix but only GLM
// retains a valid checkpoint. DeepSeek must re-sync the retained prefix
// before sampling or evaluating again; RewindSynced does that.
func TestRealLibraryRewindCheckpointSemantics(t *testing.T) {
	model := os.Getenv("DS4_MODEL")
	if model == "" {
		t.Skip("DS4_MODEL must be set")
	}
	lib := realLibrary(t)
	eng, err := lib.NewEngine(EngineOptions{ModelPath: model, Backend: realBackend(t)})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	glm := eng.IsGLMDSA()
	t.Logf("model %q glm=%v", eng.ModelName(), glm)

	sess, err := eng.NewSession(4096)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	toks, err := eng.TokenizeText("The quick brown fox jumps over the lazy dog because")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer toks.Free()
	prompt := toks.Slice()
	n := len(prompt)
	if n < 4 {
		t.Fatalf("prompt tokenized to %d tokens, want more", n)
	}

	if err := sess.Sync(prompt); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	first := sess.Argmax()
	if first < 0 {
		t.Fatalf("Argmax() after sync = %d", first)
	}
	if err := sess.Eval(first); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := sess.Pos(); got != n+1 {
		t.Fatalf("Pos() = %d, want %d", got, n+1)
	}

	// Bare rewind: the prefix is retained either way.
	sess.Rewind(n - 1)
	if got := sess.Pos(); got != n-1 {
		t.Fatalf("Pos() after Rewind = %d, want %d", got, n-1)
	}
	retained := sess.Tokens().Slice()
	if len(retained) != n-1 {
		t.Fatalf("Tokens() after Rewind has %d tokens, want %d", len(retained), n-1)
	}
	for i := range retained {
		if retained[i] != prompt[i] {
			t.Fatalf("Tokens()[%d] = %d, want %d", i, retained[i], prompt[i])
		}
	}
	argmaxAfterRewind := sess.Argmax()
	common := sess.CommonPrefix(sess.Tokens())
	evalErr := sess.Eval(prompt[n-1])
	if glm {
		// GLM 5.2 always restores state; GLM 5.3 only at its MTP rollback
		// point, so a rewind of one token may or may not keep it. Report
		// rather than assert, but RewindSynced below must still work.
		t.Logf("GLM bare rewind: argmax=%d common=%d evalErr=%v", argmaxAfterRewind, common, evalErr)
	} else {
		if argmaxAfterRewind != -1 {
			t.Errorf("DeepSeek Argmax() after bare Rewind = %d, want -1", argmaxAfterRewind)
		}
		if common != 0 {
			t.Errorf("DeepSeek CommonPrefix() after bare Rewind = %d, want 0", common)
		}
		if evalErr == nil {
			t.Error("DeepSeek Eval() after bare Rewind succeeded, want checkpoint error")
		} else {
			t.Logf("DeepSeek Eval() after bare Rewind: %v", evalErr)
		}
	}

	// RewindSynced restores a usable checkpoint on every family.
	if err := sess.Sync(prompt); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := sess.Eval(first); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if err := sess.RewindSynced(n - 1); err != nil {
		t.Fatalf("RewindSynced(%d): %v", n-1, err)
	}
	if got := sess.Pos(); got != n-1 {
		t.Fatalf("Pos() after RewindSynced = %d, want %d", got, n-1)
	}
	if got := sess.Argmax(); got < 0 {
		t.Fatalf("Argmax() after RewindSynced = %d, want a token", got)
	}
	if got := sess.CommonPrefix(sess.Tokens()); got != n-1 {
		t.Errorf("CommonPrefix() after RewindSynced = %d, want %d", got, n-1)
	}
	if err := sess.Eval(prompt[n-1]); err != nil {
		t.Fatalf("Eval() after RewindSynced: %v", err)
	}
	if got := sess.Pos(); got != n {
		t.Fatalf("Pos() after re-eval = %d, want %d", got, n)
	}
	// Replaying the last prompt token must land on the same next-token
	// prediction as the original prefill.
	if got := sess.Argmax(); got != first {
		t.Errorf("Argmax() after RewindSynced+Eval = %d, want %d (same state as the original prefill)", got, first)
	}
}

// Upstream reports GLM's embedded MTP block through ds4_engine_mtp_draft_tokens
// alone; ds4_engine_has_mtp stays false without an external support model.
func TestRealLibraryGLMEmbeddedMTP(t *testing.T) {
	model := os.Getenv("DS4_GLM_MODEL")
	if model == "" {
		t.Skip("DS4_GLM_MODEL must be set to a GLM 5.3 GGUF")
	}
	lib := realLibrary(t)
	eng, err := lib.NewEngine(EngineOptions{
		ModelPath:           model,
		Backend:             realBackend(t),
		GLMMTP:              true,
		DsparkExactSampling: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if !eng.IsGLMDSA() {
		t.Fatalf("model %q is not GLM DSA", eng.ModelName())
	}
	if eng.HasMTP() {
		t.Error("HasMTP() = true for embedded GLM MTP, want false")
	}
	if got := eng.MTPDraftTokens(); got <= 1 {
		t.Errorf("MTPDraftTokens() = %d, want > 1 with GLMMTP", got)
	}
	if !eng.MTPExactSampling() {
		t.Error("MTPExactSampling() = false with DsparkExactSampling, want true")
	}
	if !lib.SupportsLiveSteeringFFN() {
		t.Error("SupportsLiveSteeringFFN() = false, want the upstream 87495f6 symbols")
	}
}

// The V4.1 sync's think bindings against a real library: the unified prefix
// must match what the family-specific entry points produced before, and the
// pure-Go level encoding must agree with ds4's own.
func TestRealLibraryThinkPrefix(t *testing.T) {
	lib := realLibrary(t)
	if lib.raw.ds4ChatAppendThinkPrefix == nil {
		t.Skip("library predates ds4_chat_append_think_prefix")
	}
	for _, text := range []string{"0", "25", "100"} {
		var fromLib ThinkMode
		if !lib.raw.ds4ThinkModeParseLevel(text, &fromLib) {
			t.Fatalf("ds4_think_mode_parse_level(%q) failed", text)
		}
		fromGo, err := ParseThinkLevel(text)
		if err != nil || fromGo != fromLib {
			t.Errorf("ParseThinkLevel(%q) = %d (%v), library says %d", text, fromGo, err, fromLib)
		}
		if got := lib.raw.ds4ThinkModeLevel(fromGo); int(got) != fromGo.Level() {
			t.Errorf("Level() = %d, ds4_think_mode_level = %d", fromGo.Level(), got)
		}
	}
	model := os.Getenv("DS4_MODEL")
	if model == "" {
		t.Skip("DS4_MODEL must be set for the prefix comparison")
	}
	eng, err := lib.NewEngine(EngineOptions{ModelPath: model, Backend: realBackend(t)})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	for _, mode := range []ThinkMode{ThinkNone, ThinkHigh, ThinkMax} {
		got, _ := eng.NewTokens(nil)
		want, _ := eng.NewTokens(nil)
		if err := eng.ChatAppendThinkPrefix(got, mode); err != nil {
			t.Fatal(err)
		}
		switch {
		case eng.IsGLMDSA():
			if effort := eng.GLMReasoningEffortText(mode); effort != "" {
				_ = eng.ChatAppendMessage(want, "system", effort)
			}
		case eng.IsDeepSeek41():
			if effort := eng.DeepSeek41ReasoningEffortText(mode); effort != "" {
				_ = eng.ChatAppendMessage(want, "system", effort)
			}
		case mode == ThinkMax:
			_ = eng.ChatAppendMaxEffortPrefix(want)
		}
		g, w := got.Slice(), want.Slice()
		if len(g) != len(w) || !equalInts(g, w) {
			t.Errorf("mode %d: ChatAppendThinkPrefix = %v, family path = %v", mode, g, w)
		}
		got.Free()
		want.Free()
	}
}

func equalInts(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
