package ds4

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestToolSyntaxQwen(t *testing.T) {
	eng, ctl := qwenMockEngine(t)
	defer eng.Close()
	if got := ToolSyntax(eng); got != dsml.SyntaxQwen {
		t.Errorf("ToolSyntax(Qwen engine) = %v, want %v", got, dsml.SyntaxQwen)
	}
	// GLM wins over Qwen, matching agent_tool_syntax_for_engine's order.
	ctl.SetGLM(true)
	if got := ToolSyntax(eng); got != dsml.SyntaxGLM {
		t.Errorf("ToolSyntax(GLM+Qwen) = %v, want GLM", got)
	}
}

// A Qwen tool result goes back under the "tool" role so libds4 wraps it in
// a user turn carrying <tool_response> (ds4_chat_append_message); assistant
// calls render in Qwen's XML after the content, separated by a blank line
// as ds4-server's append_qwen_tool_calls_text does.
func TestBuildChatPromptQwenToolTurns(t *testing.T) {
	eng, _ := qwenMockEngine(t)
	defer eng.Close()
	history := []ChatMessage{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "Calling.", ToolCalls: []ToolCall{{ID: "c1", Name: "add", Arguments: `{"a": 1, "b": 2}`}}},
		{Role: "tool", ToolCallID: "c1", Content: "3"},
	}
	rendered, err := renderPromptMessages(history, nil, promptRenderOptions{syntax: dsml.SyntaxQwen, qwen: true, toolContext: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 3 {
		t.Fatalf("rendered %d turns, want 3", len(rendered))
	}
	wantCall := "Calling.\n\n<tool_call>\n<function=add>\n<parameter=a>\n1\n</parameter>\n<parameter=b>\n2\n</parameter>\n</function>\n</tool_call>"
	if a := rendered[1]; !a.prerendered || !strings.Contains(a.content, wantCall) {
		t.Errorf("assistant turn = %+v, want it to contain %q", a, wantCall)
	}
	if r := rendered[2]; r.role != "tool" || r.content != "3" {
		t.Errorf("tool turn = %+v, want role tool with the raw content", r)
	}
	toks, err := BuildChatPrompt(eng, "", []dsml.Tool{{Name: "add", Parameters: json.RawMessage("{}")}}, history, ThinkNone)
	if err != nil {
		t.Fatal(err)
	}
	toks.Free()
}

// The registry parses Qwen completions with the Qwen grammar and coerces
// argument types from the tool schema, as it does for GLM.
func TestToolRegistryParseAssistantQwen(t *testing.T) {
	reg := NewToolRegistry()
	reg.MustRegister(Tool{
		ToolSchema: ToolSchema{Name: "bash", Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"refresh_sec":{"type":"integer"},"tag":{"type":"string"}}}`)},
		Handler:    func(context.Context, json.RawMessage) (string, error) { return "", nil },
	})
	text := "<tool_call>\n<function=bash>\n<parameter=command>\nprintf hi\n</parameter>\n<parameter=refresh_sec>\n1\n</parameter>\n<parameter=tag>\n42\n</parameter>\n</function>\n</tool_call>"
	msg, err := reg.ParseAssistantSyntax(dsml.SyntaxQwen, text, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID == "" {
		t.Fatalf("msg = %+v", msg)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(msg.ToolCalls[0].Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["command"] != "printf hi" || args["refresh_sec"] != 1.0 || args["tag"] != "42" {
		t.Errorf("arguments = %s (schema says tag is a string)", msg.ToolCalls[0].Arguments)
	}
	// A malformed call reports Qwen's guidance.
	bad, err := reg.ParseAssistantSyntax(dsml.SyntaxQwen, "<tool_call>\n<function=bash>\n</tool_call>", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad.ToolCalls) != 0 || bad.MalformedReason == "" {
		t.Errorf("malformed = %+v", bad)
	}
	if !strings.Contains(dsml.ToolSyntaxErrorMessageSyntax(dsml.SyntaxQwen, bad.MalformedReason), "invalid Qwen tool call") {
		t.Error("error message is not the Qwen form")
	}
}
