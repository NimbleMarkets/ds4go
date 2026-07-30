package dsml

import (
	"strings"
	"testing"
)

// Ported from upstream ds4's GLM streaming tests in ds4_agent.c
// (test_agent_glm_stream_*, test_agent_glm_tool_parser_chunked_*).

// feedGLM writes chunks to a GLM stream decoder and returns the collected
// events plus the final parsed message.
func feedGLM(t *testing.T, thinking bool, chunks ...string) ([]StreamEvent, ParsedMessage) {
	t.Helper()
	d := NewStreamDecoderSyntax(SyntaxGLM, thinking)
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

// eventText concatenates the deltas of one event type.
func eventText(events []StreamEvent, typ StreamEventType) string {
	var b strings.Builder
	for _, e := range events {
		if e.Type == typ {
			b.WriteString(e.Delta)
		}
	}
	return b.String()
}

func TestGLMStreamChunkedMultiArg(t *testing.T) {
	// test_agent_glm_tool_parser_chunked_multi_arg
	_, msg := feedGLM(t, false,
		"<tool_call>bash<arg_key>command</arg_key>",
		"<arg_value>printf hi</arg_value>",
		"<arg_key>refresh_sec</arg_key><arg_value>1</arg_value></tool_call>",
	)
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "bash" {
		t.Errorf("Name = %q, want %q", msg.ToolCalls[0].Name, "bash")
	}
	want := `{"command": "printf hi", "refresh_sec": "1"}`
	if msg.ToolCalls[0].Arguments != want {
		t.Errorf("Arguments = %q, want %q", msg.ToolCalls[0].Arguments, want)
	}
}

func TestGLMStreamSplitCloseTag(t *testing.T) {
	// test_agent_glm_tool_parser_streams_param_state: the </arg_value> close
	// arrives split across chunks.
	_, msg := feedGLM(t, false,
		"<tool_call>bash<arg_key>command</arg_key><arg_value>printf hi",
		"</arg",
		"_value><arg_key>refresh_sec</arg_key><arg_value>1</arg_value></tool_call>",
	)
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	want := `{"command": "printf hi", "refresh_sec": "1"}`
	if msg.ToolCalls[0].Arguments != want {
		t.Errorf("Arguments = %q, want %q", msg.ToolCalls[0].Arguments, want)
	}
}

func TestGLMStreamToolCallChunked(t *testing.T) {
	// test_agent_glm_stream_tool_call_chunked: the opening tag is split, and
	// no markup tag may leak into the user-visible content stream.
	events, msg := feedGLM(t, false,
		"intro ",
		"<to",
		"ol_call>bash<arg_key>command</arg_key><arg_value>printf hi</arg",
		"_value><arg_key>refresh_sec</arg_key><arg_value>1</arg_value></tool_call>",
	)
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "bash" {
		t.Errorf("Name = %q, want %q", msg.ToolCalls[0].Name, "bash")
	}

	content := eventText(events, EventContentDelta)
	if !strings.Contains(content, "intro ") {
		t.Errorf("content %q lost the leading prose", content)
	}
	for _, leak := range []string{glmToolCallStart, glmArgKeyStart, glmArgValueEnd} {
		if strings.Contains(content, leak) {
			t.Errorf("content %q leaked markup %q", content, leak)
		}
	}
}

func TestGLMStreamEmitsToolCallEvents(t *testing.T) {
	events, _ := feedGLM(t, false,
		"<tool_call>bash<arg_key>command</arg_key><arg_value>printf hi</arg_value></tool_call>",
	)
	var start, end *StreamEvent
	for i := range events {
		switch events[i].Type {
		case EventToolCallStart:
			start = &events[i]
		case EventToolCallEnd:
			end = &events[i]
		}
	}
	if start == nil {
		t.Fatal("no EventToolCallStart emitted")
	}
	if start.Name != "bash" {
		t.Errorf("EventToolCallStart.Name = %q, want %q", start.Name, "bash")
	}
	if end == nil {
		t.Fatal("no EventToolCallEnd emitted")
	}
	if end.Arguments != `{"command": "printf hi"}` {
		t.Errorf("EventToolCallEnd.Arguments = %q, want %q",
			end.Arguments, `{"command": "printf hi"}`)
	}
}

func TestGLMStreamIgnoresToolInsideThink(t *testing.T) {
	// test_agent_glm_stream_ignores_tool_inside_think
	d := NewStreamDecoderSyntax(SyntaxGLM, true)
	d.Write("<think>plan <tool")
	d.Write("_call>bash<arg_key>command</arg_key><arg_value>printf hi</arg_value></tool_call>")
	if !d.ToolStanzaInThinking() {
		t.Error("ToolStanzaInThinking() = false, want true for a call opened inside <think>")
	}
	d.Write("</think>done")
	_, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("got %d tool calls from inside thinking, want 0", len(msg.ToolCalls))
	}
}

// test_agent_glm_stream_greedy_sampling_boundaries: markup structure is
// sampled greedily, argument payloads are not.
func TestGLMStreamGreedySamplingBoundaries(t *testing.T) {
	d := NewStreamDecoderSyntax(SyntaxGLM, false)

	d.Write("<tool")
	if !d.WantsGreedySampling() {
		t.Error("forming <tool_call> opener: WantsGreedySampling() = false, want true")
	}

	d.Write("_call>bash<arg_key>command</arg_key><arg_value>printf hi")
	if d.WantsGreedySampling() {
		t.Error("inside an argument value: WantsGreedySampling() = true, want false")
	}

	d.Write("</arg")
	if !d.WantsGreedySampling() {
		t.Error("forming </arg_value> close: WantsGreedySampling() = false, want true")
	}

	d.Write("_value></tool_call>")
	_, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
}

// A GLM stream with no markup behaves like plain content.
func TestGLMStreamPlainContent(t *testing.T) {
	events, msg := feedGLM(t, false, "just ", "a reply")
	if got := eventText(events, EventContentDelta); got != "just a reply" {
		t.Errorf("content = %q, want %q", got, "just a reply")
	}
	if msg.Content != "just a reply" {
		t.Errorf("Content = %q, want %q", msg.Content, "just a reply")
	}
}

// Malformed GLM markup degrades to raw content rather than dropping the text.
func TestGLMStreamMalformedDegradesToContent(t *testing.T) {
	events, msg := feedGLM(t, false, "<tool_call>list<arg_key>path</arg_key>oops</tool_call>")
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("got %d tool calls, want 0", len(msg.ToolCalls))
	}
	if msg.MalformedReason == "" {
		t.Error("MalformedReason is empty, want a parse failure reason")
	}
	if got := eventText(events, EventContentDelta); !strings.Contains(got, "oops") {
		t.Errorf("content %q dropped the malformed text", got)
	}
}

// The default constructor stays on DSML.
func TestNewStreamDecoderDefaultsToDSML(t *testing.T) {
	d := NewStreamDecoder(false)
	d.Write("<tool_call>list<arg_key>path</arg_key><arg_value>.</arg_value></tool_call>")
	_, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("DSML decoder parsed %d GLM calls, want 0", len(msg.ToolCalls))
	}
}
