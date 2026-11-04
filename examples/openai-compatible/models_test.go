package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
)

func TestModelDiscoveryMatchesLoadedEngine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*ds4api.MockControls)
		ids   []string
	}{
		{"DeepSeek", func(c *ds4api.MockControls) {}, []string{"deepseek-v4-flash", "deepseek-v4-pro"}},
		{"V4.1", func(c *ds4api.MockControls) { c.SetDeepSeek41(true) }, []string{"deepseek-v4.1-flash"}},
		{"GLM5.2", func(c *ds4api.MockControls) { c.SetGLM(true) }, []string{"glm-5.2", "glm-5.2-chat", "glm-5.2-reasoner"}},
		{"GLM5.3", func(c *ds4api.MockControls) { c.SetGLM(true); c.SetGLM53(true) }, []string{"glm-5.3-flash", "glm-5.3-flash-chat", "glm-5.3-flash-reasoner"}},
		{"Qwen", func(c *ds4api.MockControls) { c.SetQwen4(true) }, []string{"qwen3.8-flash-next", "qwen3.8-flash-next-chat", "qwen3.8-flash-next-reasoner"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, vision := range []bool{false, true} {
				for _, limit := range []int{128, 8192} {
					lib, ctl := ds4api.NewMockLibraryWithControls()
					tc.setup(ctl)
					ctl.SetVision(vision)
					engine, err := lib.NewEngine(ds4api.EngineOptions{})
					if err != nil {
						t.Fatal(err)
					}
					mux := http.NewServeMux()
					registerModelRoutes(mux, engine, 4096, limit, true)
					// Discovery must remain a startup snapshot, with no FFI calls per request.
					engine.Close()
					rec := httptest.NewRecorder()
					mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
					if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
						t.Fatalf("response: %d %v", rec.Code, rec.Header())
					}
					var list modelList
					if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
						t.Fatal(err)
					}
					if list.Object != "list" || len(list.Data) != len(tc.ids) {
						t.Fatalf("list = %+v", list)
					}
					for i, entry := range list.Data {
						if entry.ID != tc.ids[i] || entry.Name != "Mock" || entry.Object != "model" || entry.OwnedBy != "ds4go" || entry.Created != 1767225600 {
							t.Errorf("identity = %+v", entry)
						}
						inputs := []string{"text"}
						if vision {
							inputs = append(inputs, "image")
						}
						if !reflect.DeepEqual(entry.Architecture.InputModalities, inputs) || !reflect.DeepEqual(entry.Architecture.OutputModalities, []string{"text"}) {
							t.Errorf("modalities = %+v", entry.Architecture)
						}
						if entry.ContextLength != 4096 || entry.TopProvider.ContextLength != 4096 || entry.TopProvider.MaxCompletionTokens != min(limit, 4096) || entry.TopProvider.IsModerated {
							t.Errorf("limits = %+v", entry)
						}
						if !reflect.DeepEqual(entry.SupportedParameters, []string{"tools", "max_tokens", "max_completion_tokens", "reasoning_effort", "chat_template_kwargs"}) {
							t.Errorf("parameters = %v", entry.SupportedParameters)
						}
						detail := httptest.NewRecorder()
						mux.ServeHTTP(detail, httptest.NewRequest("GET", "/v1/models/"+entry.ID, nil))
						var got modelMetadata
						if err := json.Unmarshal(detail.Body.Bytes(), &got); err != nil {
							t.Fatal(err)
						}
						if detail.Code != 200 || !reflect.DeepEqual(got, entry) {
							t.Errorf("detail differs from list: %+v", got)
						}
					}
				}
			}
		})
	}
}

func TestModelRoutesAliasesErrorsAndCORS(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for _, cors := range []bool{false, true} {
		mux := http.NewServeMux()
		registerModelRoutes(mux, engine, 4096, 1024, cors)
		for _, tc := range []struct {
			method, path string
			status       int
		}{
			{"GET", "/v1/models/qwen/qwen3.8-flash-next-chat", 200},
			{"GET", "/v1/models/zai/glm-5.3-flash-reasoner", 200},
			{"GET", "/v1/models/glm-5.2-no-think", 200},
			{"GET", "/v1/models/not-a-model", 404},
			{"GET", "/v1/models/", 404},
			{"GET", "/v1/models/glm-5.2/extra", 404},
			{"POST", "/v1/models", 405},
			{"POST", "/v1/models/deepseek-v4-flash", 405},
			{"OPTIONS", "/v1/models", 204},
			{"OPTIONS", "/v1/models/deepseek-v4-flash", 204},
		} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status {
				t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.status)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); (got == "*") != cors {
				t.Errorf("cors=%v: origin=%q", cors, got)
			}
			if cors && rec.Header().Get("Access-Control-Allow-Methods") != "GET, OPTIONS" {
				t.Errorf("CORS methods = %q", rec.Header().Get("Access-Control-Allow-Methods"))
			}
		}
	}
}

func TestModelAliasesControlThinking(t *testing.T) {
	for _, id := range []string{"deepseek-chat", "qwen3.8-flash-next-chat", "qwen/qwen3.8-flash-next-chat", "glm-5.2-no-think", "glm-5.3-flash-nothink", "zai/glm-5.3-flash-chat"} {
		req := chatRequest{Model: id, ReasoningEffort: json.RawMessage(`"high"`)}
		if got, err := thinkModeForRequest(req); err != nil || got != ds4.ThinkNone {
			t.Errorf("%s = %v, %v", id, got, err)
		}
		req.ChatTemplateKwargs = json.RawMessage(`{"enable_thinking":true}`)
		if got, err := thinkModeForRequest(req); err != nil || got != ds4.ThinkHigh {
			t.Errorf("explicit enable for %s = %v, %v", id, got, err)
		}
	}
	for _, id := range []string{"qwen3.8-flash-next-reasoner", "glm-5.2-reasoner", "unknown-chat"} {
		if got, err := thinkModeForRequest(chatRequest{Model: id}); err != nil || got != serverThinkMode {
			t.Errorf("%s = %v, %v", id, got, err)
		}
	}
}

func TestReasoningContentRoundTrip(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	msg, err := parseAssistantMessage(engine, "my reasoning</think>answer", ds4.ThinkHigh)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ReasoningContent != "my reasoning" || msg.Content != "answer" {
		t.Fatalf("message = %+v", msg)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var input chatInputMessage
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	_, history, err := convertMessages([]chatInputMessage{input})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ReasoningContent != "my reasoning" {
		t.Fatalf("replay = %+v", history)
	}
	msg.ReasoningContent = ""
	data, err = json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "reasoning_content") {
		t.Errorf("empty reasoning present: %s", data)
	}
}
