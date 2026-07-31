package dsml

import (
	"encoding/json"
	"strings"
	"testing"
)

// These cases are ported from upstream ds4's GLM parser tests in ds4_agent.c
// (test_agent_glm_tool_parser_*), so ds4go's grammar stays bug-compatible with
// the engine that produces the markup.

func TestGLMParseSingleArg(t *testing.T) {
	// test_agent_glm_tool_parser_single_arg
	const text = "prose before <tool_call>list" +
		"<arg_key>path</arg_key><arg_value>.</arg_value>" +
		"</tool_call>"

	msg, err := ParseCompletionSyntax(SyntaxGLM, text, false)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if msg.MalformedReason != "" {
		t.Fatalf("MalformedReason = %q, want none", msg.MalformedReason)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	if got := msg.ToolCalls[0].Name; got != "list" {
		t.Errorf("Name = %q, want %q", got, "list")
	}
	if got := msg.ToolCalls[0].Arguments; got != `{"path": "."}` {
		t.Errorf("Arguments = %q, want %q", got, `{"path": "."}`)
	}
	if got := msg.Content; got != "prose before" {
		t.Errorf("Content = %q, want %q", got, "prose before")
	}
}

func TestGLMParseMultipleAdjacentCalls(t *testing.T) {
	// test_agent_glm_tool_parser_multiple_adjacent_calls
	const text = "<tool_call>list<arg_key>path</arg_key><arg_value>.</arg_value></tool_call>\n" +
		"<tool_call>bash<arg_key>command</arg_key><arg_value>pwd</arg_value></tool_call>"

	msg, err := ParseCompletionSyntax(SyntaxGLM, text, false)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if msg.MalformedReason != "" {
		t.Fatalf("MalformedReason = %q, want none", msg.MalformedReason)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(msg.ToolCalls))
	}
	if got := msg.ToolCalls[0].Name; got != "list" {
		t.Errorf("call 0 Name = %q, want %q", got, "list")
	}
	if got := msg.ToolCalls[0].Arguments; got != `{"path": "."}` {
		t.Errorf("call 0 Arguments = %q, want %q", got, `{"path": "."}`)
	}
	if got := msg.ToolCalls[1].Name; got != "bash" {
		t.Errorf("call 1 Name = %q, want %q", got, "bash")
	}
	if got := msg.ToolCalls[1].Arguments; got != `{"command": "pwd"}` {
		t.Errorf("call 1 Arguments = %q, want %q", got, `{"command": "pwd"}`)
	}
}

func TestGLMParseMultipleArgsPreserveOrder(t *testing.T) {
	// Argument half of test_agent_glm_tool_parser_chunked_multi_arg; the
	// chunked feeding it also covers belongs to the stream decoder.
	const text = "<tool_call>bash<arg_key>command</arg_key><arg_value>printf hi</arg_value>" +
		"<arg_key>refresh_sec</arg_key><arg_value>1</arg_value></tool_call>"

	msg, err := ParseCompletionSyntax(SyntaxGLM, text, false)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	// GLM arg values are always strings, so refresh_sec stays quoted.
	want := `{"command": "printf hi", "refresh_sec": "1"}`
	if got := msg.ToolCalls[0].Arguments; got != want {
		t.Errorf("Arguments = %q, want %q", got, want)
	}
}

// Thinking-mode handling is shared with DSML: markup is executable only after
// the final </think>.
func TestGLMParseToolCallInsideThinkingIsNotExecuted(t *testing.T) {
	const text = "<think>plan <tool_call>bash<arg_key>command</arg_key>" +
		"<arg_value>printf hi</arg_value></tool_call>"

	msg, err := ParseCompletionSyntax(SyntaxGLM, text, true)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("got %d tool calls inside unclosed thinking, want 0", len(msg.ToolCalls))
	}
}

func TestGLMParseErrors(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"no function name", "<tool_call><arg_key>path</arg_key><arg_value>.</arg_value></tool_call>"},
		{"unterminated arg_key", "<tool_call>list<arg_key>path</tool_call>"},
		{"missing arg_value", "<tool_call>list<arg_key>path</arg_key>oops</tool_call>"},
		{"empty arg_key", "<tool_call>list<arg_key></arg_key><arg_value>.</arg_value></tool_call>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, err := ParseCompletionSyntax(SyntaxGLM, c.text, false)
			if err != nil {
				t.Fatalf("ParseCompletionSyntax: %v", err)
			}
			if len(msg.ToolCalls) != 0 {
				t.Fatalf("got %d tool calls, want 0 for malformed input", len(msg.ToolCalls))
			}
			if msg.MalformedReason == "" {
				t.Error("MalformedReason is empty, want a parse failure reason")
			}
		})
	}
}

// A GLM completion with no markup is plain content, and DSML markers carry no
// meaning under the GLM grammar.
func TestGLMParsePlainContent(t *testing.T) {
	msg, err := ParseCompletionSyntax(SyntaxGLM, "just a reply", false)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if msg.Content != "just a reply" {
		t.Errorf("Content = %q, want %q", msg.Content, "just a reply")
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("got %d tool calls, want 0", len(msg.ToolCalls))
	}
}

// The default syntax stays DSML, so existing callers are unaffected.
func TestParseCompletionDefaultsToDSMLSyntax(t *testing.T) {
	const text = "<tool_call>list<arg_key>path</arg_key><arg_value>.</arg_value></tool_call>"

	msg, err := ParseCompletion(text, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("DSML parse found %d tool calls in GLM markup, want 0", len(msg.ToolCalls))
	}
}

func TestGLMRenderToolCall(t *testing.T) {
	got, err := RenderToolCallsSyntax(SyntaxGLM, []ToolCall{{
		Name:      "bash",
		Arguments: `{"command": "printf hi", "refresh_sec": 1}`,
	}})
	if err != nil {
		t.Fatalf("RenderToolCallsSyntax: %v", err)
	}
	// Non-string JSON values render as their literal text: GLM has no type
	// marker, and the parser reads every value back as a string.
	want := "<tool_call>bash" +
		"<arg_key>command</arg_key><arg_value>printf hi</arg_value>" +
		"<arg_key>refresh_sec</arg_key><arg_value>1</arg_value>" +
		"</tool_call>"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// Rendering a parsed call and parsing it again must be a fixed point, since
// multi-turn prompts replay assistant tool calls back to the model.
func TestGLMRenderToolCallRoundTrips(t *testing.T) {
	const text = "<tool_call>bash<arg_key>command</arg_key>" +
		"<arg_value>printf hi</arg_value></tool_call>"

	msg, err := ParseCompletionSyntax(SyntaxGLM, text, false)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	rendered, err := RenderToolCallsSyntax(SyntaxGLM, msg.ToolCalls)
	if err != nil {
		t.Fatalf("RenderToolCallsSyntax: %v", err)
	}
	again, err := ParseCompletionSyntax(SyntaxGLM, rendered, false)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(again.ToolCalls) != 1 {
		t.Fatalf("re-parsed %d calls, want 1", len(again.ToolCalls))
	}
	if again.ToolCalls[0].Name != msg.ToolCalls[0].Name ||
		again.ToolCalls[0].Arguments != msg.ToolCalls[0].Arguments {
		t.Errorf("round trip changed the call: %+v -> %+v", msg.ToolCalls[0], again.ToolCalls[0])
	}
}

func TestGLMRenderMultipleToolCalls(t *testing.T) {
	got, err := RenderToolCallsSyntax(SyntaxGLM, []ToolCall{
		{Name: "list", Arguments: `{"path": "."}`},
		{Name: "bash", Arguments: `{"command": "pwd"}`},
	})
	if err != nil {
		t.Fatalf("RenderToolCallsSyntax: %v", err)
	}
	msg, err := ParseCompletionSyntax(SyntaxGLM, got, false)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("re-parsed %d calls from %q, want 2", len(msg.ToolCalls), got)
	}
}

func TestGLMRenderToolsSection(t *testing.T) {
	section, err := RenderToolsSectionSyntax(SyntaxGLM, []Tool{{
		Name:        "list",
		Description: "List one directory.",
		Parameters:  []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}})
	if err != nil {
		t.Fatalf("RenderToolsSectionSyntax: %v", err)
	}
	// Structural expectations lifted from ds4's agent_glm_tools_prompt_*.
	for _, want := range []string{
		"<tools>", "</tools>",
		`"name": "list"`,
		"<tool_call>{function-name}<arg_key>{arg-key-1}</arg_key>",
		"Tool calls are not allowed inside <think></think>",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("GLM tools section missing %q\n---\n%s", want, section)
		}
	}
	if strings.Contains(section, dsmlMarker) {
		t.Error("GLM tools section leaked DSML markers")
	}
}

func TestGLMRenderToolsSectionEmpty(t *testing.T) {
	section, err := RenderToolsSectionSyntax(SyntaxGLM, nil)
	if err != nil {
		t.Fatalf("RenderToolsSectionSyntax: %v", err)
	}
	if section != "" {
		t.Errorf("empty tool list rendered %q, want \"\"", section)
	}
}

func TestGLMToolSyntaxErrorMessage(t *testing.T) {
	msg := ToolSyntaxErrorMessageSyntax(SyntaxGLM, "expected <arg_key> in GLM tool call")
	for _, want := range []string{
		"expected <arg_key> in GLM tool call",
		"<tool_call>$TOOL_NAME<arg_key>$PARAMETER_NAME</arg_key>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("GLM syntax error message missing %q\n---\n%s", want, msg)
		}
	}
	if strings.Contains(msg, dsmlMarker) {
		t.Error("GLM syntax error message leaked DSML markers")
	}
}

// ds4's GLM parser does no entity unescaping, and the GLM tools prompt (unlike
// DSML's) never asks the model to escape. A literal entity in an argument --
// HTML being written to a file, a shell command -- must therefore survive
// verbatim rather than being decoded. The streaming events and the final
// message must agree on that.
func TestGLMArgValueEntitiesArePreserved(t *testing.T) {
	const text = "<tool_call>write<arg_key>content</arg_key>" +
		"<arg_value>&lt;div&gt; &amp; more</arg_value></tool_call>"
	const want = "&lt;div&gt; &amp; more"

	msg, err := ParseCompletionSyntax(SyntaxGLM, text, false)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	if got := argValue(t, msg.ToolCalls[0].Arguments, "content"); got != want {
		t.Errorf("parsed content = %q, want %q", got, want)
	}

	// The streamed EventToolCallEnd must carry the same value as the final
	// parse; otherwise a streaming caller and a batch caller disagree.
	d := NewStreamDecoderSyntax(SyntaxGLM, false)
	events := d.Write(text)
	tail, streamed, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	events = append(events, tail...)

	if got := argValue(t, streamed.ToolCalls[0].Arguments, "content"); got != want {
		t.Errorf("streamed message content = %q, want %q", got, want)
	}
	for _, e := range events {
		if e.Type == EventToolCallEnd {
			if got := argValue(t, e.Arguments, "content"); got != want {
				t.Errorf("EventToolCallEnd content = %q, want %q", got, want)
			}
		}
	}
}

// argValue decodes one key out of a JSON arguments object.
func argValue(t *testing.T, argsJSON, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		t.Fatalf("arguments %q are not valid JSON: %v", argsJSON, err)
	}
	s, _ := m[key].(string)
	return s
}
