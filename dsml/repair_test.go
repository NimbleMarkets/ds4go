package dsml

import (
	"strings"
	"testing"
)

// Truncated completions below mirror generation that hit the token limit
// mid-stanza. RepairCompletion must append the missing closing tags so the
// block parses, mirroring upstream ds4's try_repair_dsml.

// Upstream 759dd7c: a truncated parameter or invoke is not repaired. A closed
// value may still be a truncated shell command, and closing it would invent an
// executable action. Only the enclosing wrapper is ever supplied.
func TestRepairCompletionRefusesUnclosedParameter(t *testing.T) {
	text := "I'll list the files.\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"<｜DSML｜parameter name=\"command\" string=\"true\">ls -la"
	repaired, ok := RepairCompletion(text)
	if ok || repaired != text {
		t.Fatalf("truncated parameter repaired: ok=%v repaired=%q", ok, repaired)
	}
}

func TestRepairCompletionRefusesUnclosedInvoke(t *testing.T) {
	text := "\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"<｜DSML｜parameter name=\"command\" string=\"true\">pwd</｜DSML｜parameter>\n"
	repaired, ok := RepairCompletion(text)
	if ok || repaired != text {
		t.Fatalf("truncated invoke repaired: ok=%v repaired=%q", ok, repaired)
	}
}

func TestRepairCompletionUnclosedToolCallsBlock(t *testing.T) {
	text := "\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"<｜DSML｜parameter name=\"command\" string=\"true\">pwd</｜DSML｜parameter>\n" +
		"</｜DSML｜invoke>\n"
	repaired, ok := RepairCompletion(text)
	if !ok {
		t.Fatal("RepairCompletion did not repair a truncated tool_calls block")
	}
	if want := text + "</｜DSML｜tool_calls>"; repaired != want {
		t.Fatalf("repaired = %q, want %q", repaired, want)
	}
}

func TestRepairCompletionBalancedBlockUnchanged(t *testing.T) {
	text := "\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"<｜DSML｜parameter name=\"command\" string=\"true\">pwd</｜DSML｜parameter>\n" +
		"</｜DSML｜invoke>\n" +
		"</｜DSML｜tool_calls>"
	repaired, ok := RepairCompletion(text)
	if ok || repaired != text {
		t.Fatalf("balanced block repaired: ok=%v repaired=%q", ok, repaired)
	}
}

func TestRepairCompletionRefusesExtraClosers(t *testing.T) {
	text := "<｜DSML｜tool_calls>\n</｜DSML｜invoke>\n</｜DSML｜tool_calls></｜DSML｜tool_calls>"
	repaired, ok := RepairCompletion(text)
	if ok || repaired != text {
		t.Fatalf("extra closers repaired: ok=%v repaired=%q", ok, repaired)
	}
}

func TestRepairCompletionNoToolBlock(t *testing.T) {
	repaired, ok := RepairCompletion("Just a plain answer with a stray < bracket.")
	if ok || repaired != "Just a plain answer with a stray < bracket." {
		t.Fatalf("plain text repaired: ok=%v repaired=%q", ok, repaired)
	}
}

func TestRepairCompletionIgnoresTagsInsideReasoning(t *testing.T) {
	// A stanza opening quoted inside <think> is not executable and must not
	// inflate the tag counts: the balanced block after </think> needs no repair.
	text := "<think>I could emit <｜DSML｜tool_calls> here but should not.</think>\n" +
		"\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"</｜DSML｜invoke>\n" +
		"</｜DSML｜tool_calls>"
	repaired, ok := RepairCompletion(text)
	if ok || repaired != text {
		t.Fatalf("balanced post-think block repaired: ok=%v repaired=%q", ok, repaired)
	}

	// And the converse: a truncated block after </think> is repaired using
	// only the post-think counts.
	truncated := "<think>quoting <｜DSML｜tool_calls> twice <｜DSML｜tool_calls></think>" +
		"\n\n<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\">\n</｜DSML｜invoke>\n"
	repaired, ok = RepairCompletion(truncated)
	if !ok {
		t.Fatal("truncated post-think block not repaired")
	}
	if want := truncated + "</｜DSML｜tool_calls>"; repaired != want {
		t.Fatalf("repaired = %q, want %q", repaired, want)
	}
}

func TestRepairCompletionShortDialect(t *testing.T) {
	// Short-dialect markers are canonicalized to fullwidth by marker
	// normalization (matching ParseCompletion), so the repair is applied in
	// canonical form and must parse.
	text := "\n\n<DSML｜tool_calls>\n<DSML｜invoke name=\"bash\">\n" +
		"<DSML｜parameter name=\"command\" string=\"true\">pwd</DSML｜parameter>\n</DSML｜invoke>\n"
	repaired, ok := RepairCompletion(text)
	if !ok {
		t.Fatal("short dialect not repaired")
	}
	if !strings.HasSuffix(repaired, "</｜DSML｜invoke>\n</｜DSML｜tool_calls>") {
		t.Fatalf("repaired = %q, want canonical closers appended", repaired)
	}
	msg, err := ParseCompletion(repaired, false)
	if err != nil || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "bash" {
		t.Fatalf("repaired parse = %+v, err %v", msg.ToolCalls, err)
	}
}

func TestRepairCompletionPlainDialect(t *testing.T) {
	text := "\n\n<tool_calls>\n<invoke name=\"bash\">\n" +
		"<parameter name=\"command\" string=\"true\">pwd</parameter>\n</invoke>\n"
	repaired, ok := RepairCompletion(text)
	if !ok {
		t.Fatal("plain dialect not repaired")
	}
	if want := text + "</tool_calls>"; repaired != want {
		t.Fatalf("repaired = %q, want %q", repaired, want)
	}
}

func TestRepairCompletionComposesWithMarkerNormalization(t *testing.T) {
	// A sampled marker typo plus truncation: normalization rewrites the typo,
	// then repair closes the block, and the result parses.
	text := "\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSMI｜invoke name=\"bash\">\n" +
		"<｜DSML｜parameter name=\"command\" string=\"true\">pwd</｜DSML｜parameter>\n" +
		"</｜DSMI｜invoke>\n"
	repaired, ok := RepairCompletion(text)
	if !ok {
		t.Fatal("typo'd truncated block not repaired")
	}
	if strings.Contains(repaired, "DSMI") {
		t.Fatalf("repaired text kept marker typo: %q", repaired)
	}
	msg, err := ParseCompletion(repaired, false)
	if err != nil || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "bash" {
		t.Fatalf("repaired parse = %+v, err %v", msg.ToolCalls, err)
	}
}

func TestRepairCompletionWithThinkingReparse(t *testing.T) {
	// Closed thinking followed by a truncated stanza: after repair the parse
	// must produce both the reasoning and the recovered call.
	text := "<think>need to check the directory</think>" +
		"\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"<｜DSML｜parameter name=\"command\" string=\"true\">ls</｜DSML｜parameter>\n" +
		"</｜DSML｜invoke>\n"
	repaired, ok := RepairCompletion(text)
	if !ok {
		t.Fatal("truncated stanza after closed thinking not repaired")
	}
	msg, err := ParseCompletion(repaired, true)
	if err != nil {
		t.Fatalf("ParseCompletion(repaired): %v", err)
	}
	if msg.ReasoningContent != "need to check the directory" {
		t.Fatalf("reasoning = %q", msg.ReasoningContent)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "bash" {
		t.Fatalf("tool calls = %+v", msg.ToolCalls)
	}
}
