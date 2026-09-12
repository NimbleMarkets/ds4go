package main

import (
	"encoding/base64"
	"encoding/json"
	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveMaxTokens(t *testing.T) {
	tests := []struct {
		name        string
		req         chatRequest
		serverLimit int
		want        int
		wantErr     bool
	}{
		{name: "default", serverLimit: 128, want: 128},
		{name: "max_tokens", req: chatRequest{MaxTokens: 64}, serverLimit: 128, want: 64},
		{name: "max_completion_tokens", req: chatRequest{MaxCompletionTokens: 96}, serverLimit: 128, want: 96},
		{name: "too high", req: chatRequest{MaxTokens: 129}, serverLimit: 128, wantErr: true},
		{name: "bad server limit", serverLimit: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveMaxTokens(tt.req, tt.serverLimit)
			if tt.wantErr {
				if err == nil {
					t.Fatal("resolveMaxTokens succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveMaxTokens: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveMaxTokens = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDecodeChatRequestRejectsOversize(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	_, err := decodeChatRequest(httptest.NewRecorder(), req, 8)
	if err == nil {
		t.Fatal("decodeChatRequest succeeded, want size error")
	}
}

func TestNewHTTPServerHasTimeouts(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:0", http.NewServeMux())
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout is not set")
	}
	if srv.ReadTimeout <= 0 {
		t.Fatal("ReadTimeout is not set")
	}
	if srv.WriteTimeout <= 0 {
		t.Fatal("WriteTimeout is not set")
	}
	if srv.IdleTimeout <= 0 {
		t.Fatal("IdleTimeout is not set")
	}
}

func imageContent(n int) string {
	part := `{"type":"image_url","image_url":{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nimg")) + `"}}`
	parts := make([]string, 0, n+1)
	parts = append(parts, `{"type":"text","text":"look"}`)
	for i := 0; i < n; i++ {
		parts = append(parts, part)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestConvertMessagesCarriesImageParts(t *testing.T) {
	var req chatRequest
	body := `{"messages":[{"role":"system","content":"be brief"},{"role":"user","content":` + imageContent(1) + `}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	system, history, err := convertMessages(req.Messages)
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if system != "be brief" {
		t.Errorf("system = %q", system)
	}
	if len(history) != 1 || len(history[0].Parts) != 2 || history[0].Parts[1].Image == nil || len(history[0].Parts[1].Image.Data) == 0 {
		t.Fatalf("history = %+v, want one user message with a text part and an image part", history)
	}
	if history[0].Content != "" {
		t.Errorf("Content = %q, want Parts to carry the message", history[0].Content)
	}
}

func TestConvertMessagesCapsImagesPerRequest(t *testing.T) {
	var req chatRequest
	body := `{"messages":[{"role":"user","content":` + imageContent(ds4.MaxHTTPImages) + `},{"role":"user","content":` + imageContent(1) + `}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if _, _, err := convertMessages(req.Messages); err == nil {
		t.Fatalf("accepted %d images in one request", ds4.MaxHTTPImages+1)
	}
}

func TestChatRequestBodyLimitMatchesUpstream(t *testing.T) {
	if maxChatRequestBytes != 64<<20 {
		t.Errorf("maxChatRequestBytes = %d, want upstream ds4-server's 64 MiB", maxChatRequestBytes)
	}
}

func TestBuildPromptWithImages(t *testing.T) {
	var req chatRequest
	body := `{"messages":[{"role":"user","content":` + imageContent(1) + `}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}

	// Text-only engine: a clear client error rather than a crash.
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(false)
	plain, err := lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err := buildPrompt(plain, nil, req); err == nil || !strings.Contains(err.Error(), "--vision") {
		t.Errorf("text-only engine: err = %v, want a --vision hint", err)
	}

	// Vision engine: the prompt carries one image span.
	vlib, vctl := ds4api.NewMockLibraryWithControls()
	vctl.SetVision(true)
	eng, err := vlib.NewEngine(ds4.EngineOptions{VisionPath: "enc.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	prompt, err := buildPrompt(eng, ds4.NewImageEncoder(eng), req)
	if err != nil {
		t.Fatalf("buildPrompt: %v", err)
	}
	defer prompt.Free()
	if len(prompt.Images) != 1 {
		t.Errorf("prompt has %d image spans, want 1", len(prompt.Images))
	}
}

// The prompt is rendered in a thinking mode, so generation must not treat the
// closing think marker as a stop: with a mismatched ThinkMode the completion
// is only the reasoning block and the client sees empty content.
func TestGenerateOptionsMatchThePromptThinkMode(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	eng, err := lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	var req chatRequest
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), &req); err != nil {
		t.Fatal(err)
	}
	prompt, err := buildPrompt(eng, nil, req)
	if err != nil {
		t.Fatal(err)
	}
	defer prompt.Free()

	run := func(opts ds4.GenerateOptions) []int {
		t.Helper()
		session, err := eng.NewSession(4096)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		out, err := (ds4.Generator{Engine: eng, Session: session}).GeneratePrompt(prompt, opts)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	const want = 6
	first := run(generateOptions(want, nil, nil))
	if len(first) != want {
		t.Fatalf("baseline generated %d tokens, want %d", len(first), want)
	}
	// From now on the second generated token is a thinking-control marker.
	ctl.SetThinkingControlTokens(first[1])
	// The stop token itself is not emitted, so ThinkNone yields one token.
	if got := run(ds4.GenerateOptions{MaxTokens: want, StopOnEOS: true, ThinkMode: ds4.ThinkNone}); len(got) != 1 {
		t.Fatalf("control: ThinkNone generated %d tokens, want 1 (stop at the marker); the mock is not exercising the rule", len(got))
	}
	if got := run(generateOptions(want, nil, nil)); len(got) != want {
		t.Errorf("server options generated %d tokens, want %d: generation stopped at a thinking marker", len(got), want)
	}
}
