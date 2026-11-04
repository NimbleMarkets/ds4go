//go:build ds4_integration

package openaiserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/spf13/pflag"
)

// TestRealServerDiskCache exercises the actual native payload path, then HTTP
// concurrency, cancellation recovery, and tool replay on the selected backend.
// No network listener is required. DS4_SERVER_TEST_FLAGS is a JSON string array
// of extra engine flags (for example SSD streaming or a smaller expert cache).
func TestRealServerDiskCache(t *testing.T) {
	if os.Getenv("DS4_LIB") == "" || os.Getenv("DS4_MODEL") == "" {
		t.Skip("set DS4_LIB and DS4_MODEL")
	}
	fs := pflag.NewFlagSet("integration", pflag.ContinueOnError)
	cfg := cliopts.RegisterServer(fs)
	args := []string{"--lib", os.Getenv("DS4_LIB"), "--model", os.Getenv("DS4_MODEL"), "--mtp=", "--ctx=2048", "--tokens=256", "--prefill-chunk=128", "--batched-session=2"}
	if b := os.Getenv("DS4_BACKEND"); b != "" {
		args = append(args, "--backend", b)
	}
	if raw := os.Getenv("DS4_SERVER_TEST_FLAGS"); raw != "" {
		var extra []string
		if err := json.Unmarshal([]byte(raw), &extra); err != nil {
			t.Fatal(err)
		}
		args = append(args, extra...)
	}
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	cfg.KVDiskDir = t.TempDir()
	cfg.KVCacheMinTokens = 1
	cfg.KVCacheBoundaryTrimTokens = 1
	cfg.KVCacheBoundaryAlignTokens = 0
	if err := cfg.ValidateCacheFlags(); err != nil {
		t.Fatal(err)
	}
	lib, err := ds4.Load(cfg.Lib)
	if err != nil {
		t.Fatal(err)
	}
	previous := ds4api.CurrentDefaultLibrary()
	ds4.SetDefaultLibrary(lib)
	defer ds4.SetDefaultLibrary(previous)
	opts, err := cfg.EngineOptions()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := lib.NewEngine(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	namespace, err := cacheNamespace(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	start := func(cache bool) *server {
		s := newHandler(engine, nil, cfg)
		if cache {
			var err error
			s.cache, err = openDiskCache(cfg.KVDiskDir, namespace, int64(cfg.KVDiskSpaceMB)<<20)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			s.cache.minTokens = cfg.KVCacheMinTokens
		}
		return s
	}
	body := `{"messages":[{"role":"user","content":"Reply with the word hello."}],"temperature":0,"max_tokens":16,"reasoning_effort":"none"}`
	request := func(s *server, body string) (chatMessage, error) {
		r := httptest.NewRecorder()
		s.ServeHTTP(r, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		if r.Code != 200 {
			return chatMessage{}, &integrationError{r.Body.String()}
		}
		var out struct {
			Choices []struct{ Message chatMessage }
		}
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			return chatMessage{}, err
		}
		if len(out.Choices) != 1 {
			return chatMessage{}, &integrationError{r.Body.String()}
		}
		return out.Choices[0].Message, nil
	}
	plain := start(false)
	want, err := request(plain, body)
	plain.Close()
	if err != nil {
		t.Fatal(err)
	}
	cold := start(true)
	got, err := request(cold, body)
	if err != nil {
		cold.Close()
		t.Fatal(err)
	}
	entries, err := cold.cache.entries()
	cold.Close()
	if err != nil || len(entries) == 0 {
		t.Fatalf("no native checkpoint written: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cold checkpoint changed output: got %+v want %+v", got, want)
	}
	warm := start(true)
	defer warm.Close()
	got, err = request(warm, body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored checkpoint changed output: got %+v want %+v", got, want)
	}
	if warm.cache.hits == 0 {
		t.Fatal("request did not restore a disk checkpoint")
	}
	t.Log("PASS native checkpoint persisted across handler restart; cached and uncached greedy output match")
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			msg, err := request(warm, body)
			if err != nil {
				t.Error(err)
			} else if !reflect.DeepEqual(msg, want) {
				t.Errorf("concurrent output differs: %+v", msg)
			}
		})
	}
	wg.Wait()
	t.Log("PASS concurrent requests")
	var req chatRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel on the first content token, after the session has begun inference.
	var out result
	err = warm.generate(ctx, req, &out, func(d delta) error {
		if d.Content != "" || d.Reasoning != "" {
			cancel()
		}
		return nil
	})
	cancel()
	if err == nil {
		t.Fatal("generation did not observe cancellation")
	}
	if _, err := request(warm, body); err != nil {
		t.Fatalf("request after cancellation: %v", err)
	}
	t.Log("PASS cancellation followed by successful request")
	toolBody := `{"messages":[{"role":"user","content":"Use the add tool to calculate 19 plus 23. Do not calculate it yourself. After receiving the tool result, reply with the result."}],"tools":[{"type":"function","function":{"name":"add","description":"Add two integers.","parameters":{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]}}}],"temperature":0,"max_tokens":256,"reasoning_effort":"none"}`
	msg, err := request(warm, toolBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "add" {
		t.Fatalf("model did not emit the expected tool call: %+v", msg)
	}
	var arguments struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	if err := json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &arguments); err != nil || arguments.A+arguments.B != 42 {
		t.Fatalf("unexpected tool arguments: %s (%v)", msg.ToolCalls[0].Function.Arguments, err)
	}
	var toolReq map[string]any
	if err := json.Unmarshal([]byte(toolBody), &toolReq); err != nil {
		t.Fatal(err)
	}
	toolReq["messages"] = append(toolReq["messages"].([]any), msg, map[string]any{"role": "tool", "tool_call_id": msg.ToolCalls[0].ID, "content": "42"})
	replay, _ := json.Marshal(toolReq)
	answer, err := request(warm, string(replay))
	if err != nil {
		t.Fatal(err)
	}
	answerJSON, _ := json.Marshal(answer.Content)
	if !strings.Contains(string(answerJSON), "42") || len(answer.ToolCalls) != 0 {
		t.Fatalf("tool replay answer: %+v", answer)
	}
	t.Log("PASS model-generated tool arguments and replay")
}

type integrationError struct{ message string }

func (e *integrationError) Error() string { return e.message }
