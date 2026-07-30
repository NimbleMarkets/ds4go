package ds4api

import "testing"

// openMockEngine opens an engine on a mock library for GLM predicate tests.
func openMockEngine(t *testing.T, lib *Library) *Engine {
	t.Helper()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(eng.Close)
	return eng
}

func TestEngineIsGLMDSA(t *testing.T) {
	lib := NewMockLibrary()
	eng := openMockEngine(t, lib)
	if !eng.IsGLMDSA() {
		t.Error("IsGLMDSA() = false, want true (mock reports the GLM DSA family)")
	}
}

func TestEngineIsGLMDSAFalseWhenUnsupported(t *testing.T) {
	lib := NewMockLibrary()
	lib.raw.ds4EngineIsGLMDSA = nil
	eng := openMockEngine(t, lib)
	if eng.IsGLMDSA() {
		t.Error("IsGLMDSA() = true on a library without ds4_engine_is_glm_dsa, want false")
	}
	if lib.SupportsGLM() {
		t.Error("SupportsGLM() = true without ds4_engine_is_glm_dsa, want false")
	}
}

// GLM stops generation on the role tokens, not just EOS, so the engine
// predicate must be consulted rather than comparing against TokenEOS.
func TestEngineTokenIsStopUsesLibraryPredicate(t *testing.T) {
	lib := NewMockLibrary()
	eng := openMockEngine(t, lib)

	user := eng.TokenUser()
	if user == eng.TokenEOS() {
		t.Fatalf("mock user token %d collides with EOS; test cannot distinguish", user)
	}
	if !eng.TokenIsStop(user) {
		t.Errorf("TokenIsStop(user=%d) = false, want true", user)
	}
	if !eng.TokenIsStop(eng.TokenEOS()) {
		t.Error("TokenIsStop(EOS) = false, want true")
	}
	if eng.TokenIsStop(4242) {
		t.Error("TokenIsStop(ordinary token) = true, want false")
	}
}

// Older libds4 builds predate ds4_token_is_stop. Callers must still get correct
// DeepSeek behaviour, where EOS is the only generation stop.
func TestEngineTokenIsStopFallsBackToEOS(t *testing.T) {
	lib := NewMockLibrary()
	lib.raw.ds4TokenIsStop = nil
	lib.raw.ds4TokenIsStopForThinkMode = nil
	eng := openMockEngine(t, lib)

	if !eng.TokenIsStop(eng.TokenEOS()) {
		t.Error("TokenIsStop(EOS) = false without ds4_token_is_stop, want true")
	}
	if eng.TokenIsStop(eng.TokenUser()) {
		t.Error("TokenIsStop(user) = true without ds4_token_is_stop, want false (DeepSeek semantics)")
	}
	if !eng.TokenIsStopForThinkMode(eng.TokenEOS(), ThinkNone) {
		t.Error("TokenIsStopForThinkMode(EOS) = false without the symbol, want true")
	}
}

func TestEngineTokenIsThinkingControl(t *testing.T) {
	lib := NewMockLibrary()
	eng := openMockEngine(t, lib)

	if !eng.TokenIsThinkingControl(int(mockThinkStartToken)) {
		t.Errorf("TokenIsThinkingControl(%d) = false, want true", mockThinkStartToken)
	}
	if eng.TokenIsThinkingControl(4242) {
		t.Error("TokenIsThinkingControl(ordinary token) = true, want false")
	}

	lib.raw.ds4TokenIsThinkingControl = nil
	if eng.TokenIsThinkingControl(int(mockThinkStartToken)) {
		t.Error("TokenIsThinkingControl = true without the symbol, want false")
	}
}

// In no-thinking mode a stray thinking tag is a control marker, not content,
// so it must terminate the completion alongside the ordinary stop set.
func TestEngineTokenIsStopForThinkMode(t *testing.T) {
	lib := NewMockLibrary()
	eng := openMockEngine(t, lib)

	if !eng.TokenIsStopForThinkMode(int(mockThinkStartToken), ThinkNone) {
		t.Error("TokenIsStopForThinkMode(<think>, ThinkNone) = false, want true")
	}
	if eng.TokenIsStopForThinkMode(int(mockThinkStartToken), ThinkHigh) {
		t.Error("TokenIsStopForThinkMode(<think>, ThinkHigh) = true, want false")
	}
	if !eng.TokenIsStopForThinkMode(eng.TokenEOS(), ThinkHigh) {
		t.Error("TokenIsStopForThinkMode(EOS, ThinkHigh) = false, want true")
	}
}

func TestGLMReasoningEffortText(t *testing.T) {
	lib := NewMockLibrary()

	cases := []struct {
		mode ThinkMode
		want string
	}{
		{ThinkNone, ""},
		{ThinkHigh, "Reasoning Effort: High"},
		{ThinkMax, "Reasoning Effort: Max"},
	}
	for _, c := range cases {
		if got := lib.GLMReasoningEffortText(c.mode); got != c.want {
			t.Errorf("GLMReasoningEffortText(%v) = %q, want %q", c.mode, got, c.want)
		}
	}

	lib.raw.ds4GLMReasoningEffortText = nil
	if got := lib.GLMReasoningEffortText(ThinkHigh); got != "" {
		t.Errorf("GLMReasoningEffortText without the symbol = %q, want %q", got, "")
	}
}

func TestEnginePrefillChunk(t *testing.T) {
	lib := NewMockLibrary()
	eng := openMockEngine(t, lib)

	if got := eng.PrefillChunk(); got != mockPrefillChunk {
		t.Errorf("PrefillChunk() = %d, want %d", got, mockPrefillChunk)
	}

	lib.raw.ds4EnginePrefillChunk = nil
	if got := eng.PrefillChunk(); got != 0 {
		t.Errorf("PrefillChunk() without the symbol = %d, want 0", got)
	}
}

func TestSessionPrefillCap(t *testing.T) {
	lib := NewMockLibrary()
	eng := openMockEngine(t, lib)
	sess, err := eng.NewSession(4096)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	if got := sess.PrefillCap(); got != int(mockPrefillChunk) {
		t.Errorf("PrefillCap() = %d, want %d", got, mockPrefillChunk)
	}

	lib.raw.ds4SessionPrefillCap = nil
	if got := sess.PrefillCap(); got != 0 {
		t.Errorf("PrefillCap() without the symbol = %d, want 0", got)
	}
}
