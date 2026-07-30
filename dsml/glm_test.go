package dsml

import "testing"

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
