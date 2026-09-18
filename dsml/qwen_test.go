package dsml

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ported from upstream ds4's Qwen parser tests in ds4_agent.c
// (test_agent_qwen_tool_parser_*, test_agent_tool_argument_literal_markup) and
// ds4-server's parse_qwen_generated_message_ex / append_qwen_tool_calls_text.

const qwenTwoArgCall = "<tool_call>\n<function=bash>\n<parameter=command>\nprintf hi\n</parameter>\n" +
	"<parameter=refresh_sec>\n1\n</parameter>\n</function>\n</tool_call>"

func TestQwenParseTypesValuesLikeUpstream(t *testing.T) {
	// test_agent_qwen_tool_parser_chunked_multi_arg: one newline is stripped
	// from each end of a value; a value that reads as JSON is not a string.
	msg, err := ParseCompletionSyntax(SyntaxQwen, "intro "+qwenTwoArgCall, false)
	if err != nil {
		t.Fatal(err)
	}
	if msg.MalformedReason != "" {
		t.Fatalf("MalformedReason = %q", msg.MalformedReason)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "bash" {
		t.Fatalf("calls = %+v, want one call to bash", msg.ToolCalls)
	}
	if got, want := msg.ToolCalls[0].Arguments, `{"command": "printf hi", "refresh_sec": 1}`; got != want {
		t.Errorf("Arguments = %q, want %q", got, want)
	}
	if msg.Content != "intro" {
		t.Errorf("Content = %q, want intro", msg.Content)
	}
	if msg.ToolCalls[0].Exact != "" {
		t.Errorf("Exact = %q, want none (Qwen calls re-render canonically)", msg.ToolCalls[0].Exact)
	}
	// Objects, arrays, booleans, null, and numbers are JSON; anything else,
	// including a number too long for upstream's buffer, stays a string.
	for raw, want := range map[string]string{
		"{\"a\":1}": `{"a":1}`,
		"[1, 2]":    `[1, 2]`,
		"true":      `true`,
		"null":      `null`,
		"-2.5e3":    `-2.5e3`,
		"  7 ":      `7`,
		"1 2":       `"1 2"`,
		"yes":       `"yes"`,
		"0x10":      `"0x10"`,
		"12345678901234567890123456789012345678901234567890123456789012345": `"12345678901234567890123456789012345678901234567890123456789012345"`,
	} {
		text := "<tool_call>\n<function=f>\n<parameter=v>\n" + raw + "\n</parameter>\n</function>\n</tool_call>"
		msg, err := ParseCompletionSyntax(SyntaxQwen, text, false)
		if err != nil || len(msg.ToolCalls) != 1 {
			t.Fatalf("%q: %+v %v", raw, msg, err)
		}
		if got := msg.ToolCalls[0].Arguments; got != `{"v": `+want+`}` {
			t.Errorf("value %q -> %s, want %s", raw, got, want)
		}
	}
}

func TestQwenParseTwoCallsAndErrors(t *testing.T) {
	// test_agent_qwen_tool_parser_two_calls_and_error
	text := "<tool_call>\n<function=list>\n<parameter=path>\n.\n</parameter>\n</function>\n</tool_call>\n" +
		"<tool_call>\n<function=read>\n<parameter=path>\n/tmp/x\n</parameter>\n</function>\n</tool_call>"
	msg, err := ParseCompletionSyntax(SyntaxQwen, text, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 2 || msg.ToolCalls[1].Name != "read" || msg.ToolCalls[1].Arguments != `{"path": "/tmp/x"}` {
		t.Fatalf("calls = %+v", msg.ToolCalls)
	}
	for text, reason := range map[string]string{
		"<tool_call>\n<function=list>\n<parameter=path>\n.\n</parameter>\n</tool_call>":                   "expected <parameter=...> or </function> in Qwen tool call",
		"<tool_call>\n<parameter=path>\n.\n</parameter>\n</function>\n</tool_call>":                       "expected <function=...> in Qwen tool call",
		"<tool_call>\n<function= >\n</function>\n</tool_call>":                                            "Qwen tool call without function name",
		"<tool_call>\n<function=list>\n<parameter=>\nx\n</parameter>\n</function>\n</tool_call>":          "empty <parameter=> name in Qwen tool call",
		"<tool_call>\n<function=list>\n<parameter=path>\n.\n</parameter>\n</function>\nextra</tool_call>": "expected </tool_call> after </function>",
		"<tool_call>\n<function=list>\n<parameter=path>\n.\n</function>\n</tool_call>":                    "unterminated <parameter=> value in Qwen tool call",
		"<tool_call>\n<function=list>\n</function>\n</tool_call> and then prose":                          "unexpected text after the Qwen tool call",
	} {
		msg, err := ParseCompletionSyntax(SyntaxQwen, text, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(msg.ToolCalls) != 0 || msg.MalformedReason != reason {
			t.Errorf("%q: calls=%d reason=%q, want %q", text, len(msg.ToolCalls), msg.MalformedReason, reason)
		}
	}
}

func TestQwenParseLiteralMarkupInsideValues(t *testing.T) {
	// test_agent_qwen_argument_markers_bytewise: only </parameter> ends a
	// value; tool and think markers inside it are data.
	text := "<tool_call><function=write><parameter=content>\n" +
		"literal </tool_call> </think> <think>\n" +
		"</parameter></function></tool_call>"
	msg, err := ParseCompletionSyntax(SyntaxQwen, text, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 || argValue(t, msg.ToolCalls[0].Arguments, "content") != "literal </tool_call> </think> <think>" {
		t.Fatalf("calls = %+v (%s)", msg.ToolCalls, msg.MalformedReason)
	}
	// test_agent_tool_argument_literal_markup (Qwen row): only the escaped
	// closing delimiter is decoded, one level.
	text = "<tool_call>\n<function=write>\n<parameter=content>\n<p>&amp; &lt;</p> " +
		"&lt;/parameter> &amp;lt;/parameter>\n</parameter>\n</function>\n</tool_call>"
	msg, err = ParseCompletionSyntax(SyntaxQwen, text, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 || argValue(t, msg.ToolCalls[0].Arguments, "content") != "<p>&amp; &lt;</p> </parameter> &lt;/parameter>" {
		t.Fatalf("calls = %+v (%s)", msg.ToolCalls, msg.MalformedReason)
	}
}

func TestQwenParseThinkingAndPlainContent(t *testing.T) {
	msg, err := ParseCompletionSyntax(SyntaxQwen, "<think>plan "+qwenTwoArgCall+" more", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 0 || msg.Content != "" || !strings.Contains(msg.ReasoningContent, "<tool_call>") {
		t.Errorf("unclosed thinking: %+v", msg)
	}
	msg, err = ParseCompletionSyntax(SyntaxQwen, "<think>plan</think>\n"+qwenTwoArgCall, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 || msg.ReasoningContent != "plan" || msg.Content != "" {
		t.Errorf("closed thinking: %+v", msg)
	}
	msg, err = ParseCompletionSyntax(SyntaxQwen, "just an answer<|im_end|>", false)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "just an answer<|im_end|>" && msg.Content != "just an answer" {
		t.Errorf("plain content = %q", msg.Content)
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("plain content parsed calls: %+v", msg.ToolCalls)
	}
}

func TestQwenRenderToolCallsLikeServer(t *testing.T) {
	// append_qwen_tool_calls_text: values on their own lines, non-strings as
	// compact JSON, calls separated by a newline, </parameter> escaped.
	calls := []ToolCall{
		{Name: "bash", Arguments: `{"command": "printf hi", "refresh_sec": 1, "opts": {"a": [1, 2]}}`},
		{Name: "write", Arguments: `{"content": "x </parameter> &lt;/parameter> y"}`},
	}
	got, err := RenderToolCallsSyntax(SyntaxQwen, calls)
	if err != nil {
		t.Fatal(err)
	}
	want := "<tool_call>\n<function=bash>\n<parameter=command>\nprintf hi\n</parameter>\n" +
		"<parameter=refresh_sec>\n1\n</parameter>\n<parameter=opts>\n{\"a\":[1,2]}\n</parameter>\n</function>\n</tool_call>\n" +
		"<tool_call>\n<function=write>\n<parameter=content>\nx &lt;/parameter> &amp;lt;/parameter> y\n</parameter>\n</function>\n</tool_call>"
	if got != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", got, want)
	}
	// Round trip through the strict parser.
	msg, err := ParseCompletionSyntax(SyntaxQwen, got, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 2 || msg.ToolCalls[0].Arguments != `{"command": "printf hi", "refresh_sec": 1, "opts": {"a":[1,2]}}` ||
		argValue(t, msg.ToolCalls[1].Arguments, "content") != "x </parameter> &lt;/parameter> y" {
		t.Errorf("round trip = %+v (%s)", msg.ToolCalls, msg.MalformedReason)
	}
	if _, err := RenderToolCallsSyntax(SyntaxQwen, []ToolCall{{Name: "bad>name", Arguments: "{}"}}); err == nil {
		t.Error("tool name with '>' accepted")
	}
	if _, err := RenderToolCallsSyntax(SyntaxQwen, []ToolCall{{Name: "f", Arguments: `{"k>": 1}`}}); err == nil {
		t.Error("argument name with '>' accepted")
	}
	if empty, err := RenderToolCallsSyntax(SyntaxQwen, nil); err != nil || empty != "" {
		t.Errorf("no calls = %q, %v", empty, err)
	}
}

func TestQwenRenderToolsSectionAndErrorMessage(t *testing.T) {
	tools := []Tool{{Name: "add", Description: "Add two numbers", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"}}}`)}}
	got, err := RenderToolsSectionSyntax(SyntaxQwen, tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Tools\n\nYou have access to the following functions:\n\n<tools>\n{\"type\": \"function\", \"function\": {\"name\": \"add\", \"description\": \"Add two numbers\", \"parameters\": ",
		"\n</tools>\n\n",
		"Inside string values only, escape a literal </parameter> as &lt;/parameter>. To write that escaped spelling literally, use &amp;lt;/parameter>. Other HTML entities are unchanged.\n\n",
		"If you choose to call a function ONLY reply in the following format with NO suffix:\n\n<tool_call>\n<function=example_function_name>\n<parameter=example_parameter_1>\nvalue_1\n</parameter>\n",
		"</function>\n</tool_call>\n\n<IMPORTANT>\nReminder:\n- Function calls MUST follow the specified format",
		"Tool calls are not allowed inside <think></think>; finish thinking before emitting <tool_call>.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tools section lacks %q:\n%s", want, got)
		}
	}
	if empty, err := RenderToolsSectionSyntax(SyntaxQwen, nil); err != nil || empty != "" {
		t.Errorf("empty tools = %q, %v", empty, err)
	}
	msg := ToolSyntaxErrorMessageSyntax(SyntaxQwen, "expected </tool_call> after </function>")
	if !strings.HasPrefix(msg, "Tool error: invalid Qwen tool call: expected </tool_call> after </function>\n") ||
		!strings.HasSuffix(msg, "Tool-call syntax reminder:\n<tool_call>\n<function=$TOOL_NAME>\n<parameter=$PARAMETER_NAME>\n$PARAMETER_VALUE\n</parameter>\n</function>\n</tool_call>\n") {
		t.Errorf("error message = %q", msg)
	}
	if repaired, ok := RepairCompletionSyntax(SyntaxQwen, "<tool_call>\n<function=f>\n</function>"); ok || repaired != "<tool_call>\n<function=f>\n</function>" {
		t.Error("Qwen completions must not be repaired")
	}
	if SyntaxQwen.String() != "qwen" {
		t.Errorf("String() = %q", SyntaxQwen.String())
	}
}

// A structural </think> inside a Qwen parameter body is data, as it is for
// DSML parameters and GLM arg values.
func TestQwenParameterBodyIsNotStructural(t *testing.T) {
	text := "<think>plan</think>x<tool_call><function=f><parameter=v>\n</think>\n</parameter></function></tool_call>"
	if got := lastStructuralIndex(text, "</think>"); got != len("<think>plan") {
		t.Errorf("lastStructuralIndex = %d, want %d", got, len("<think>plan"))
	}
}
