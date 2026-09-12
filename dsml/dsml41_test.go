package dsml

import (
	"reflect"
	"strings"
	"testing"
)

// Ported from upstream tests/ds4_agent_test.c test_v41_tool_syntax: two
// calls in V4.1's spaced tags, a string value holding "</think>", a literal
// escaped close tag, and a double-escaped one.
const v41TwoCalls = "<think>Plan.</think>\n\n<｜DSML｜ calls>\n" +
	"<｜DSML｜ invoke name=\"write\">\n" +
	"<｜DSML｜ parameter name=\"path\" string=\"true\">a.txt</｜DSML｜ parameter>\n" +
	"<｜DSML｜ parameter name=\"content\" string=\"true\">x </think> " +
	"&lt;/｜DSML｜ parameter> &amp;lt;/｜DSML｜ parameter></｜DSML｜ parameter>\n" +
	"</｜DSML｜ invoke>\n<｜DSML｜ invoke name=\"list\">\n" +
	"<｜DSML｜ parameter name=\"path\" string=\"true\">.</｜DSML｜ parameter>\n" +
	"</｜DSML｜ invoke>\n</｜DSML｜ calls>"

const v41ContentWant = "x </think> </｜DSML｜ parameter> &lt;/｜DSML｜ parameter>"

func TestDSML41ParseTwoCallsWithEscapes(t *testing.T) {
	msg, err := ParseCompletionSyntax(SyntaxDSML41, v41TwoCalls, true)
	if err != nil {
		t.Fatalf("ParseCompletionSyntax: %v", err)
	}
	if msg.ReasoningContent != "Plan." || msg.Content != "" {
		t.Errorf("reasoning=%q content=%q", msg.ReasoningContent, msg.Content)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("calls = %+v, want 2", msg.ToolCalls)
	}
	if msg.ToolCalls[0].Name != "write" || msg.ToolCalls[1].Name != "list" {
		t.Errorf("names = %q, %q", msg.ToolCalls[0].Name, msg.ToolCalls[1].Name)
	}
	var args struct {
		Path, Content string
	}
	if err := jsonUnmarshalTest(msg.ToolCalls[0].Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args.Path != "a.txt" || args.Content != v41ContentWant {
		t.Errorf("write args = %+v", args)
	}
	if !strings.HasPrefix(strings.TrimSpace(msg.ToolCalls[0].Exact), "<｜DSML｜ calls>") {
		t.Errorf("Exact = %q, want the sampled V4.1 block", msg.ToolCalls[0].Exact)
	}
}

func TestDSML41StreamSplitAtEveryByte(t *testing.T) {
	want, err := ParseCompletionSyntax(SyntaxDSML41, v41TwoCalls, true)
	if err != nil {
		t.Fatal(err)
	}
	for split := 1; split < len(v41TwoCalls); split++ {
		d := NewStreamDecoderSyntax(SyntaxDSML41, true)
		d.Write(v41TwoCalls[:split])
		d.Write(v41TwoCalls[split:])
		_, got, err := d.Close()
		if err != nil {
			t.Fatalf("split=%d Close: %v", split, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("split=%d mismatch:\n got: %+v\nwant: %+v", split, got, want)
		}
	}
}

func TestDSML41ToolCallInsideThinkingIsNotExecuted(t *testing.T) {
	text := "<think><｜DSML｜ calls><｜DSML｜ invoke name=\"list\"></｜DSML｜ invoke></｜DSML｜ calls></think>Done"
	msg, err := ParseCompletionSyntax(SyntaxDSML41, text, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 0 || msg.Content != "Done" {
		t.Errorf("msg = %+v, want no calls and content Done", msg)
	}
}

// Each syntax parses only its own tags; a V4 block is plain text to a V4.1
// parser and vice versa, as upstream's per-syntax forms tables require.
func TestDSML41AndDSMLAreStrict(t *testing.T) {
	v4 := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"list\">\n</｜DSML｜invoke>\n</｜DSML｜tool_calls>"
	v41 := "<｜DSML｜ calls>\n<｜DSML｜ invoke name=\"list\">\n</｜DSML｜ invoke>\n</｜DSML｜ calls>"
	if msg, _ := ParseCompletionSyntax(SyntaxDSML41, v4, false); len(msg.ToolCalls) != 0 {
		t.Errorf("V4.1 parser executed V4 tags: %+v", msg.ToolCalls)
	}
	if msg, _ := ParseCompletionSyntax(SyntaxDSML, v41, false); len(msg.ToolCalls) != 0 {
		t.Errorf("V4 parser executed V4.1 tags: %+v", msg.ToolCalls)
	}
	if msg, _ := ParseCompletionSyntax(SyntaxDSML41, v41, false); len(msg.ToolCalls) != 1 {
		t.Errorf("V4.1 parser missed its own tags: %+v", msg)
	}
	// The missing-bar near miss is accepted for V4.1 as it is for V4.
	missingBar := "<DSML｜ calls>\n<DSML｜ invoke name=\"list\">\n</DSML｜ invoke>\n</DSML｜ calls>"
	if msg, _ := ParseCompletionSyntax(SyntaxDSML41, missingBar, false); len(msg.ToolCalls) != 1 {
		t.Errorf("V4.1 parser rejected the missing-bar form: %+v", msg)
	}
}

func TestDSML41RenderToolCallsRoundTrip(t *testing.T) {
	calls := []ToolCall{
		{Name: "write", Arguments: `{"path":"a.txt","content":"x </｜DSML｜ parameter> &lt;/｜DSML｜ parameter>"}`},
		{Name: "add", Arguments: `{"a":1,"b":[2,3]}`},
	}
	out, err := RenderToolCallsSyntax(SyntaxDSML41, calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<｜DSML｜ calls>", "<｜DSML｜ invoke name=\"write\">", "<｜DSML｜ parameter name=\"path\" string=\"true\">a.txt</｜DSML｜ parameter>",
		"&lt;/｜DSML｜ parameter> &amp;lt;/｜DSML｜ parameter></｜DSML｜ parameter>", "string=\"false\">[2,3]</｜DSML｜ parameter>", "</｜DSML｜ calls>"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered block lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "｜DSML｜tool_calls") || strings.Contains(out, "｜DSML｜invoke") {
		t.Errorf("rendered block uses V4 tags:\n%s", out)
	}
	back, err := ParseCompletionSyntax(SyntaxDSML41, "ok"+out, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.ToolCalls) != 2 {
		t.Fatalf("round trip = %+v", back.ToolCalls)
	}
	for i := range calls {
		var want, got any
		if err := jsonUnmarshalTest(calls[i].Arguments, &want); err != nil {
			t.Fatal(err)
		}
		if err := jsonUnmarshalTest(back.ToolCalls[i].Arguments, &got); err != nil {
			t.Fatalf("call %d arguments %q: %v", i, back.ToolCalls[i].Arguments, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("call %d round trip = %v, want %v", i, got, want)
		}
	}
}

func TestDSML41ToolsSectionUsesUpstreamText(t *testing.T) {
	out, err := RenderToolsSectionSyntax(SyntaxDSML41, []Tool{{Name: "add", Description: "Add.", Parameters: []byte(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Tools\n\nYou can invoke tools using this format:", "<｜DSML｜ calls>\n<｜DSML｜ invoke name=\"$TOOL_NAME\">",
		"escape a literal </｜DSML｜ parameter> as &lt;/｜DSML｜ parameter>", "use &amp;lt;/｜DSML｜ parameter>",
		"Finish reasoning with </think> before tool calls or a final response.", "### Available Tool Schemas", `{"name": "add"`,
		"You MUST strictly follow the above defined tool name and parameter schemas"} {
		if !strings.Contains(out, want) {
			t.Errorf("tools section lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "｜DSML｜tool_calls") {
		t.Error("tools section still mentions V4's tool_calls tag")
	}
}

func TestDSML41RepairAndErrorMessage(t *testing.T) {
	truncated := "<｜DSML｜ calls>\n<｜DSML｜ invoke name=\"list\">\n</｜DSML｜ invoke>\n"
	repaired, ok := RepairCompletionSyntax(SyntaxDSML41, truncated)
	if !ok || !strings.HasSuffix(repaired, "</｜DSML｜ calls>") {
		t.Errorf("repair = (%q, %v)", repaired, ok)
	}
	if _, ok := RepairCompletionSyntax(SyntaxDSML, truncated); ok {
		t.Error("V4 repair touched a V4.1 block")
	}
	msg := ToolSyntaxErrorMessageSyntax(SyntaxDSML41, "bad")
	if !strings.Contains(msg, "<｜DSML｜ calls>") || !strings.Contains(msg, "<｜DSML｜ parameter name=\"$PARAMETER_NAME\"") {
		t.Errorf("error message lacks the V4.1 reminder:\n%s", msg)
	}
	if SyntaxDSML41.String() != "dsml41" {
		t.Errorf("String() = %q", SyntaxDSML41.String())
	}
}
