package ds4

import (
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// glmMockEngine returns a mock engine plus its controls, so tests can switch
// the modelled family between DeepSeek V4 and GLM DSA.
func glmMockEngine(t *testing.T) (*Engine, *ds4api.MockControls) {
	t.Helper()
	lib, ctl := ds4api.NewMockLibraryWithControls()
	eng, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(eng.Close)
	return eng, ctl
}

// generateN runs a fresh session for exactly one generation call, so each case
// starts from an identical session position.
func generateN(t *testing.T, eng *Engine, opts GenerateOptions) []int {
	t.Helper()
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

	got, err := (Generator{Engine: eng, Session: sess}).GenerateTokens(toks, opts)
	if err != nil {
		t.Fatalf("GenerateTokens: %v", err)
	}
	return got
}

// GLM ends a turn on the role tokens, not only on EOS, so StopOnEOS must
// consult the engine stop predicate rather than comparing against TokenEOS.
func TestGenerateStopsOnNonEOSStopToken(t *testing.T) {
	eng, ctl := glmMockEngine(t)
	ctl.SetGLM(true)

	baseline := generateN(t, eng, GenerateOptions{MaxTokens: 5, StopOnEOS: true})
	if len(baseline) != 5 {
		t.Fatalf("baseline produced %d tokens, want 5", len(baseline))
	}
	if baseline[2] == eng.TokenEOS() {
		t.Fatalf("baseline token %d collides with EOS; test cannot distinguish", baseline[2])
	}

	ctl.SetStopTokens(baseline[2])
	got := generateN(t, eng, GenerateOptions{MaxTokens: 5, StopOnEOS: true})

	if len(got) != 2 {
		t.Fatalf("generated %v (%d tokens), want 2 before the stop token %d",
			got, len(got), baseline[2])
	}
}

// With thinking disabled, a stray <think>/</think> marker is a control token
// rather than assistant content and must end the completion; with thinking
// enabled the same token is ordinary output.
func TestGenerateStopsOnThinkingControlByThinkMode(t *testing.T) {
	eng, ctl := glmMockEngine(t)
	ctl.SetGLM(true)

	baseline := generateN(t, eng, GenerateOptions{MaxTokens: 5, StopOnEOS: true})
	if len(baseline) != 5 {
		t.Fatalf("baseline produced %d tokens, want 5", len(baseline))
	}
	ctl.SetThinkingControlTokens(baseline[2])

	none := generateN(t, eng, GenerateOptions{MaxTokens: 5, StopOnEOS: true, ThinkMode: ThinkNone})
	if len(none) != 2 {
		t.Errorf("ThinkNone generated %v (%d tokens), want 2 before the thinking marker",
			none, len(none))
	}

	high := generateN(t, eng, GenerateOptions{MaxTokens: 5, StopOnEOS: true, ThinkMode: ThinkHigh})
	if len(high) != 5 {
		t.Errorf("ThinkHigh generated %v (%d tokens), want 5: a thinking marker is content here",
			high, len(high))
	}
}

// Stop detection must agree with how the prompt was rendered. A thinking turn
// emits <think>/</think> markers as ordinary content, so the loop has to hand
// its own think mode to generation; leaving it at the zero value would make
// generation treat the first thinking marker as a turn-ending control token.
func TestToolLoopPassesThinkModeToGeneration(t *testing.T) {
	eng, _ := glmMockEngine(t)
	sess, err := eng.NewSession(256)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	for _, c := range []struct {
		name string
		loop ToolLoop
		want ThinkMode
	}{
		{"thinking defaults to high", ToolLoop{Thinking: true}, ThinkHigh},
		{"explicit max is preserved", ToolLoop{Thinking: true, ThinkMode: ThinkMax}, ThinkMax},
		{"non-thinking stays none", ToolLoop{}, ThinkNone},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got ThinkMode
			seen := false
			loop := c.loop
			loop.Engine, loop.Session, loop.Tools = eng, sess, NewToolRegistry()
			loop.CompleteFunc = func(prompt *Tokens, opts GenerateOptions) (string, error) {
				got, seen = opts.ThinkMode, true
				return "done", nil
			}
			if _, err := loop.Run(ToolLoopOptions{
				History: []ChatMessage{{Role: "user", Content: "hi"}},
			}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !seen {
				t.Fatal("CompleteFunc was never called")
			}
			if got != c.want {
				t.Errorf("generation ThinkMode = %v, want %v", got, c.want)
			}
		})
	}
}

// containsSubsequence reports whether want appears contiguously within got.
func containsSubsequence(got, want []int) bool {
	if len(want) == 0 || len(want) > len(got) {
		return false
	}
	for i := 0; i+len(want) <= len(got); i++ {
		match := true
		for j := range want {
			if got[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// promptTokens builds a tool-free chat prompt and returns its token ids.
func promptTokens(t *testing.T, eng *Engine, system string, think ThinkMode) []int {
	t.Helper()
	toks, err := BuildChatPrompt(eng, system, nil, []ChatMessage{{Role: "user", Content: "hi"}}, think)
	if err != nil {
		t.Fatalf("BuildChatPrompt: %v", err)
	}
	defer toks.Free()
	return append([]int(nil), toks.Slice()...)
}

// renderedTokens returns the ids for text as the mock chat renderer would emit
// them for a system message, so prompt assertions read as text not magic ids.
func renderedTokens(t *testing.T, eng *Engine, text string) []int {
	t.Helper()
	toks, err := eng.TokenizeText("system: " + text)
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer toks.Free()
	return append([]int(nil), toks.Slice()...)
}

// GLM encodes reasoning effort as a system message ("Reasoning Effort: High"),
// where DeepSeek uses the max-effort prompt prefix and only at ThinkMax.
func TestBuildChatPromptGLMReasoningEffort(t *testing.T) {
	eng, ctl := glmMockEngine(t)
	ctl.SetGLM(true)

	for _, c := range []struct {
		think ThinkMode
		want  string
	}{
		{ThinkHigh, "Reasoning Effort: High"},
		{ThinkMax, "Reasoning Effort: Max"},
	} {
		got := promptTokens(t, eng, "", c.think)
		want := renderedTokens(t, eng, c.want)
		if !containsSubsequence(got, want) {
			t.Errorf("GLM prompt at think=%v does not contain %q", c.think, c.want)
		}
	}

	// ThinkNone has no reasoning-effort line at all.
	got := promptTokens(t, eng, "", ThinkNone)
	for _, effort := range []string{"Reasoning Effort: High", "Reasoning Effort: Max"} {
		if containsSubsequence(got, renderedTokens(t, eng, effort)) {
			t.Errorf("GLM prompt at ThinkNone unexpectedly contains %q", effort)
		}
	}
}

// The DeepSeek path must be untouched: no GLM reasoning-effort system line,
// and the max-effort prefix still applied at ThinkMax.
func TestBuildChatPromptDeepSeekUnaffected(t *testing.T) {
	eng, _ := glmMockEngine(t) // not GLM

	maxPrefix, err := eng.TokenizeText("<think_max>")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer maxPrefix.Free()
	wantPrefix := append([]int(nil), maxPrefix.Slice()...)

	got := promptTokens(t, eng, "", ThinkMax)
	if !containsSubsequence(got, wantPrefix) {
		t.Error("DeepSeek prompt at ThinkMax lost the max-effort prefix")
	}
	if containsSubsequence(got, renderedTokens(t, eng, "Reasoning Effort: Max")) {
		t.Error("DeepSeek prompt contains a GLM reasoning-effort line")
	}

	high := promptTokens(t, eng, "", ThinkHigh)
	if containsSubsequence(high, wantPrefix) {
		t.Error("DeepSeek prompt at ThinkHigh gained the max-effort prefix")
	}
}
