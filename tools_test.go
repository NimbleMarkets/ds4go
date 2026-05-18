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
	if !ok || !strings.Contains(exact, `invoke name="add"`) {
		t.Fatalf("expected exact replay block, got %q", exact)
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
