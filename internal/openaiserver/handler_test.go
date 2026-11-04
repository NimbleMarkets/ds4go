package openaiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

func TestRequestSampling(t *testing.T) {
	for _, tc := range []struct {
		body string
		bad  bool
	}{
		{`{}`, false}, {`{"temperature":0,"top_p":0,"min_p":0,"top_k":0,"seed":0,"ignore_eos":true}`, false},
		{`{"temperature":-1}`, true}, {`{"top_k":-1}`, true}, {`{"top_k":2147483648}`, true}, {`{"top_p":1.1}`, true}, {`{"min_p":-0.1}`, true},
		{`{"ignore_eos":true}`, true}, {`{"ignore_eos":true,"temperature":1}`, true}, {`{"stop":3}`, true}, {`{"stop":[""]}`, true}, {`{"stop":["END","STOP"]}`, false},
	} {
		var req chatRequest
		if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
			t.Fatal(err)
		}
		opts, _, err := requestSampling(req)
		if (err != nil) != tc.bad {
			t.Errorf("%s: %v", tc.body, err)
		}
		if req.Seed != nil && opts.Seed != *req.Seed {
			t.Error("explicit zero seed lost")
		}
		if req.TopP != nil && opts.TopP != *req.TopP {
			t.Error("explicit zero top_p lost")
		}
	}
}
func TestStopFilterAcrossEveryBoundary(t *testing.T) {
	for _, tc := range []struct {
		text    string
		stops   []string
		want    string
		stopped bool
	}{
		{"hello END trailing", []string{"END"}, "hello ", true},
		{"hello STOP END", []string{"END", "STOP"}, "hello ", true},
		{"hello EN", []string{"END"}, "hello EN", false},
		{"café 世界!", []string{"DONE"}, "café 世界!", false},
		{"aaaab!", []string{"aaab"}, "a", true},
	} {
		for size := 1; size <= len(tc.text); size++ {
			f := stopFilter{stops: tc.stops}
			var out strings.Builder
			for i := 0; i < len(tc.text); i += size {
				out.WriteString(f.push(tc.text[i:min(i+size, len(tc.text))]))
			}
			out.WriteString(f.close())
			if out.String() != tc.want || f.stopped != tc.stopped {
				t.Errorf("%q size=%d: %q stopped=%v", tc.text, size, out.String(), f.stopped)
			}
		}
	}
}
func mockHandler(t *testing.T) http.Handler {
	t.Helper()
	lib := ds4api.NewMockLibrary()
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.Close)
	handler := newHandler(engine, nil, &cliopts.ServerConfig{EngineFlags: cliopts.EngineFlags{Ctx: 512}, Tokens: 20})
	t.Cleanup(handler.Close)
	return handler
}
func TestChatStreamMatchesJSONAndUsage(t *testing.T) {
	handler := mockHandler(t)
	body := `{"messages":[{"role":"user","content":"hi"}],"temperature":0,"reasoning_effort":"none","max_tokens":4,"seed":42`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body+"}")))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var full struct {
		Choices []struct {
			Message chatMessage `json:"message"`
			Finish  string      `json:"finish_reason"`
		} `json:"choices"`
		Usage usage `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &full); err != nil {
		t.Fatal(err)
	}
	stream := httptest.NewRecorder()
	handler.ServeHTTP(stream, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body+`,"stream":true,"stream_options":{"include_usage":true}}`)))
	if stream.Code != 200 || stream.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %s", stream.Code, stream.Body.String())
	}
	var content, reasoning, finish string
	var gotUsage usage
	done := false
	role := false
	for _, line := range strings.Split(stream.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			done = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta  delta   `json:"delta"`
				Finish *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatal(err)
		}
		for _, c := range chunk.Choices {
			content += c.Delta.Content
			reasoning += c.Delta.Reasoning
			role = role || c.Delta.Role == "assistant"
			if c.Finish != nil {
				finish = *c.Finish
			}
		}
		if chunk.Usage != nil {
			gotUsage = *chunk.Usage
		}
	}
	if !done || !role || content != full.Choices[0].Message.Content || reasoning != full.Choices[0].Message.ReasoningContent || finish != full.Choices[0].Finish || !reflect.DeepEqual(gotUsage, full.Usage) {
		t.Fatalf("stream differs: %s\nJSON: %s", stream.Body.String(), rec.Body.String())
	}
	if finish != "length" || gotUsage.CompletionTokens != 4 {
		t.Fatalf("finish=%s usage=%+v", finish, gotUsage)
	}
}
func TestChatRejectsInvalidBeforeSSE(t *testing.T) {
	handler := mockHandler(t)
	for _, body := range []string{`{"stream":true,"ignore_eos":true}`, `{"stream":true,"temperature":-1}`, `{"stream":true,"tool_choice":"required"}`} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		if rec.Code != 400 || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
}
func TestGenerationCancelledWhileQueued(t *testing.T) {
	lifetime, cancelLife := context.WithCancel(context.Background())
	defer cancelLife()
	s := server{cfg: &cliopts.ServerConfig{Tokens: 10}, slots: make(chan *ds4api.Session, 1), lifetime: lifetime}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.generate(ctx, chatRequest{}, &result{}, nil); err != context.Canceled {
		t.Fatalf("err=%v", err)
	}
}

func TestCancellationDuringFinalStreamFlushDiscardsSession(t *testing.T) {
	s := mockHandler(t).(*server)
	var req chatRequest
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"hi"}],"temperature":0,"reasoning_effort":"none","max_tokens":4}`), &req); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out result
	emitted := false
	err := s.generate(ctx, req, &out, func(d delta) error {
		// The short mock completion fits entirely in the DSML decoder's lookbehind
		// buffer and is emitted by Close(), after the native generation loop ends.
		if d.Content != "" {
			emitted = true
			cancel()
		}
		return nil
	})
	if !emitted || err != context.Canceled {
		t.Fatalf("emitted=%v error=%v", emitted, err)
	}
	slot := <-s.slots
	s.slots <- slot
	if slot != nil {
		t.Fatal("cancelled session was retained")
	}
	if err := s.generate(context.Background(), req, &result{}, nil); err != nil {
		t.Fatalf("next request: %v", err)
	}
}
