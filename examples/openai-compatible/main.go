// Command openai-compatible serves a small OpenAI-style chat endpoint backed by
// the ds4 engine.
//
// It reuses the shared model/runtime flag set from the upstream-style server
// config, but the HTTP surface here stays intentionally narrow: one
// /v1/chat/completions endpoint with basic DSML tool-call round-tripping.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/dsml"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/spf13/pflag"
)

const (
	maxChatRequestBytes = 4 << 20

	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 30 * time.Minute
	httpIdleTimeout       = 120 * time.Second
)

type chatRequest struct {
	Messages            []chatMessage `json:"messages"`
	Tools               []chatTool    `json:"tools,omitempty"`
	MaxTokens           int           `json:"max_tokens"`
	MaxCompletionTokens int           `json:"max_completion_tokens"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Index   int         `json:"index"`
	Message chatMessage `json:"message"`
}

type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Arguments   string          `json:"arguments,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function chatToolFunction `json:"function"`
}

func main() {
	fs := pflag.NewFlagSet("openai-compatible", pflag.ContinueOnError)
	cfg := cliopts.RegisterServer(fs)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: openai-compatible [options]\n\nServe POST /v1/chat/completions backed by the ds4 engine.\n\nOptions:\n")
		fmt.Fprint(os.Stderr, fs.FlagUsagesWrapped(100))
	}
	cliopts.Parse(fs, os.Args[1:])

	if err := run(cfg); err != nil {
		fatal(err)
	}
}

func run(cfg *cliopts.ServerConfig) error {
	var engine *ds4.Engine
	if cfg.Lib != "" {
		lib, err := ds4.Load(cfg.Lib)
		if err != nil {
			return err
		}
		ds4.SetDefaultLibrary(lib)
		engine, err = lib.NewEngine(cfg.EngineOptions())
		if err != nil {
			return ds4.EnrichEngineOpenError(err)
		}
	} else {
		var err error
		engine, err = ds4.NewEngine(cfg.EngineOptions())
		if err != nil {
			return err
		}
	}
	defer engine.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if cfg.CORS {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		req, err := decodeChatRequest(w, r, maxChatRequestBytes)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session, err := engine.NewSession(cfg.Ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer session.Close()
		prompt, err := buildPrompt(engine, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer prompt.Free()

		maxTokens, err := resolveMaxTokens(req, cfg.Tokens)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var text string
		_, err = (ds4.Generator{Engine: engine, Session: session}).GenerateTokens(prompt, ds4.GenerateOptions{
			MaxTokens: maxTokens,
			StopOnEOS: true,
			Context:   r.Context(),
			OnToken: func(token int) {
				if part, err := engine.TokenText(token); err == nil {
					text += part
				}
			},
		})
		if err != nil {
			if err == context.Canceled {
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		msg := chatMessage{Role: "assistant", Content: text}
		if parsed, err := dsml.ParseCompletion(text, true); err == nil {
			msg.Content = parsed.Content
			if len(parsed.ToolCalls) > 0 {
				msg.ToolCalls = make([]chatToolCall, len(parsed.ToolCalls))
				for i, call := range parsed.ToolCalls {
					id, err := newToolCallID()
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					msg.ToolCalls[i] = chatToolCall{
						ID:   id,
						Type: "function",
						Function: chatToolFunction{
							Name:      call.Name,
							Arguments: call.Arguments,
						},
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatResponse{
			ID:     "chatcmpl-ds4go",
			Object: "chat.completion",
			Choices: []chatChoice{{
				Index:   0,
				Message: msg,
			}},
		})
	})

	addr := cfg.Addr()
	fmt.Println("listening on http://" + addr)
	return newHTTPServer(addr, mux).ListenAndServe()
}

func buildPrompt(engine *ds4.Engine, req chatRequest) (*ds4.Tokens, error) {
	tools, err := convertTools(req.Tools)
	if err != nil {
		return nil, err
	}
	system, history, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	return ds4.BuildChatPrompt(engine, system, tools, history, ds4.ThinkHigh)
}

func decodeChatRequest(w http.ResponseWriter, r *http.Request, maxBytes int64) (chatRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	defer r.Body.Close()

	var req chatRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		return chatRequest{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("request body contains multiple JSON values")
		}
		return chatRequest{}, err
	}
	return req, nil
}

func resolveMaxTokens(req chatRequest, serverLimit int) (int, error) {
	if serverLimit <= 0 {
		return 0, fmt.Errorf("server token limit must be positive")
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = req.MaxCompletionTokens
	}
	if maxTokens <= 0 {
		return serverLimit, nil
	}
	if maxTokens > serverLimit {
		return 0, fmt.Errorf("requested max tokens %d exceeds server limit %d", maxTokens, serverLimit)
	}
	return maxTokens, nil
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}

func convertTools(tools []chatTool) ([]dsml.Tool, error) {
	out := make([]dsml.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "" && tool.Type != "function" {
			return nil, fmt.Errorf("unsupported tool type %q", tool.Type)
		}
		out = append(out, dsml.Tool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return out, nil
}

func convertMessages(messages []chatMessage) (string, []ds4.ChatMessage, error) {
	var systemParts []string
	history := make([]ds4.ChatMessage, 0, len(messages))
	seenCallIDs := map[string]bool{}
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if msg.Content != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case "user":
			history = append(history, ds4.ChatMessage{
				Role:    "user",
				Content: msg.Content,
			})
		case "assistant":
			calls := make([]ds4.ToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				if tc.ID != "" {
					seenCallIDs[tc.ID] = true
				}
				calls[i] = ds4.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}
			}
			history = append(history, ds4.ChatMessage{
				Role:      "assistant",
				Content:   msg.Content,
				ToolCalls: calls,
			})
		case "tool":
			if msg.ToolCallID == "" || !seenCallIDs[msg.ToolCallID] {
				return "", nil, fmt.Errorf("tool message references unknown tool_call_id %q", msg.ToolCallID)
			}
			history = append(history, ds4.ChatMessage{
				Role:       "tool",
				Content:    msg.Content,
				ToolCallID: msg.ToolCallID,
			})
		default:
			// Keep this example permissive for OpenAI-compatible clients that
			// include fields or roles this narrow server does not consume.
		}
	}
	return strings.Join(systemParts, "\n\n"), history, nil
}

func newToolCallID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "call_" + strings.ToLower(hex.EncodeToString(raw[:])), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
