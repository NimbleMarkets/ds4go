package dsml

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderToolsSection(t *testing.T) {
	out := RenderToolsSection([]Tool{
		{
			Name:        "add",
			Description: "Add two numbers",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	})
	if !strings.Contains(out, "## Tools") {
		t.Error("missing ## Tools heading")
	}
	if !strings.Contains(out, `"name": "add"`) {
		t.Errorf("missing rendered tool name in:\n%s", out)
	}
	if !strings.Contains(out, `"description": "Add two numbers"`) {
		t.Errorf("missing rendered description in:\n%s", out)
	}
	if !strings.Contains(out, `"parameters": {"type":"object"}`) {
		t.Errorf("missing rendered parameters in:\n%s", out)
	}
}

func TestRenderToolsSectionEmptyParameters(t *testing.T) {
	out := RenderToolsSection([]Tool{{Name: "noop"}})
	if !strings.Contains(out, `"parameters": {}`) {
		t.Errorf("empty Parameters should render as {} in:\n%s", out)
	}
}

func TestRenderToolCalls(t *testing.T) {
	out := RenderToolCalls([]ToolCall{
		{Name: "add", Arguments: `{"a":2,"b":3}`},
	})
	want := "\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"add\">\n" +
		"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">2</" + dsmlMarker + "parameter>\n" +
		"<" + dsmlMarker + "parameter name=\"b\" string=\"false\">3</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"
	if out != want {
		t.Errorf("RenderToolCalls =\n%q\nwant\n%q", out, want)
	}
}

func TestRenderToolCallsStringArgument(t *testing.T) {
	out := RenderToolCalls([]ToolCall{
		{Name: "weather", Arguments: `{"city":"New York"}`},
	})
	if !strings.Contains(out, `name="city" string="true">New York<`) {
		t.Errorf("string argument not rendered with string=\"true\" in:\n%s", out)
	}
}

func TestRenderToolCallsEmpty(t *testing.T) {
	if got := RenderToolCalls(nil); got != "" {
		t.Errorf("RenderToolCalls(nil) = %q, want empty", got)
	}
}

func TestRenderToolCallsPreservesKeyOrder(t *testing.T) {
	args := `{"z":1,"a":2,"m":3}`
	first := RenderToolCalls([]ToolCall{{Name: "t", Arguments: args}})
	for range 20 {
		if RenderToolCalls([]ToolCall{{Name: "t", Arguments: args}}) != first {
			t.Fatal("RenderToolCalls output is not deterministic")
		}
	}
	zi := strings.Index(first, `name="z"`)
	ai := strings.Index(first, `name="a"`)
	mi := strings.Index(first, `name="m"`)
	if !(zi < ai && ai < mi) {
		t.Errorf("key order not preserved: z=%d a=%d m=%d", zi, ai, mi)
	}
}

func TestRenderToolCallsNoArguments(t *testing.T) {
	out := RenderToolCalls([]ToolCall{{Name: "ping", Arguments: ""}})
	if !strings.Contains(out, `invoke name="ping"`) {
		t.Errorf("missing invoke header in:\n%s", out)
	}
	if strings.Contains(out, "parameter") {
		t.Errorf("a no-argument call should emit no parameters in:\n%s", out)
	}
}

func TestRenderToolsSectionInvalidParameters(t *testing.T) {
	out := RenderToolsSection([]Tool{
		{Name: "bad", Parameters: json.RawMessage("not-json")},
	})
	if !strings.Contains(out, `"parameters": {}`) {
		t.Errorf("invalid Parameters should fall back to {} in:\n%s", out)
	}
}
