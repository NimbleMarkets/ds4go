package dsml

import (
	"strings"
	"testing"
)

// Ported from upstream ds4 (commits d108ae4, fc6414c, 759dd7c): tool bodies are
// not HTML. Only the body's own closing delimiter, and spellings that would
// collide with that escape, are escaped; every other entity is literal data.

func streamedArguments(t *testing.T, syntax Syntax, text string, thinking bool) (string, string) {
	t.Helper()
	d := NewStreamDecoderSyntax(syntax, thinking)
	events := d.Write(text)
	closing, _, _ := d.Close()
	events = append(events, closing...)
	var args, reasoning string
	for _, ev := range events {
		switch ev.Type {
		case EventToolCallEnd:
			args = ev.Arguments
		case EventReasoningDelta:
			reasoning += ev.Delta
		}
	}
	return args, reasoning
}

func TestToolTextEscapeRoundTrip(t *testing.T) {
	ends := []string{parameterEndToken, glmArgKeyEnd, glmArgValueEnd, toolResultEnd, "</tool_response>"}
	for _, end := range ends {
		tail := end[1:]
		input := "&amp; &lt; &gt; &quot; &apos; <html> " + end + " &lt;" + tail + " &amp;lt;" + tail + " &amp;amp;lt;" + tail + " end"
		encoded := escapeToolText(input, end)
		if strings.Contains(encoded, end) {
			t.Errorf("escapeToolText(%q) left the closing delimiter in %q", end, encoded)
		}
		if got := unescapeToolText(encoded, end); got != input {
			t.Errorf("round trip for %q:\n got %q\nwant %q", end, got, input)
		}
	}
	// The exact spellings upstream produces.
	if got := escapeToolText("a</arg_value>b", glmArgValueEnd); got != "a&lt;/arg_value>b" {
		t.Errorf("literal close = %q", got)
	}
	if got := escapeToolText("a&lt;/arg_value>b", glmArgValueEnd); got != "a&amp;lt;/arg_value>b" {
		t.Errorf("escaped spelling = %q", got)
	}
	if got := escapeToolText("a&amp;lt;/arg_value>b", glmArgValueEnd); got != "a&amp;amp;lt;/arg_value>b" {
		t.Errorf("double-escaped spelling = %q", got)
	}
	// Ordinary entities and unrelated escapes are untouched by both directions.
	const plain = "&amp; &lt;div&gt; &lt;/other> &quot;"
	if got := escapeToolText(plain, glmArgValueEnd); got != plain {
		t.Errorf("escapeToolText(plain) = %q", got)
	}
	if got := unescapeToolText(plain, glmArgValueEnd); got != plain {
		t.Errorf("unescapeToolText(plain) = %q", got)
	}
}

// Mirrors upstream test_agent_tool_argument_literal_markup: entities other
// than the closing-delimiter escape are literal, and the one escape reverses
// exactly one level.
func TestParseCompletionKeepsLiteralEntitiesInArguments(t *testing.T) {
	dsmlBody := "<p>&amp; &lt;</p> </tool_call> </think> &lt;/｜DSML｜parameter> &amp;lt;/｜DSML｜parameter>"
	dsmlWant := "<p>&amp; &lt;</p> </tool_call> </think> </｜DSML｜parameter> &lt;/｜DSML｜parameter>"
	glmBody := "<p>&amp; &lt;</p> </tool_call> </think> &lt;/arg_value> &amp;lt;/arg_value>"
	glmWant := "<p>&amp; &lt;</p> </tool_call> </think> </arg_value> &lt;/arg_value>"

	dsmlText := "<｜DSML｜tool_calls><｜DSML｜invoke name=\"write\"><｜DSML｜parameter name=\"content\" string=\"true\">" +
		dsmlBody + "</｜DSML｜parameter></｜DSML｜invoke></｜DSML｜tool_calls>"
	glmText := "<tool_call>write<arg_key>content</arg_key><arg_value>" + glmBody + "</arg_value></tool_call>"

	cases := []struct {
		name   string
		syntax Syntax
		text   string
		want   string
	}{
		{"dsml", SyntaxDSML, dsmlText, dsmlWant},
		{"glm", SyntaxGLM, glmText, glmWant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := ParseCompletionSyntax(tc.syntax, tc.text, false)
			if err != nil {
				t.Fatalf("ParseCompletionSyntax: %v", err)
			}
			if len(msg.ToolCalls) != 1 {
				t.Fatalf("got %d tool calls, want 1 (%+v)", len(msg.ToolCalls), msg)
			}
			if got := argValue(t, msg.ToolCalls[0].Arguments, "content"); got != tc.want {
				t.Errorf("parsed content =\n %q\nwant\n %q", got, tc.want)
			}
			args, _ := streamedArguments(t, tc.syntax, tc.text, false)
			if args == "" {
				t.Fatal("stream decoder emitted no EventToolCallEnd")
			}
			if got := argValue(t, args, "content"); got != tc.want {
				t.Errorf("streamed content =\n %q\nwant\n %q", got, tc.want)
			}
		})
	}
}

// GLM <arg_key> bodies use the same narrow escape as values.
func TestParseGLMUnescapesArgKey(t *testing.T) {
	text := "<tool_call>write<arg_key>odd&lt;/arg_key>key</arg_key><arg_value>v</arg_value></tool_call>"
	msg, err := ParseCompletionSyntax(SyntaxGLM, text, false)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	const wantKey = "odd</arg_key>key"
	if got := argValue(t, msg.ToolCalls[0].Arguments, wantKey); got != "v" {
		t.Errorf("parsed args = %s, want key %q", msg.ToolCalls[0].Arguments, wantKey)
	}
	args, _ := streamedArguments(t, SyntaxGLM, text, false)
	if got := argValue(t, args, wantKey); got != "v" {
		t.Errorf("streamed args = %s, want key %q", args, wantKey)
	}
}

// Mirrors upstream test_tool_body_escape_round_trip's render half: rendering
// a call and parsing it back preserves entities in the argument.
func TestRenderToolCallsRoundTripsEntities(t *testing.T) {
	const content = "<p>&amp; &lt; &quot; &apos;</p> </｜DSML｜parameter> &lt;/arg_value>"
	calls := []ToolCall{{Name: "write", Arguments: `{"content":"` + strings.ReplaceAll(content, `"`, `\"`) + `"}`}}
	for _, syntax := range []Syntax{SyntaxDSML, SyntaxGLM} {
		out, err := RenderToolCallsSyntax(syntax, calls)
		if err != nil {
			t.Fatalf("RenderToolCallsSyntax(%v): %v", syntax, err)
		}
		msg, err := ParseCompletionSyntax(syntax, out, false)
		if err != nil {
			t.Fatalf("ParseCompletionSyntax(%v): %v\n%s", syntax, err, out)
		}
		if len(msg.ToolCalls) != 1 {
			t.Fatalf("syntax %v: got %d calls, want 1\n%s", syntax, len(msg.ToolCalls), out)
		}
		if got := argValue(t, msg.ToolCalls[0].Arguments, "content"); got != content {
			t.Errorf("syntax %v: round trip =\n %q\nwant\n %q", syntax, got, content)
		}
	}
}

func TestRenderToolResultEscapesEscapedSpelling(t *testing.T) {
	out, err := RenderToolResult("a &lt;/tool_result> b </tool_result> c &amp; d")
	if err != nil {
		t.Fatalf("RenderToolResult: %v", err)
	}
	want := toolResultStart + "a &amp;lt;/tool_result> b &lt;/tool_result> c &amp; d" + toolResultEnd
	if out != want {
		t.Fatalf("RenderToolResult =\n %q\nwant\n %q", out, want)
	}
}

// Mirrors upstream test_tool_control_text_inside_arguments: structural
// markers quoted inside an argument body are data, not the end of thinking or
// of the call.
func TestParseCompletionControlTextInsideArguments(t *testing.T) {
	const body = "literal </tool_call> </｜DSML｜tool_calls> </think> <think>"
	dsmlText := "<think>reason</think><｜DSML｜tool_calls><｜DSML｜invoke name=\"write\"><｜DSML｜parameter name=\"content\" string=\"true\">" +
		body + "</｜DSML｜parameter></｜DSML｜invoke></｜DSML｜tool_calls>"
	glmText := "<think>reason</think><tool_call>write<arg_key>content</arg_key><arg_value>" + body + "</arg_value></tool_call>"
	for _, tc := range []struct {
		name   string
		syntax Syntax
		text   string
	}{{"dsml", SyntaxDSML, dsmlText}, {"glm", SyntaxGLM, glmText}} {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := ParseCompletionSyntax(tc.syntax, tc.text, true)
			if err != nil {
				t.Fatalf("ParseCompletionSyntax: %v", err)
			}
			if msg.ReasoningContent != "reason" {
				t.Errorf("reasoning = %q, want %q", msg.ReasoningContent, "reason")
			}
			if len(msg.ToolCalls) != 1 {
				t.Fatalf("got %d tool calls, want 1 (%+v)", len(msg.ToolCalls), msg)
			}
			if got := argValue(t, msg.ToolCalls[0].Arguments, "content"); got != body {
				t.Errorf("content = %q, want %q", got, body)
			}
			if msg.MalformedReason != "" {
				t.Errorf("MalformedReason = %q", msg.MalformedReason)
			}
		})
	}
	// A truncated block whose argument quotes </think> must still scan from
	// the real end of thinking, so the wrapper repair is attempted on the
	// whole stanza rather than on the tail after the quoted marker. (Like
	// upstream, the tag counts themselves do not skip bodies, so the quoted
	// wrapper closer is left out of this case.)
	truncated := "<think>reason</think><｜DSML｜tool_calls><｜DSML｜invoke name=\"write\"><｜DSML｜parameter name=\"content\" string=\"true\">" +
		"literal </think> <think>" + "</｜DSML｜parameter></｜DSML｜invoke>"
	repaired, ok := RepairCompletion(truncated)
	if !ok || repaired != truncated+"</｜DSML｜tool_calls>" {
		t.Errorf("repair with quoted </think>: ok=%v repaired=%q", ok, repaired)
	}
}

func TestLastStructuralIndexSkipsArgumentBodies(t *testing.T) {
	text := "a</think>b<｜DSML｜parameter name=\"x\" string=\"true\">c</think>d</｜DSML｜parameter>e" +
		"<arg_key>k</think></arg_key><arg_value>v</think></arg_value>f"
	if got := lastStructuralIndex(text, thinkingEndToken); got != 1 {
		t.Errorf("lastStructuralIndex = %d, want 1", got)
	}
	// An unterminated wrapper ends the scan with what was found so far.
	open := "a</think>b<arg_value>c</think>"
	if got := lastStructuralIndex(open, thinkingEndToken); got != 1 {
		t.Errorf("lastStructuralIndex(open) = %d, want 1", got)
	}
	if got := lastStructuralIndex("no marker", thinkingEndToken); got != -1 {
		t.Errorf("lastStructuralIndex(none) = %d, want -1", got)
	}
}
