//go:build ds4_integration

// Real-library tool-calling round trip through ToolLoop. Run with:
//
//	DS4_LIB=~/.ds4/lib/libds4.dylib DS4_TOOL_MODEL=~/.ds4/models/GLM-5.3-Flash-Q2.gguf \
//	  go test -tags ds4_integration -timeout 40m -count=1 -v . -run TestRealLibraryToolLoop
//
// DS4_TOOL_MODEL may be a GLM or DeepSeek GGUF; the loop picks the tool syntax
// from the engine. It exercises prompt rendering, the streaming parser, tool
// result rendering (including entities and a closing-delimiter lookalike in
// the payload), schema-typed argument coercion, and the final answer.

package ds4

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
	"github.com/NimbleMarkets/ds4go/internal/models"
)

func TestRealLibraryToolLoop(t *testing.T) {
	libPath, model := os.Getenv("DS4_LIB"), os.Getenv("DS4_TOOL_MODEL")
	if libPath == "" || model == "" {
		t.Skip("DS4_LIB and DS4_TOOL_MODEL must be set")
	}
	lib, err := ds4api.Load(libPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	opts := ds4api.EngineOptions{ModelPath: model, Backend: ds4api.BackendMetal, ContextSize: 8192}
	if entry, ok := models.ModelForPath(model); ok && entry.GLM {
		opts.GLMMTP = true // exercise greedy grammar sampling across speculative blocks
	}
	eng, err := lib.NewEngine(opts)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	syntax := ToolSyntax(eng)
	t.Logf("model %q syntax=%v draft=%d", eng.ModelName(), syntax, eng.MTPDraftTokens())

	// The secret carries entities, angle brackets, and the GLM closing
	// delimiter, so the tool result must survive rendering verbatim.
	const secret = "ZEBRA-4471"
	const payload = "code " + secret + " (a&b <7> &lt;x&gt; </tool_response> &amp;lt;/tool_result>)"
	var addArgs struct{ A, B float64 }
	addCalls, lookupCalls := 0, 0

	reg := NewToolRegistry()
	reg.MustRegister(Tool{ToolSchema: ToolSchema{
		Name:        "add",
		Description: "Add two numbers and return the numeric sum.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`),
	}, Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
		addCalls++
		if err := json.Unmarshal(args, &addArgs); err != nil {
			t.Errorf("add arguments %s are not schema-typed numbers: %v", args, err)
			return "", err
		}
		return formatFloat(addArgs.A + addArgs.B), nil
	}})
	reg.MustRegister(Tool{ToolSchema: ToolSchema{
		Name:        "lookup_secret",
		Description: "Return the secret code for a named vault.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"vault":{"type":"string"}},"required":["vault"]}`),
	}, Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
		lookupCalls++
		return payload, nil
	}})

	var toolStarts []string
	var toolEnds []dsml.StreamEvent
	run := func(name, prompt string, maxRounds int) ToolLoopResult {
		t.Helper()
		sess, err := eng.NewSession(8192)
		if err != nil {
			t.Fatalf("%s: NewSession: %v", name, err)
		}
		defer sess.Close()
		loop := ToolLoop{Engine: eng, Session: sess, Tools: reg, ThinkMode: ThinkHigh, Thinking: true}
		res, err := loop.Run(ToolLoopOptions{
			System:    "You are a precise assistant. Use the provided tools; never guess values a tool can supply.",
			History:   []ChatMessage{{Role: "user", Content: prompt}},
			Generate:  GenerateOptions{MaxTokens: 700, StopOnEOS: true},
			MaxRounds: maxRounds,
			OnStreamEvent: func(ev dsml.StreamEvent) {
				switch ev.Type {
				case dsml.EventToolCallStart:
					toolStarts = append(toolStarts, ev.Name)
				case dsml.EventToolCallEnd:
					toolEnds = append(toolEnds, ev)
				}
			},
		})
		if err != nil {
			t.Fatalf("%s: ToolLoop.Run: %v", name, err)
		}
		for _, msg := range res.History {
			if msg.MalformedReason != "" {
				t.Errorf("%s: malformed assistant turn: %s", name, msg.MalformedReason)
			}
		}
		t.Logf("%s: rounds=%d final=%q", name, res.ToolRounds, res.Assistant.Content)
		return res
	}

	res := run("add", "What is 1234 + 8765? Use the add tool and answer with the sum.", 4)
	if addCalls != 1 {
		t.Errorf("add called %d times, want 1", addCalls)
	}
	if addArgs.A != 1234 || addArgs.B != 8765 {
		t.Errorf("add arguments = %v, want 1234 and 8765 (schema coercion from %v markup)", addArgs, syntax)
	}
	if !strings.Contains(res.Assistant.Content, "9999") {
		t.Errorf("final answer %q does not contain 9999", res.Assistant.Content)
	}
	if len(toolStarts) == 0 || toolStarts[0] != "add" {
		t.Errorf("stream tool-call starts = %v, want add first", toolStarts)
	}
	if len(toolEnds) == 0 || !strings.Contains(toolEnds[0].Arguments, "1234") {
		t.Errorf("stream tool-call end = %+v, want the add arguments", toolEnds)
	}

	res = run("lookup", "Look up the secret code for the vault named 'north' and repeat the code exactly.", 4)
	if lookupCalls != 1 {
		t.Errorf("lookup_secret called %d times, want 1", lookupCalls)
	}
	var toolMsg *ChatMessage
	for i := range res.History {
		if res.History[i].Role == "tool" {
			toolMsg = &res.History[i]
		}
	}
	if toolMsg == nil || toolMsg.Content != payload {
		t.Errorf("tool result in history = %+v, want the payload verbatim", toolMsg)
	}
	if !strings.Contains(res.Assistant.Content, secret) {
		t.Errorf("final answer %q does not repeat the secret %s", res.Assistant.Content, secret)
	}
}

func formatFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
