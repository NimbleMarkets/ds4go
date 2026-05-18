package dsml

import (
	"encoding/json"
	"reflect"
	"testing"
)

func argsMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("arguments %q is not valid JSON: %v", s, err)
	}
	return m
}

func TestParseCompletionPlainContent(t *testing.T) {
	// No EOS marker: ds4go stops on the EOS token id before it is decoded to
	// text, so a completion normally ends without the marker.
	msg, err := ParseCompletion("Hello there.", false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.Content != "Hello there." {
		t.Errorf("Content = %q, want %q", msg.Content, "Hello there.")
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %v, want none", msg.ToolCalls)
	}
	if msg.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", msg.Role)
	}
}

func TestParseCompletionThinking(t *testing.T) {
	msg, err := ParseCompletion("reasoning here</think>final answer", true)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.ReasoningContent != "reasoning here" {
		t.Errorf("ReasoningContent = %q", msg.ReasoningContent)
	}
	if msg.Content != "final answer" {
		t.Errorf("Content = %q", msg.Content)
	}
}

func TestParseCompletionThinkingMissingEnd(t *testing.T) {
	_, err := ParseCompletion("reasoning with no end", true)
	if err == nil {
		t.Fatal("expected an error for missing </think>")
	}
}

func TestParseCompletionToolCalls(t *testing.T) {
	completion := "answer\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"add\">\n" +
		"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">2</" + dsmlMarker + "parameter>\n" +
		"<" + dsmlMarker + "parameter name=\"b\" string=\"false\">3</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>" + eosToken

	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.Content != "answer" {
		t.Errorf("Content = %q, want %q", msg.Content, "answer")
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != "add" {
		t.Errorf("Name = %q, want add", tc.Name)
	}
	want := map[string]any{"a": float64(2), "b": float64(3)}
	if got := argsMap(t, tc.Arguments); !reflect.DeepEqual(got, want) {
		t.Errorf("Arguments = %v, want %v", got, want)
	}
}

func TestParseCompletionStringParameter(t *testing.T) {
	completion := "ok\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"weather\">\n" +
		"<" + dsmlMarker + "parameter name=\"city\" string=\"true\">New York</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"

	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msg.ToolCalls))
	}
	want := map[string]any{"city": "New York"}
	if got := argsMap(t, msg.ToolCalls[0].Arguments); !reflect.DeepEqual(got, want) {
		t.Errorf("Arguments = %v, want %v", got, want)
	}
}

func TestParseCompletionMalformedInvoke(t *testing.T) {
	completion := "x\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke garbage>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"
	_, err := ParseCompletion(completion, false)
	if err == nil {
		t.Fatal("expected an error for a malformed invoke header")
	}
}

func TestParseCompletionMultipleToolCalls(t *testing.T) {
	completion := "doing two things\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"add\">\n" +
		"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">1</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"<" + dsmlMarker + "invoke name=\"greet\">\n" +
		"<" + dsmlMarker + "parameter name=\"who\" string=\"true\">world</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>" + eosToken

	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("ToolCalls len = %d, want 2", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "add" || msg.ToolCalls[1].Name != "greet" {
		t.Errorf("names = %q, %q; want add, greet", msg.ToolCalls[0].Name, msg.ToolCalls[1].Name)
	}
	if got := argsMap(t, msg.ToolCalls[1].Arguments); !reflect.DeepEqual(got, map[string]any{"who": "world"}) {
		t.Errorf("call 1 args = %v", got)
	}
}

func TestParseCompletionThinkingWithToolCalls(t *testing.T) {
	completion := "let me compute</think>here goes\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"add\">\n" +
		"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">5</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>" + eosToken

	msg, err := ParseCompletion(completion, true)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.ReasoningContent != "let me compute" {
		t.Errorf("ReasoningContent = %q", msg.ReasoningContent)
	}
	if msg.Content != "here goes" {
		t.Errorf("Content = %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "add" {
		t.Fatalf("ToolCalls = %v", msg.ToolCalls)
	}
}

func TestParseCompletionInvalidJSONArgument(t *testing.T) {
	completion := "x\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"calc\">\n" +
		"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">not json</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"
	_, err := ParseCompletion(completion, false)
	if err == nil {
		t.Fatal("expected an error for invalid JSON in a string=\"false\" parameter")
	}
}
