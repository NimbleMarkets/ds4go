package dsml

import (
	"strings"
	"testing"
)

// Ported from upstream ds4's Qwen streaming tests in ds4_agent.c
// (test_agent_qwen_stream_tool_call_chunked, test_agent_qwen_tool_parser_chunked_multi_arg,
// test_agent_qwen_argument_markers_bytewise).

func feedQwen(t *testing.T, thinking bool, chunks ...string) ([]StreamEvent, ParsedMessage) {
	t.Helper()
	d := NewStreamDecoderSyntax(SyntaxQwen, thinking)
	var events []StreamEvent
	for _, c := range chunks {
		events = append(events, d.Write(c)...)
	}
	tail, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return append(events, tail...), msg
}

func TestQwenStreamChunkedToolCall(t *testing.T) {
	events, msg := feedQwen(t, false,
		"intro ",
		"<to",
		"ol_call>\n<function=bash>\n<parameter=command>\nprintf hi\n</param",
		"eter>\n<parameter=refresh_sec>\n1\n</parameter>\n</function>\n</tool_call>",
	)
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Arguments != `{"command": "printf hi", "refresh_sec": 1}` {
		t.Fatalf("calls = %+v (%s)", msg.ToolCalls, msg.MalformedReason)
	}
	content := eventText(events, EventContentDelta)
	if !strings.Contains(content, "intro ") || strings.Contains(content, "<tool_call>") || strings.Contains(content, "<parameter=") {
		t.Errorf("content = %q", content)
	}
	var started, ended int
	for _, e := range events {
		switch e.Type {
		case EventToolCallStart:
			started++
			if e.Name != "bash" {
				t.Errorf("start name = %q", e.Name)
			}
		case EventToolCallEnd:
			ended++
			if e.Arguments != `{"command": "printf hi", "refresh_sec": 1}` {
				t.Errorf("end arguments = %q", e.Arguments)
			}
		}
	}
	if started != 1 || ended != 1 {
		t.Errorf("start/end events = %d/%d", started, ended)
	}
}

func TestQwenStreamBytewiseLiteralMarkers(t *testing.T) {
	raw := "<tool_call><function=write><parameter=content>\n" +
		"literal </tool_call> </think> <think>\n" +
		"</parameter></function></tool_call>"
	chunks := make([]string, len(raw))
	for i := range raw {
		chunks[i] = raw[i : i+1]
	}
	events, msg := feedQwen(t, false, chunks...)
	if len(msg.ToolCalls) != 1 || argValue(t, msg.ToolCalls[0].Arguments, "content") != "literal </tool_call> </think> <think>" {
		t.Fatalf("calls = %+v (%s)", msg.ToolCalls, msg.MalformedReason)
	}
	if c := eventText(events, EventContentDelta); c != "" {
		t.Errorf("content leaked: %q", c)
	}
}

func TestQwenStreamTwoCallsAndTrailingProse(t *testing.T) {
	events, msg := feedQwen(t, false,
		"<tool_call>\n<function=list>\n<parameter=path>\n.\n</parameter>\n</function>\n</tool_call>\n",
		"<tool_call>\n<function=read>\n<parameter=path>\n/tmp/x\n</parameter>\n</function>\n</tool_call>",
	)
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("calls = %+v (%s)", msg.ToolCalls, msg.MalformedReason)
	}
	var starts int
	for _, e := range events {
		if e.Type == EventToolCallStart {
			starts++
		}
	}
	if starts != 2 {
		t.Errorf("start events = %d", starts)
	}
	// Prose after the run degrades the stanza to content, as Close's strict
	// parse does.
	events, msg = feedQwen(t, false,
		"<tool_call>\n<function=list>\n</function>\n</tool_call> and then prose")
	if len(msg.ToolCalls) != 0 {
		t.Errorf("trailing prose: calls = %+v", msg.ToolCalls)
	}
	if c := eventText(events, EventContentDelta); !strings.Contains(c, "<tool_call>") || !strings.Contains(c, "and then prose") {
		t.Errorf("stanza not replayed as content: %q", c)
	}
}

func TestQwenStreamIgnoresToolInsideThink(t *testing.T) {
	d := NewStreamDecoderSyntax(SyntaxQwen, true)
	d.Write("<think>plan <tool")
	d.Write("_call>\n<function=bash>\n<parameter=command>\nprintf hi\n</parameter>\n</function>\n</tool_call>")
	if !d.ToolStanzaInThinking() {
		t.Error("stanza inside thinking not detected")
	}
	d.Write("</think>done")
	_, msg, err := d.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 0 || msg.Content != "done" {
		t.Errorf("msg = %+v", msg)
	}
}

func TestQwenStreamBracketedValueMustBeJSON(t *testing.T) {
	for _, tc := range qwenBracketedValueCases {
		t.Run(tc.name, func(t *testing.T) {
			text := "<tool_call>\n<function=f>\n<parameter=v>\n" + tc.raw + "\n</parameter>\n</function>\n</tool_call>"
			events, msg := feedQwen(t, false, text[:len(text)/2], text[len(text)/2:])
			if msg.MalformedReason != "" || len(msg.ToolCalls) != 1 {
				t.Fatalf("calls = %+v (%s)", msg.ToolCalls, msg.MalformedReason)
			}
			want := `{"v": ` + tc.want + `}`
			if got := msg.ToolCalls[0].Arguments; got != want {
				t.Errorf("Arguments = %s, want %s", got, want)
			}
			var ended int
			for _, e := range events {
				if e.Type == EventToolCallEnd {
					ended++
					if e.Arguments != want {
						t.Errorf("end arguments = %s, want %s", e.Arguments, want)
					}
				}
			}
			if ended != 1 {
				t.Errorf("end events = %d, want 1", ended)
			}
		})
	}
}
