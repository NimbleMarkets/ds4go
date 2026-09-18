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

func TestQwenAssistantHistoryChatML(t *testing.T) {
	eng, _ := qwenMockEngine(t)
	defer eng.Close()
	for _, tc := range []struct {
		name, content, reasoning, body string
	}{
		{"plain", "hello", "", "<think>\n\n</think>\n\nhello"},
		{"reasoning", "hello", " plan ", "<think>\nplan\n</think>\n\nhello"},
		{"whitespace", "  hello \n", "", "<think>\n\n</think>\n\nhello \n"},
		{"inline reasoning", "<think>plan</think>hello", "", "<think>plan</think>hello"},
		{"closed thinking", "</think>hello", "", "</think>hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := []ChatMessage{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: tc.content, ReasoningContent: tc.reasoning},
				{Role: "user", Content: "continue"},
			}
			want := "<|im_start|>assistant\n" + tc.body + "<|im_end|>\n"
			// Check bytes too: the mock tokenizer ignores whitespace, but the
			// real tokenizer needs the exact separators and trailing whitespace.
			rendered, err := renderPromptMessages(history, nil, promptRenderOptions{qwen: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := rendered[1]; got.content != want || !got.prerendered {
				t.Fatalf("assistant history = %+v, want rendered %q", got, want)
			}
			// Exercise engine-family dispatch for plain, registry, and
			// multimodal prompt builders, with thinking both on and off.
			for _, think := range []ThinkMode{ThinkNone, ThinkLow, ThinkMedium, ThinkHigh, ThinkMax} {
				check := func(tokens *Tokens, err error) {
					t.Helper()
					if err != nil {
						t.Fatal(err)
					}
					defer tokens.Free()
					if !containsSubsequence(tokens.Slice(), wordTokens(t, eng, want)) {
						t.Fatalf("think %d: prompt lacks Qwen assistant turn %q", think, want)
					}
				}
				check(BuildChatPrompt(eng, "", nil, history, think))
				registry := &ToolRegistry{}
				check(registry.BuildPrompt(eng, "", history, think))
				prompt, err := BuildChatPromptMultimodal(eng, nil, "", nil, history, think)
				if err != nil {
					t.Fatal(err)
				}
				check(prompt.Tokens, nil)
			}
		})
	}
}
