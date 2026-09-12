package ds4

import (
	"testing"

	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestToolSyntaxPicksDSML41ForV41Engines(t *testing.T) {
	eng, ctl := glmMockEngine(t)
	if got := ToolSyntax(eng); got != dsml.SyntaxDSML {
		t.Errorf("V4 engine syntax = %v, want dsml", got)
	}
	ctl.SetDeepSeek41(true)
	if got := ToolSyntax(eng); got != dsml.SyntaxDSML41 {
		t.Errorf("V4.1 engine syntax = %v, want dsml41", got)
	}
	ctl.SetGLM(true)
	if got := ToolSyntax(eng); got != dsml.SyntaxGLM {
		t.Errorf("GLM wins over V4.1: got %v", got)
	}
}

// V4.1 renders parallel tool results in the order the assistant issued the
// calls, whatever order they arrived in (upstream ds41_order_messages). V4
// keeps arrival order.
func TestBuildChatPromptDSML41OrdersToolResultsByCallOrder(t *testing.T) {
	history := []ChatMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "a", Arguments: "{}"}, {ID: "c2", Name: "b", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "c2", Content: " resultTWO "},
		{Role: "tool", ToolCallID: "c1", Content: " resultONE "},
	}
	position := func(t *testing.T, eng *Engine, word string) int {
		t.Helper()
		toks, err := BuildChatPrompt(eng, "", nil, history, ThinkNone)
		if err != nil {
			t.Fatal(err)
		}
		defer toks.Free()
		id := renderedTokensRole(t, eng, "", word)
		got := toks.Slice()
		for i := range got {
			if got[i] == id[len(id)-1] {
				return i
			}
		}
		t.Fatalf("%q not in prompt %v", word, got)
		return -1
	}
	eng, ctl := glmMockEngine(t)
	if position(t, eng, "resultTWO") > position(t, eng, "resultONE") {
		t.Error("V4: tool results were reordered")
	}
	ctl.SetDeepSeek41(true)
	if position(t, eng, "resultONE") > position(t, eng, "resultTWO") {
		t.Error("V4.1: tool results not in call order")
	}
}
