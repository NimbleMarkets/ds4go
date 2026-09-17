package ds4

import (
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

func qwenMockEngine(t *testing.T) (*Engine, *ds4api.MockControls) {
	t.Helper()
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetQwen4(true)
	eng, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return eng, ctl
}

// wordTokens returns the mock's whitespace-word ids for text.
func wordTokens(t *testing.T, eng *Engine, text string) []int {
	t.Helper()
	toks, err := eng.TokenizeText(text)
	if err != nil {
		t.Fatal(err)
	}
	defer toks.Free()
	return append([]int(nil), toks.Slice()...)
}

// Qwen3.8 carries its reasoning-effort instruction inside the system turn,
// ahead of the caller's system text, the way ds4-agent and ds4-server build
// it; it never gets DeepSeek's Think Max prefix. Medium and none add nothing.
func TestBuildChatPromptQwenEffortLeadsTheSystemTurn(t *testing.T) {
	eng, _ := qwenMockEngine(t)
	defer eng.Close()
	history := []ChatMessage{{Role: "user", Content: "hi"}}
	build := func(system string, think ThinkMode) []int {
		t.Helper()
		toks, err := BuildChatPrompt(eng, system, nil, history, think)
		if err != nil {
			t.Fatal(err)
		}
		defer toks.Free()
		return append([]int(nil), toks.Slice()...)
	}
	low := wordTokens(t, eng, eng.Qwen4ReasoningEffortText(ThinkLow))
	xhigh := wordTokens(t, eng, eng.Qwen4ReasoningEffortText(ThinkHigh))
	maxPrefix := wordTokens(t, eng, "<think_max>")
	sys := wordTokens(t, eng, "SYSTEMTEXT")

	got := build("SYSTEMTEXT", ThinkLow)
	if !containsSubsequence(got, low) {
		t.Errorf("ThinkLow prompt lacks the low instruction: %v", got)
	}
	if !containsSubsequence(got, sys) {
		t.Errorf("ThinkLow prompt lacks the system text: %v", got)
	}
	if li, si := indexOfSubsequence(got, low), indexOfSubsequence(got, sys); li < 0 || si < 0 || li > si {
		t.Errorf("effort instruction (%d) must precede the system text (%d)", li, si)
	}
	if got := build("", ThinkMax); !containsSubsequence(got, xhigh) || containsSubsequence(got, maxPrefix) {
		t.Errorf("ThinkMax prompt = %v, want the xhigh instruction and no DeepSeek Think Max prefix", got)
	}
	for _, think := range []ThinkMode{ThinkMedium, ThinkNone} {
		got := build("SYSTEMTEXT", think)
		if containsSubsequence(got, low) || containsSubsequence(got, xhigh) {
			t.Errorf("think %d prompt carries an effort instruction: %v", think, got)
		}
		if !containsSubsequence(got, sys) {
			t.Errorf("think %d prompt lacks the system text: %v", think, got)
		}
	}
	// Names survive the round trip through the root package.
	if ThinkLow != ds4api.ThinkLow || ThinkMedium != ds4api.ThinkMedium {
		t.Error("root ThinkLow/ThinkMedium differ from ds4api's")
	}
	if !strings.HasPrefix(eng.Qwen4ReasoningEffortText(ThinkLow), "Reasoning effort is set to low") {
		t.Error("root engine lacks the Qwen effort text")
	}
}

func indexOfSubsequence(got, want []int) int {
	for i := 0; i+len(want) <= len(got); i++ {
		match := true
		for j := range want {
			if got[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
