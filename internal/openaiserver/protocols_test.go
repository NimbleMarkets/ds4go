package openaiserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesToolReplay(t *testing.T) {
	raw := `{"model":"qwen3.8-flash-next","instructions":"be useful","input":[{"role":"user","content":[{"type":"input_text","text":"look"}]},{"type":"reasoning","summary":[{"type":"summary_text","text":"check first"}]},{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"result"}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"max_output_tokens":32,"reasoning":{"effort":"low"}}`
	req, err := translateRequest(json.RawMessage(raw), "responses")
	if err != nil {
		t.Fatal(err)
	}
	system, history, err := convertMessages(req.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if system != "be useful" || len(history) != 3 || history[1].ReasoningContent != "check first" || history[2].ToolCallID != "c1" || history[2].Content != "result" || req.MaxTokens != 32 || req.Tools[0].Function.Name != "lookup" || string(req.ReasoningEffort) != `"low"` {
		t.Fatalf("req=%+v history=%+v", req, history)
	}
}
func TestAnthropicToolReplay(t *testing.T) {
	raw := `{"max_tokens":32,"system":[{"type":"text","text":"be useful"}],"messages":[{"role":"user","content":"look"},{"role":"assistant","content":[{"type":"thinking","thinking":"check first"},{"type":"tool_use","id":"c1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":[{"type":"text","text":"result"}]},{"type":"text","text":"continue"}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"thinking":{"type":"disabled"},"stop_sequences":["STOP"]}`
	req, err := translateRequest(json.RawMessage(raw), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	system, history, err := convertMessages(req.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if system != "be useful" || len(history) != 4 || history[1].ReasoningContent != "check first" || history[2].ToolCallID != "c1" || history[2].Content != "result" || history[3].Content != "continue" || req.Tools[0].Function.Name != "lookup" || string(req.ReasoningEffort) != `"none"` {
		t.Fatalf("req=%+v history=%+v", req, history)
	}
}
func TestProtocolAdaptersRejectUnsupported(t *testing.T) {
	for _, tc := range []struct{ protocol, body string }{
		{"responses", `{"input":"hi","previous_response_id":"r1"}`},
		{"responses", `{"input":"hi","background":true}`},
		{"responses", `{"input":[{"type":"unknown"}]}`},
		{"responses", `{"input":"hi","tools":[{"type":"web_search"}]}`},
		{"anthropic", `{"messages":[],"max_tokens":0}`},
		{"anthropic", `{"max_tokens":10,"messages":[{"role":"user","content":[{"type":"tool_use","id":"a","name":"b","input":{}}]}]}`},
		{"completion", `{"prompt":["one","two"]}`},
	} {
		if _, err := translateRequest(json.RawMessage(tc.body), tc.protocol); err == nil {
			t.Errorf("accepted %s %s", tc.protocol, tc.body)
		}
	}
}
func sseEvents(t *testing.T, body string) []map[string]any {
	t.Helper()
	events := []map[string]any{}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := strings.TrimPrefix(line, "data: ")
		if raw == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}
func TestProtocolHTTPStreaming(t *testing.T) {
	h := mockHandler(t)
	for _, tc := range []struct{ path, body, start, end string }{
		{"/v1/responses", `{"input":"hi","max_output_tokens":4,"reasoning":{"effort":"none"},"temperature":0,"stream":true}`, "response.created", "response.incomplete"},
		{"/v1/messages", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":4,"thinking":{"type":"disabled"},"temperature":0,"stream":true}`, "message_start", "message_stop"},
		{"/v1/completions", `{"prompt":"hi","max_tokens":4,"temperature":0,"stream":true}`, "", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body)))
			if r.Code != 200 || r.Header().Get("Content-Type") != "text/event-stream" {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			events := sseEvents(t, r.Body.String())
			if len(events) < 3 {
				t.Fatal(r.Body.String())
			}
			if tc.start != "" && (events[0]["type"] != tc.start || events[len(events)-1]["type"] != tc.end) {
				t.Fatal(r.Body.String())
			}
			if tc.path == "/v1/responses" {
				for i, e := range events {
					if e["sequence_number"] != float64(i) {
						t.Fatalf("sequence: %+v", e)
					}
				}
				last := events[len(events)-1]["response"].(map[string]any)
				if last["usage"].(map[string]any)["output_tokens"] != float64(4) {
					t.Fatal(last)
				}
			}
			if tc.path == "/v1/messages" {
				msg := events[0]["message"].(map[string]any)
				if msg["usage"].(map[string]any)["input_tokens"].(float64) <= 0 {
					t.Fatal(msg)
				}
			}
		})
	}
}
func TestProtocolToolStreamIdentities(t *testing.T) {
	for _, protocol := range []string{"responses", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			rec := httptest.NewRecorder()
			out := result{ID: "test", Model: "test", Finish: "tool_calls", Message: chatMessage{Role: "assistant", ReasoningContent: "plan", Content: "checking", ToolCalls: []chatToolCall{{ID: "c1", Type: "function", Function: chatToolFunction{Name: "lookup", Arguments: `{"a":1}`}}}}}
			p := newProtocolStream(rec, protocol, &out)
			for _, d := range []delta{{Role: "assistant"}, {Reasoning: "plan"}, {Content: "checking"}, {ToolCalls: []toolDelta{{Index: 0, ID: "c1", Type: "function", Function: chatToolFunction{Name: "lookup", Arguments: `{"a":1}`}}}}} {
				if err := p.emit(d); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.finish(out); err != nil {
				t.Fatal(err)
			}
			events := sseEvents(t, rec.Body.String())
			if protocol == "responses" {
				added := map[string]int{}
				done := map[string]int{}
				for _, e := range events {
					if e["type"] == "response.output_item.added" {
						item := e["item"].(map[string]any)
						added[item["id"].(string)] = int(e["output_index"].(float64))
					}
					if e["type"] == "response.output_item.done" {
						item := e["item"].(map[string]any)
						done[item["id"].(string)] = int(e["output_index"].(float64))
					}
				}
				if len(added) != 3 || len(done) != 3 {
					t.Fatal(rec.Body.String())
				}
				for id, index := range added {
					if got, ok := done[id]; !ok || got != index {
						t.Fatal("item identity changed")
					}
				}
				last := events[len(events)-1]["response"].(map[string]any)
				items := last["output"].([]any)
				if items[2].(map[string]any)["call_id"] != "c1" {
					t.Fatal(last)
				}
			} else {
				starts, stops := 0, 0
				for _, e := range events {
					if e["type"] == "content_block_start" {
						starts++
					}
					if e["type"] == "content_block_stop" {
						stops++
					}
				}
				if starts != 3 || stops != 3 {
					t.Fatal(rec.Body.String())
				}
			}
		})
	}
}
func TestProtocolOutputReplaysAsInput(t *testing.T) {
	out := result{ID: "test", Message: chatMessage{Role: "assistant", ReasoningContent: "plan", Content: "checking", ToolCalls: []chatToolCall{{ID: "c1", Type: "function", Function: chatToolFunction{Name: "lookup", Arguments: `{"a":1}`}}}}, Finish: "tool_calls"}
	for _, protocol := range []string{"responses", "anthropic"} {
		var body any
		if protocol == "responses" {
			items := responseItems(out)
			items = append(items, map[string]any{"type": "function_call_output", "call_id": "c1", "output": "result"})
			body = map[string]any{"input": items}
		} else {
			body = map[string]any{"max_tokens": 20, "messages": []any{map[string]any{"role": "assistant", "content": anthropicBlocks(out)}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "c1", "content": "result"}}}}}
		}
		req, err := translateRequest(jsonRaw(body), protocol)
		if err != nil {
			t.Fatal(err)
		}
		_, history, err := convertMessages(req.Messages)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 2 || history[0].ReasoningContent != "plan" || history[0].Content != "checking" || history[1].ToolCallID != "c1" {
			t.Fatalf("%s %+v", protocol, history)
		}
	}
}
