package dsml

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestReadUntilStop(t *testing.T) {
	t.Run("matches the earliest stop", func(t *testing.T) {
		idx, content, stop := readUntilStop(0, "abc<X>def<Y>", []string{"<Y>", "<X>"})
		if content != "abc" {
			t.Errorf("content = %q, want %q", content, "abc")
		}
		if stop != "<X>" {
			t.Errorf("stop = %q, want %q", stop, "<X>")
		}
		if idx != 6 {
			t.Errorf("idx = %d, want 6", idx)
		}
	})

	t.Run("no match returns end of input", func(t *testing.T) {
		idx, content, stop := readUntilStop(0, "abcdef", []string{"<X>"})
		if content != "abcdef" {
			t.Errorf("content = %q, want %q", content, "abcdef")
		}
		if stop != "" {
			t.Errorf("stop = %q, want empty", stop)
		}
		if idx != 6 {
			t.Errorf("idx = %d, want 6", idx)
		}
	})

	t.Run("respects the start index", func(t *testing.T) {
		idx, content, stop := readUntilStop(3, "<X>abc<X>", []string{"<X>"})
		if content != "abc" {
			t.Errorf("content = %q, want %q", content, "abc")
		}
		if stop != "<X>" || idx != 9 {
			t.Errorf("stop = %q idx = %d, want %q 9", stop, idx, "<X>")
		}
	})
}

func TestToJSONString(t *testing.T) {
	if got := toJSONString(`a"b`); got != `"a\"b"` {
		t.Errorf("toJSONString = %q, want %q", got, `"a\"b"`)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	calls := []ToolCall{
		{Name: "add", Arguments: `{"a":2,"b":3}`},
		{Name: "weather", Arguments: `{"city":"New York","days":5}`},
	}

	// Render the calls, then wrap them in a minimal completion the way a
	// model would emit one, and parse it back.
	rendered, err := RenderToolCalls(calls)
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}
	completion := "here are the calls" + rendered + eosToken

	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if msg.Content != "here are the calls" {
		t.Errorf("Content = %q", msg.Content)
	}
	if len(msg.ToolCalls) != len(calls) {
		t.Fatalf("ToolCalls len = %d, want %d", len(msg.ToolCalls), len(calls))
	}
	for i, want := range calls {
		got := msg.ToolCalls[i]
		if got.Name != want.Name {
			t.Errorf("call %d Name = %q, want %q", i, got.Name, want.Name)
		}
		if got.Exact == "" {
			t.Errorf("call %d Exact was not captured", i)
		}
		var gotArgs, wantArgs map[string]any
		if err := json.Unmarshal([]byte(got.Arguments), &gotArgs); err != nil {
			t.Fatalf("call %d arguments not valid JSON: %v", i, err)
		}
		if err := json.Unmarshal([]byte(want.Arguments), &wantArgs); err != nil {
			t.Fatalf("call %d want-arguments not valid JSON: %v", i, err)
		}
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Errorf("call %d Arguments = %v, want %v", i, gotArgs, wantArgs)
		}
	}
}

func TestEncodeDecodeRoundTripNoArguments(t *testing.T) {
	calls := []ToolCall{{Name: "ping", Arguments: ""}}
	rendered, err := RenderToolCalls(calls)
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}
	completion := "calling" + rendered + eosToken

	msg, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Name != "ping" {
		t.Errorf("Name = %q, want ping", msg.ToolCalls[0].Name)
	}
	if msg.ToolCalls[0].Arguments != "{}" {
		t.Errorf("Arguments = %q, want {}", msg.ToolCalls[0].Arguments)
	}
}
