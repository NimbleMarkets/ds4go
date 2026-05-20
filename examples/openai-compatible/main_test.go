package main

import (
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
