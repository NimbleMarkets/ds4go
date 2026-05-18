package ds4

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestToolLoopRun(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	reg := NewToolRegistry()
	if err := reg.RegisterFunc(ToolSchema{
		Name:        "add",
		Description: "Add two numbers",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "5", nil
	}); err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}

	first, err := dsml.RenderToolCalls([]dsml.ToolCall{{
		Name:      "add",
		Arguments: `{"a":2,"b":3}`,
	}})
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}

	callCount := 0
	loop := ToolLoop{
		Engine:   eng,
		Session:  sess,
		Tools:    reg,
		Thinking: false,
		CompleteFunc: func(prompt *Tokens, opts GenerateOptions) (string, error) {
			callCount++
			if callCount == 1 {
				return "let me calculate" + first, nil
			}
			return "the answer is 5", nil
		},
	}

	result, err := loop.Run(ToolLoopOptions{
		System: "you can use tools",
		History: []ChatMessage{{
			Role:    "user",
			Content: "what is 2 + 3?",
		}},
		MaxRounds: 2,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Assistant.Content != "the answer is 5" {
		t.Fatalf("Assistant.Content = %q", result.Assistant.Content)
	}
	if result.ToolRounds != 1 {
		t.Fatalf("ToolRounds = %d, want 1", result.ToolRounds)
	}
	if len(result.History) != 4 {
		t.Fatalf("expected 4 history messages, got %d", len(result.History))
	}
	if result.History[2].Role != "tool" || result.History[2].Content != "5" {
		t.Fatalf("unexpected tool result message: %#v", result.History[2])
	}
}

func TestToolLoopRunMaxRounds(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	reg := NewToolRegistry()
	if err := reg.RegisterFunc(ToolSchema{
		Name:        "again",
		Description: "Repeat forever",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}

	rendered, err := dsml.RenderToolCalls([]dsml.ToolCall{{
		Name:      "again",
		Arguments: `{"n":1}`,
	}})
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}

	loop := ToolLoop{
		Engine:  eng,
		Session: sess,
		Tools:   reg,
		CompleteFunc: func(prompt *Tokens, opts GenerateOptions) (string, error) {
			return "still working" + rendered, nil
		},
	}

	_, err = loop.Run(ToolLoopOptions{
		History:   []ChatMessage{{Role: "user", Content: "loop"}},
		MaxRounds: 1,
	})
	if err == nil {
		t.Fatal("expected max-rounds error")
	}
}
