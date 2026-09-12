package ds4api

import (
	"strings"
	"testing"
)

// Think levels mirror upstream: DS4_THINK_LEVEL_BASE + N for N in 0..100,
// outside the named modes; Level reports -1 for the named modes.
func TestThinkLevelEncodesLikeUpstream(t *testing.T) {
	if got := ThinkLevel(25); got != ThinkMode(1025) {
		t.Errorf("ThinkLevel(25) = %d, want 1025", got)
	}
	if ThinkLevel(25).Level() != 25 || ThinkLevel(0).Level() != 0 || ThinkLevel(100).Level() != 100 {
		t.Error("Level() does not round-trip")
	}
	for _, m := range []ThinkMode{ThinkNone, ThinkHigh, ThinkMax} {
		if m.Level() != -1 {
			t.Errorf("%d.Level() = %d, want -1 for a named mode", m, m.Level())
		}
	}
	for text, want := range map[string]ThinkMode{"0": ThinkLevel(0), "25": ThinkLevel(25), "100": ThinkLevel(100)} {
		got, err := ParseThinkLevel(text)
		if err != nil || got != want {
			t.Errorf("ParseThinkLevel(%q) = (%d, %v), want %d", text, got, err, want)
		}
	}
	for _, bad := range []string{"", "-1", "101", "x", "1.5", " 5"} {
		if _, err := ParseThinkLevel(bad); err == nil {
			t.Errorf("ParseThinkLevel(%q) accepted", bad)
		}
	}
}

func TestThinkModeEnabledHonoursLevelZero(t *testing.T) {
	lib, _ := NewMockLibraryWithControls()
	_ = lib
	if ThinkModeEnabled(ThinkLevel(0)) {
		t.Error("level 0 reported as thinking enabled")
	}
	if !ThinkModeEnabled(ThinkLevel(1)) || !ThinkModeEnabled(ThinkHigh) {
		t.Error("level 1 / ThinkHigh reported as disabled")
	}
	if ThinkModeEnabled(ThinkNone) {
		t.Error("ThinkNone reported as enabled")
	}
}

func TestEngineIsDeepSeek41FollowsTheLibrary(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if eng.IsDeepSeek41() {
		t.Error("fresh mock engine reports V4.1")
	}
	ctl.SetDeepSeek41(true)
	if !eng.IsDeepSeek41() {
		t.Error("IsDeepSeek41 false after SetDeepSeek41(true)")
	}
	if got := eng.DeepSeek41ReasoningEffortText(ThinkLevel(25)); !strings.HasPrefix(got, "Reasoning Effort: 25 ") {
		t.Errorf("effort text = %q", got)
	}
	if got := eng.DeepSeek41ReasoningEffortText(ThinkHigh); !strings.HasPrefix(got, "Reasoning Effort: 75 ") {
		t.Errorf("ThinkHigh effort text = %q, want level 75", got)
	}
	if got := eng.DeepSeek41ReasoningEffortText(ThinkLevel(0)); got != "" {
		t.Errorf("level 0 effort text = %q, want none", got)
	}
}

// ChatAppendThinkPrefix is upstream's chat_push_think_prefix: the family
// decides how effort is expressed. GLM and V4.1 emit a system line, V4 emits
// its max-effort prefix only at ThinkMax, and nothing is emitted otherwise.
func TestChatAppendThinkPrefixByFamily(t *testing.T) {
	type tc struct {
		name     string
		glm, v41 bool
		mode     ThinkMode
		want     func(eng *Engine, tokens *Tokens)
	}
	cases := []tc{
		{"v4 high", false, false, ThinkHigh, func(*Engine, *Tokens) {}},
		{"v4 max", false, false, ThinkMax, func(e *Engine, tk *Tokens) { _ = e.ChatAppendMaxEffortPrefix(tk) }},
		{"glm high", true, false, ThinkHigh, func(e *Engine, tk *Tokens) {
			_ = e.ChatAppendMessage(tk, "system", e.GLMReasoningEffortText(ThinkHigh))
		}},
		{"glm none", true, false, ThinkNone, func(*Engine, *Tokens) {}},
		{"v41 level 25", false, true, ThinkLevel(25), func(e *Engine, tk *Tokens) {
			_ = e.ChatAppendMessage(tk, "system", e.DeepSeek41ReasoningEffortText(ThinkLevel(25)))
		}},
		{"v41 level 0", false, true, ThinkLevel(0), func(*Engine, *Tokens) {}},
	}
	for _, c := range cases {
		lib, ctl := NewMockLibraryWithControls()
		ctl.SetGLM(c.glm)
		ctl.SetDeepSeek41(c.v41)
		eng, err := lib.NewEngine(EngineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := eng.NewTokens(nil)
		if err := eng.ChatAppendThinkPrefix(got, c.mode); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		want, _ := eng.NewTokens(nil)
		c.want(eng, want)
		if g, w := got.Slice(), want.Slice(); len(g) != len(w) || !equalTokens(g, w) {
			t.Errorf("%s: tokens = %v, want %v", c.name, g, w)
		}
		got.Free()
		want.Free()
		eng.Close()
	}
}

func equalTokens(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
