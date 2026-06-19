package dsml

import (
	"encoding/json"
	"reflect"
	"strings"
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
	msg, err := ParseCompletion("reasoning with no end", true)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.ReasoningContent != "reasoning with no end" {
		t.Fatalf("ReasoningContent = %q", msg.ReasoningContent)
	}
	if msg.Content != "" || len(msg.ToolCalls) != 0 {
		t.Fatalf("unfinished thinking should not be executable: %#v", msg)
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
	if tc.Exact == "" {
		t.Fatal("Exact DSML invoke block was not captured")
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
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.Content != completion {
		t.Fatalf("Content = %q, want raw completion %q", msg.Content, completion)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("malformed DSML should not produce tool calls: %#v", msg.ToolCalls)
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

func TestParseCompletionUnexpectedTextAfterToolCallsReturnsRawContent(t *testing.T) {
	completion := "x\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"calc\">\n" +
		"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">1</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>\nextra"
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.Content != completion {
		t.Fatalf("Content = %q, want raw completion %q", msg.Content, completion)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("malformed completion should not produce tool calls: %#v", msg.ToolCalls)
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
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.Content != completion {
		t.Fatalf("Content = %q, want raw completion %q", msg.Content, completion)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("invalid JSON DSML should not produce tool calls: %#v", msg.ToolCalls)
	}
}

func TestParseCompletionDuplicateParameterName(t *testing.T) {
	completion := "x\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"calc\">\n" +
		"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">1</" + dsmlMarker + "parameter>\n" +
		"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">2</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if got := argsMap(t, msg.ToolCalls[0].Arguments); !reflect.DeepEqual(got, map[string]any{"n": float64(2)}) {
		t.Fatalf("duplicate parameters should use the latest value, got %v", got)
	}
}

func TestParseCompletionIgnoresToolCallsInsideUnfinishedThinking(t *testing.T) {
	completion := "reasoning\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"danger\">\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"
	msg, err := ParseCompletion(completion, true)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("tool calls inside unfinished thinking were executable: %#v", msg.ToolCalls)
	}
}

func TestParseCompletionPlainXMLToolCalls(t *testing.T) {
	completion := "ok\n\n<tool_calls>\n" +
		"<invoke name=\"add\">\n" +
		"<parameter name=\"a\" string=\"false\">1</parameter>\n" +
		"</invoke>\n" +
		"</tool_calls>"
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "add" {
		t.Fatalf("ToolCalls = %#v", msg.ToolCalls)
	}
	if msg.ToolCalls[0].Exact != "\n\n<tool_calls>\n<invoke name=\"add\">\n<parameter name=\"a\" string=\"false\">1</parameter>\n</invoke>\n</tool_calls>" {
		t.Fatalf("Exact = %q", msg.ToolCalls[0].Exact)
	}
}

// TestParseCompletionNearMissMarkerInvoke reproduces a sampling typo seen
// in the wild: the model writes <｜DSMI｜invoke ...> (DSMI for DSML) while
// the surrounding tool_calls wrapper is correct. Near-miss markers are
// normalized so the call still parses.
func TestParseCompletionNearMissMarkerInvoke(t *testing.T) {
	completion := "ok\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<｜DSMI｜invoke name=\"svg_append\">\n" +
		"<｜DSMI｜parameter name=\"chunk\" string=\"true\">&lt;rect/&gt;</｜DSMI｜parameter>\n" +
		"</｜DSMI｜invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"

	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "svg_append" {
		t.Errorf("Name = %q, want svg_append", msg.ToolCalls[0].Name)
	}
}

// TestParseCompletionNearMissMarkerWrapper covers a typo'd tool_calls
// wrapper marker as well.
func TestParseCompletionNearMissMarkerWrapper(t *testing.T) {
	completion := "ok\n\n<｜DSLI｜tool_calls>\n" +
		"<｜DSLI｜invoke name=\"add\">\n" +
		"<｜DSLI｜parameter name=\"a\" string=\"false\">2</｜DSLI｜parameter>\n" +
		"</｜DSLI｜invoke>\n" +
		"</｜DSLI｜tool_calls>"

	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msg.ToolCalls))
	}
}

// TestParseCompletionASCIIPipeMarker normalizes ASCII vertical bars in
// markers (<|DSML|invoke ...>) to the canonical fullwidth form.
func TestParseCompletionASCIIPipeMarker(t *testing.T) {
	completion := "ok\n\n<|DSML|tool_calls>\n" +
		"<|DSML|invoke name=\"add\">\n" +
		"<|DSML|parameter name=\"a\" string=\"false\">2</|DSML|parameter>\n" +
		"</|DSML|invoke>\n" +
		"</|DSML|tool_calls>"

	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msg.ToolCalls))
	}
}

// TestParseCompletionNearMissDoesNotTouchContent ensures normalization
// only rewrites tag-shaped markers, not ordinary prose.
func TestParseCompletionNearMissDoesNotTouchContent(t *testing.T) {
	completion := "comparing |DSML| markers and <other> text"
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.Content != completion {
		t.Errorf("Content = %q, want untouched %q", msg.Content, completion)
	}
}

// Malformed-attempt reporting: when a tool stanza degrades to plain content,
// MalformedReason carries the parse failure so callers can ask the model to
// retry (mirroring upstream's invalid-DSML tool error suffix).

func TestParseCompletionSetsMalformedReason(t *testing.T) {
	// Balanced tags but the invoke header is missing its name attribute: not
	// repairable, degrades to content, and the reason must say why.
	text := "\n\n<｜DSML｜tool_calls>\n<｜DSML｜invoke>\n</｜DSML｜invoke>\n</｜DSML｜tool_calls>"
	msg, err := ParseCompletion(text, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("tool calls = %+v, want none", msg.ToolCalls)
	}
	if msg.MalformedReason == "" {
		t.Fatal("MalformedReason empty for a degraded tool attempt")
	}
	if !strings.Contains(msg.MalformedReason, "invoke") {
		t.Fatalf("MalformedReason = %q, want the invoke-header failure", msg.MalformedReason)
	}
}

func TestParseCompletionMalformedReasonTrailingText(t *testing.T) {
	text := "\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"</｜DSML｜invoke>\n" +
		"</｜DSML｜tool_calls>\nbut wait, I should explain more"
	msg, err := ParseCompletion(text, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("tool calls = %+v, want none", msg.ToolCalls)
	}
	if msg.MalformedReason == "" {
		t.Fatal("MalformedReason empty for trailing text after the block")
	}
}

func TestParseCompletionMalformedReasonEmptyForCleanOutput(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		thinking bool
	}{
		{"plainAnswer", "just a plain answer", false},
		{"validCall", "\n\n<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\">\n</｜DSML｜invoke>\n</｜DSML｜tool_calls>", false},
		{"stanzaInsideUnclosedThink", "pondering <｜DSML｜tool_calls> usage", true},
		{"stanzaAfterEOS", "done<｜end▁of▁sentence｜>\n\n<｜DSML｜tool_calls>garbage", false},
	}
	for _, tc := range cases {
		msg, err := ParseCompletion(tc.text, tc.thinking)
		if err != nil {
			t.Fatalf("%s: ParseCompletion: %v", tc.name, err)
		}
		if msg.MalformedReason != "" {
			t.Fatalf("%s: MalformedReason = %q, want empty", tc.name, msg.MalformedReason)
		}
	}
}

func TestParseCompletionInvokeNameBoundary(t *testing.T) {
	// "<｜DSML｜invokefoo" must NOT match the "invoke" opener.
	completion := "x\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invokefoo name=\"add\">\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("invokefoo must not parse as a tool call: %#v", msg.ToolCalls)
	}
	if msg.MalformedReason == "" {
		t.Fatalf("invokefoo must produce a MalformedReason, got none")
	}
}

func TestParseCompletionImplicitInvoke(t *testing.T) {
	// A bare invoke with no <｜DSML｜tool_calls> wrapper still produces a call.
	completion := "sure\n\n<" + dsmlMarker + "invoke name=\"add\">\n" +
		"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">2</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>" + eosToken
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1: %#v", len(msg.ToolCalls), msg)
	}
	if msg.ToolCalls[0].Name != "add" {
		t.Errorf("Name = %q, want add", msg.ToolCalls[0].Name)
	}
	if got := argsMap(t, msg.ToolCalls[0].Arguments); !reflect.DeepEqual(got, map[string]any{"a": float64(2)}) {
		t.Errorf("Arguments = %v", got)
	}
	// Implicit blocks must NOT populate Exact: the replay store rejects any
	// block without a tool_calls wrapper, and ParseAssistant propagates that
	// error. An empty Exact makes the call re-render canonically on replay.
	if msg.ToolCalls[0].Exact != "" {
		t.Errorf("Exact = %q, want empty for implicit invoke", msg.ToolCalls[0].Exact)
	}
}

func TestParseCompletionImplicitInvokeMultiple(t *testing.T) {
	completion := "<" + dsmlMarker + "invoke name=\"a\">\n" +
		"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">7</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"<" + dsmlMarker + "invoke name=\"b\">\n</" + dsmlMarker + "invoke>" + eosToken
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 2 ||
		msg.ToolCalls[0].Name != "a" || msg.ToolCalls[1].Name != "b" {
		t.Fatalf("want [a b], got %#v", msg.ToolCalls)
	}
	if got := argsMap(t, msg.ToolCalls[0].Arguments); !reflect.DeepEqual(got, map[string]any{"n": float64(7)}) {
		t.Errorf("ToolCalls[0] args = %v, want {n:7}", got)
	}
	// Implicit blocks must NOT populate Exact (Finding 1)
	for i, tc := range msg.ToolCalls {
		if tc.Exact != "" {
			t.Errorf("ToolCalls[%d].Exact = %q, want empty for implicit invoke", i, tc.Exact)
		}
	}
}

func TestParseCompletionImplicitInvokeTrailingProseIsRaw(t *testing.T) {
	// Prose after the implicit block is ambiguous; fall back to raw content.
	completion := "<" + dsmlMarker + "invoke name=\"a\">\n</" + dsmlMarker + "invoke>\nand then some prose"
	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("trailing prose must degrade to raw content: %#v", msg.ToolCalls)
	}
	if msg.MalformedReason == "" {
		t.Errorf("expected MalformedReason for trailing prose")
	}
}

func TestHasTagPrefix(t *testing.T) {
	yes := []string{"<x name=\"a\">", "<x>", "<x\t>", "<x\n"}
	for _, s := range yes {
		if !hasTagPrefix(s, "<x") {
			t.Errorf("hasTagPrefix(%q, \"<x\") = false, want true", s)
		}
	}
	no := []string{"<xy>", "<x", "<", "y<x>"}
	for _, s := range no {
		if hasTagPrefix(s, "<x") {
			t.Errorf("hasTagPrefix(%q, \"<x\") = true, want false", s)
		}
	}
}
