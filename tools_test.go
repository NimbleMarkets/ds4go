package ds4

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestToolRegistryRegisterAndSchemas(t *testing.T) {
	reg := NewToolRegistry()
	err := reg.RegisterFunc(ToolSchema{
		Name:        "weather",
		Description: "Look up weather",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "sunny", nil
	})
	if err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}
	schemas := reg.Schemas()
	if len(schemas) != 1 || schemas[0].Name != "weather" {
		t.Fatalf("Schemas() = %#v", schemas)
	}
}

func TestToolRegistryBuildPrompt(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	reg := NewToolRegistry()
	if err := reg.RegisterFunc(ToolSchema{
		Name:        "add",
		Description: "Add two numbers",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"}}}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "3", nil
	}); err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}

	history := []ChatMessage{
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "calling tool",
			ToolCalls: []ToolCall{{
				ID:        "call_1",
				Name:      "add",
				Arguments: `{"a":2,"b":1}`,
			}},
		},
		{Role: "tool", Content: "3", ToolCallID: "call_1"},
	}
	prompt, err := reg.BuildPrompt(eng, "system prompt", history, ThinkHigh)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	defer prompt.Free()
	if prompt.Len() == 0 {
		t.Fatal("expected non-empty prompt")
	}
}

func TestBuildChatPromptNoTools(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	history := []ChatMessage{{Role: "user", Content: "hello there"}}
	prompt, err := BuildChatPrompt(eng, "system prompt", nil, history, ThinkHigh)
	if err != nil {
		t.Fatalf("BuildChatPrompt: %v", err)
	}
	defer prompt.Free()

	if got, want := prompt.Len(), expectedPromptLen(t, "system prompt", nil, history, renderChatMessage); got != want {
		t.Fatalf("prompt Len = %d, want %d", got, want)
	}
}

func TestBuildChatPromptToolsWithSystem(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	tools := []dsml.Tool{{
		Name:        "weather",
		Description: "Look up weather",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}}
	history := []ChatMessage{{Role: "user", Content: "forecast"}}
	prompt, err := BuildChatPrompt(eng, "client system", tools, history, ThinkHigh)
	if err != nil {
		t.Fatalf("BuildChatPrompt: %v", err)
	}
	defer prompt.Free()

	if got, want := prompt.Len(), expectedPromptLen(t, "client system", tools, history, renderChatMessage); got != want {
		t.Fatalf("prompt Len = %d, want %d", got, want)
	}
}

func TestBuildChatPromptToolsNoSystem(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	tools := []dsml.Tool{{
		Name:        "lookup",
		Description: "Lookup data",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}}
	history := []ChatMessage{{Role: "user", Content: "search"}}
	prompt, err := BuildChatPrompt(eng, "", tools, history, ThinkHigh)
	if err != nil {
		t.Fatalf("BuildChatPrompt: %v", err)
	}
	defer prompt.Free()

	if got, want := prompt.Len(), expectedPromptLen(t, "", tools, history, renderChatMessage); got != want {
		t.Fatalf("prompt Len = %d, want %d", got, want)
	}
}

func TestBuildChatPromptThinkMaxAddsMaxEffortPrefix(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	history := []ChatMessage{{Role: "user", Content: "hello"}}
	high, err := BuildChatPrompt(eng, "system", nil, history, ThinkHigh)
	if err != nil {
		t.Fatalf("BuildChatPrompt high: %v", err)
	}
	defer high.Free()
	max, err := BuildChatPrompt(eng, "system", nil, history, ThinkMax)
	if err != nil {
		t.Fatalf("BuildChatPrompt max: %v", err)
	}
	defer max.Free()

	if got, want := max.Len(), high.Len()+1; got != want {
		t.Fatalf("ThinkMax prompt Len = %d, want %d", got, want)
	}
}

func TestBuildChatPromptMultiTurnToolHistory(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	history := []ChatMessage{
		{Role: "user", Content: "add numbers"},
		{
			Role:    "assistant",
			Content: "calling",
			ToolCalls: []ToolCall{{
				ID:        "call_1",
				Name:      "add",
				Arguments: `{"a":2,"b":3}`,
			}},
		},
		{Role: "tool", Content: "5", ToolCallID: "call_1"},
		{Role: "assistant", Content: "answer is 5"},
	}
	prompt, err := BuildChatPrompt(eng, "system", nil, history, ThinkHigh)
	if err != nil {
		t.Fatalf("BuildChatPrompt: %v", err)
	}
	defer prompt.Free()

	rendered, err := renderPromptMessages(history, renderChatMessage, promptRenderOptions{thinking: true, toolContext: true})
	if err != nil {
		t.Fatalf("renderPromptMessages: %v", err)
	}
	if len(rendered) != 4 {
		t.Fatalf("rendered len = %d, want 4", len(rendered))
	}
	if !strings.Contains(rendered[1].content, `<｜DSML｜tool_calls>`) {
		t.Fatalf("assistant tool calls were not rendered: %q", rendered[1].content)
	}
	if rendered[2].role != "user" || rendered[2].content != "<tool_result>5</tool_result>" {
		t.Fatalf("tool result rendered as %#v", rendered[2])
	}
	if got, want := prompt.Len(), expectedPromptLen(t, "system", nil, history, renderChatMessage); got != want {
		t.Fatalf("prompt Len = %d, want %d", got, want)
	}
}

func TestToolAwareSystemContentPlacesToolsFirst(t *testing.T) {
	got := toolAwareSystemContent("client system", "## Tools\nschemas")
	want := "## Tools\nschemas\n\nclient system"
	if got != want {
		t.Fatalf("system content = %q, want %q", got, want)
	}
}

func TestToolRegistryRenderPromptMessagesCoalescesToolResults(t *testing.T) {
	reg := NewToolRegistry()
	got, err := renderPromptMessages([]ChatMessage{
		{Role: "user", Content: "question"},
		{Role: "tool", Content: "A", ToolCallID: "call_1"},
		{Role: "tool", Content: "B", ToolCallID: "call_2"},
		{Role: "assistant", Content: "done"},
	}, reg.renderMessage, promptRenderOptions{})
	if err != nil {
		t.Fatalf("renderPromptMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("rendered messages len = %d, want 3: %#v", len(got), got)
	}
	if got[1].role != "user" {
		t.Fatalf("coalesced tool role = %q, want user", got[1].role)
	}
	want := "<tool_result>A</tool_result><tool_result>B</tool_result>"
	if got[1].content != want {
		t.Fatalf("coalesced tool content = %q, want %q", got[1].content, want)
	}
}

func TestToolRegistryParseAssistantStoresReplay(t *testing.T) {
	reg := NewToolRegistry()
	rendered, err := dsml.RenderToolCalls([]dsml.ToolCall{{
		Name:      "add",
		Arguments: `{"a":2,"b":3}`,
	}})
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}
	msg, err := reg.ParseAssistant("working"+rendered, false)
	if err != nil {
		t.Fatalf("ParseAssistant: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID == "" {
		t.Fatal("expected assigned tool-call ID")
	}
	exact, ok := reg.ReplayStore().Lookup(msg.ToolCalls[0].ID)
	if !ok || !strings.Contains(exact, `<｜DSML｜tool_calls>`) || !strings.Contains(exact, `invoke name="add"`) {
		t.Fatalf("expected exact replay block, got %q", exact)
	}
}

func TestToolRegistryParseAssistantRepairsTruncatedBlock(t *testing.T) {
	reg := NewToolRegistry()
	// Generation that hit the token limit mid-parameter: without repair the
	// strict parse degrades this to plain content and the call is dropped.
	truncated := "checking\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"<｜DSML｜parameter name=\"command\" string=\"true\">ls -la"
	msg, err := reg.ParseAssistant(truncated, false)
	if err != nil {
		t.Fatalf("ParseAssistant: %v", err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "bash" {
		t.Fatalf("truncated call not recovered: %+v", msg.ToolCalls)
	}
	if msg.Content != "checking" {
		t.Fatalf("content = %q, want %q", msg.Content, "checking")
	}
	exact, ok := reg.ReplayStore().Lookup(msg.ToolCalls[0].ID)
	if !ok || !strings.HasSuffix(exact, "</｜DSML｜tool_calls>") {
		t.Fatalf("expected repaired replay block, got %q", exact)
	}
}

func TestToolRegistryParseAssistantKeepsRawWhenRepairCannotHelp(t *testing.T) {
	reg := NewToolRegistry()
	// Balanced tags but a malformed invoke header: repair has nothing to
	// append, so the raw-content fallback must be preserved.
	malformed := "\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke>\n" +
		"</｜DSML｜invoke>\n" +
		"</｜DSML｜tool_calls>"
	msg, err := reg.ParseAssistant(malformed, false)
	if err != nil {
		t.Fatalf("ParseAssistant: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("malformed header produced tool calls: %+v", msg.ToolCalls)
	}
}

func TestToolRegistryReplaysWholeToolCallsBlock(t *testing.T) {
	reg := NewToolRegistry()
	rendered, err := dsml.RenderToolCalls([]dsml.ToolCall{
		{Name: "add", Arguments: `{"a":2}`},
		{Name: "mul", Arguments: `{"x":3}`},
	})
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}
	msg, err := reg.ParseAssistant("working"+rendered, false)
	if err != nil {
		t.Fatalf("ParseAssistant: %v", err)
	}
	got, err := reg.renderAssistantToolCalls(dsml.SyntaxDSML, msg.ToolCalls)
	if err != nil {
		t.Fatalf("renderAssistantToolCalls: %v", err)
	}
	if got != rendered {
		t.Fatalf("replayed block = %q, want exact sampled block %q", got, rendered)
	}
}

func TestToolRegistryExecuteToolCalls(t *testing.T) {
	reg := NewToolRegistry()
	if err := reg.RegisterFunc(ToolSchema{
		Name:        "echo",
		Description: "Echo JSON args",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		return string(args), nil
	}); err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}
	results, err := reg.ExecuteToolCalls(context.Background(), []ToolCall{{
		ID:        "call_1",
		Name:      "echo",
		Arguments: `{"msg":"hi"}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolCalls: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one tool result, got %d", len(results))
	}
	if results[0].Role != "tool" || results[0].ToolCallID != "call_1" || results[0].Content != `{"msg":"hi"}` {
		t.Fatalf("unexpected tool result: %#v", results[0])
	}
}

func TestToolRegistryCoercesGLMArgumentsFromSchema(t *testing.T) {
	reg := NewToolRegistry()
	type args struct {
		Count   int            `json:"count"`
		Ratio   float64        `json:"ratio"`
		Enabled bool           `json:"enabled"`
		Label   string         `json:"label"`
		Items   []int          `json:"items"`
		Meta    map[string]int `json:"meta"`
	}
	var got args
	if err := reg.RegisterFunc(ToolSchema{
		Name: "typed",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"count":{"type":"integer"},"ratio":{"type":"number"},` +
			`"enabled":{"type":"boolean"},"label":{"type":"string"},` +
			`"items":{"type":"array"},"meta":{"type":"object"}}}`),
	}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		return "ok", json.Unmarshal(raw, &got)
	}); err != nil {
		t.Fatal(err)
	}

	text := "<tool_call>typed" +
		"<arg_key>count</arg_key><arg_value>3</arg_value>" +
		"<arg_key>ratio</arg_key><arg_value>1.5</arg_value>" +
		"<arg_key>enabled</arg_key><arg_value>true</arg_value>" +
		"<arg_key>label</arg_key><arg_value>007</arg_value>" +
		"<arg_key>items</arg_key><arg_value>[1,2]</arg_value>" +
		"<arg_key>meta</arg_key><arg_value>{\"x\":4}</arg_value>" +
		"</tool_call>"
	msg, err := reg.ParseAssistantSyntax(dsml.SyntaxGLM, text, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(msg.ToolCalls))
	}
	if _, err := reg.ExecuteToolCalls(context.Background(), msg.ToolCalls); err != nil {
		t.Fatal(err)
	}
	if got.Count != 3 || got.Ratio != 1.5 || !got.Enabled || got.Label != "007" ||
		len(got.Items) != 2 || got.Items[1] != 2 || got.Meta["x"] != 4 {
		t.Fatalf("decoded args = %+v", got)
	}
}

func TestToolRegistryRenderToolsSectionEmpty(t *testing.T) {
	out, err := NewToolRegistry().RenderToolsSection()
	if err != nil {
		t.Fatalf("RenderToolsSection: %v", err)
	}
	if out != "" {
		t.Errorf("empty registry RenderToolsSection = %q, want empty", out)
	}
}

func TestToolRegistryExecuteToolCallsContextCancelled(t *testing.T) {
	reg := NewToolRegistry()
	invoked := false
	if err := reg.RegisterFunc(ToolSchema{Name: "noop"}, func(ctx context.Context, args json.RawMessage) (string, error) {
		invoked = true
		return "ok", nil
	}); err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := reg.ExecuteToolCalls(ctx, []ToolCall{{Name: "noop"}})
	if err != context.Canceled {
		t.Fatalf("ExecuteToolCalls error = %v, want context.Canceled", err)
	}
	if invoked {
		t.Error("handler was invoked despite a cancelled context")
	}
}

func expectedPromptLen(t *testing.T, system string, tools []dsml.Tool, history []ChatMessage, render chatMessageRenderer) int {
	t.Helper()
	toolsSection, err := dsml.RenderToolsSection(tools)
	if err != nil {
		t.Fatalf("RenderToolsSection: %v", err)
	}
	total := 0
	if system != "" || toolsSection != "" {
		total += len(strings.Fields("system: " + toolAwareSystemContent(system, toolsSection)))
	}
	// Mirrors buildChatPrompt's options for the ThinkHigh prompts the tests build.
	rendered, err := renderPromptMessages(history, render, promptRenderOptions{
		thinking:    true,
		toolContext: len(tools) > 0 || historyUsesToolContext(history),
	})
	if err != nil {
		t.Fatalf("renderPromptMessages: %v", err)
	}
	for _, msg := range rendered {
		if msg.prerendered {
			// The mock rendered-chat tokenizer emits one token per field
			// with no role prefix.
			total += len(strings.Fields(msg.content))
			continue
		}
		total += len(strings.Fields(msg.role + ": " + msg.content))
	}
	return total
}

// Reasoning replay: mirroring upstream's render_chat_prompt_text, assistant
// history turns re-render their <think> block when thinking is enabled and
// the turn is in tool context (or follows the last user-like turn). Dropping
// reasoning from replayed tool-call turns degrades reasoning models and
// breaks KV prefix reuse against the live session.

func TestRenderPromptMessagesReplaysReasoningInToolContext(t *testing.T) {
	reg := NewToolRegistry()
	history := []ChatMessage{
		{Role: "user", Content: "question"},
		{
			Role:             "assistant",
			Content:          "checking",
			ReasoningContent: "let me check",
			ToolCalls:        []ToolCall{{ID: "c1", Name: "add", Arguments: `{"a":1}`}},
		},
		{Role: "tool", Content: "5", ToolCallID: "c1"},
	}
	out, err := renderPromptMessages(history, reg.renderMessage, promptRenderOptions{thinking: true, toolContext: true})
	if err != nil {
		t.Fatalf("renderPromptMessages: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("rendered %d messages, want 3", len(out))
	}
	if out[0].prerendered || out[0].role != "user" {
		t.Fatalf("user turn = %#v", out[0])
	}
	turn := out[1]
	if !turn.prerendered {
		t.Fatalf("assistant turn not prerendered: %#v", turn)
	}
	if !strings.Contains(turn.content, "<think>let me check</think>") {
		t.Fatalf("assistant turn dropped reasoning: %q", turn.content)
	}
	if !strings.Contains(turn.content, `invoke name="add"`) {
		t.Fatalf("assistant turn missing tool calls: %q", turn.content)
	}
	if !strings.HasPrefix(turn.content, "<｜Assistant｜>") || !strings.HasSuffix(turn.content, "<｜end▁of▁sentence｜>") {
		t.Fatalf("assistant turn missing template markers: %q", turn.content)
	}
}

func TestRenderPromptMessagesPlainChatReplaysOnlyAfterLastUser(t *testing.T) {
	history := []ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "one", ReasoningContent: "old reasoning"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "two", ReasoningContent: "fresh reasoning"},
	}
	out, err := renderPromptMessages(history, renderChatMessage, promptRenderOptions{thinking: true})
	if err != nil {
		t.Fatalf("renderPromptMessages: %v", err)
	}
	if strings.Contains(out[1].content, "old reasoning") {
		t.Fatalf("prior-turn reasoning replayed in plain chat: %q", out[1].content)
	}
	if !strings.Contains(out[1].content, "</think>") {
		t.Fatalf("prior turn missing closed think slot: %q", out[1].content)
	}
	if !strings.Contains(out[3].content, "<think>fresh reasoning</think>") {
		t.Fatalf("turn after last user dropped reasoning: %q", out[3].content)
	}
}

func TestRenderPromptMessagesNoThinkingNeverReplays(t *testing.T) {
	history := []ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "a", ReasoningContent: "hidden"},
	}
	out, err := renderPromptMessages(history, renderChatMessage, promptRenderOptions{toolContext: true})
	if err != nil {
		t.Fatalf("renderPromptMessages: %v", err)
	}
	if strings.Contains(out[1].content, "hidden") {
		t.Fatalf("reasoning replayed without thinking: %q", out[1].content)
	}
}

func TestHistoryUsesToolContext(t *testing.T) {
	if historyUsesToolContext([]ChatMessage{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}}) {
		t.Fatal("plain chat reported as tool context")
	}
	if !historyUsesToolContext([]ChatMessage{{Role: "tool", Content: "5"}}) {
		t.Fatal("tool message not reported as tool context")
	}
	if !historyUsesToolContext([]ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{{Name: "add"}}}}) {
		t.Fatal("assistant tool calls not reported as tool context")
	}
}
