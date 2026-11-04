package openaiserver

import (
	"encoding/json"
	"testing"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestServerToolSchemaCoercionAndExactReplay(t *testing.T) {
	for _, family := range []string{"deepseek", "glm", "qwen"} {
		t.Run(family, func(t *testing.T) {
			lib, ctl := ds4api.NewMockLibraryWithControls()
			ctl.SetGLM(family == "glm")
			ctl.SetQwen4(family == "qwen")
			engine, err := lib.NewEngine(ds4.EngineOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			store := dsml.NewReplayStore(16)
			req := chatRequest{Tools: []chatTool{{Type: "function", Function: chatToolFunction{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"},"code":{"type":"string"}}}`)}}}}
			registry, err := requestRegistry(req, store)
			if err != nil {
				t.Fatal(err)
			}
			text, err := dsml.RenderToolCallsSyntax(ds4.ToolSyntax(engine), []dsml.ToolCall{{Name: "lookup", Arguments: `{"count":12,"code":"123"}`}})
			if err != nil {
				t.Fatal(err)
			}
			msg, err := parseRegistryMessage(engine, registry, text, ds4.ThinkNone)
			if err != nil {
				t.Fatal(err)
			}
			if len(msg.ToolCalls) != 1 {
				t.Fatalf("message=%+v", msg)
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &args); err != nil {
				t.Fatal(err)
			}
			if args["count"] != float64(12) || args["code"] != "123" {
				t.Fatalf("arguments=%+v", args)
			}
			if family == "deepseek" {
				if exact, ok := store.Lookup(msg.ToolCalls[0].ID); !ok || exact == "" {
					t.Fatal("exact sampled tool block not retained")
				}
			}
		})
	}
}
