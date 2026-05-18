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
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/dsml"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/spf13/pflag"
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
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

		maxTokens := req.MaxTokens
		if maxTokens <= 0 {
			maxTokens = req.MaxCompletionTokens
		}
		if maxTokens <= 0 {
			maxTokens = cfg.Tokens
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
	return http.ListenAndServe(addr, mux)
}

func buildPrompt(engine *ds4.Engine, req chatRequest) (*ds4.Tokens, error) {
	tokens, err := engine.NewTokens(nil)
	if err != nil {
		return nil, err
	}
	if err := engine.ChatBegin(tokens); err != nil {
		tokens.Free()
		return nil, err
	}
	toolsSection, err := renderToolsSection(req.Tools)
	if err != nil {
		tokens.Free()
		return nil, err
	}
	appendedTools := false
	if toolsSection != "" && !hasSystemMessage(req.Messages) {
		if err := engine.ChatAppendMessage(tokens, "system", toolsSection); err != nil {
			tokens.Free()
			return nil, err
		}
		appendedTools = true
	}
	seenCallIDs := map[string]bool{}
	for i := 0; i < len(req.Messages); {
		msg := req.Messages[i]
		switch msg.Role {
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					seenCallIDs[tc.ID] = true
				}
			}
		case "tool":
			var content strings.Builder
			for i < len(req.Messages) && req.Messages[i].Role == "tool" {
				toolMsg := req.Messages[i]
				if toolMsg.ToolCallID == "" || !seenCallIDs[toolMsg.ToolCallID] {
					tokens.Free()
					return nil, fmt.Errorf("tool message references unknown tool_call_id %q", toolMsg.ToolCallID)
				}
				_, part, err := renderMessage(toolMsg, toolsSection, false)
				if err != nil {
					tokens.Free()
					return nil, err
				}
				content.WriteString(part)
				i++
			}
			if err := engine.ChatAppendMessage(tokens, "user", content.String()); err != nil {
				tokens.Free()
				return nil, err
			}
			continue
		}
		role, content, err := renderMessage(msg, toolsSection, !appendedTools)
		if err != nil {
			tokens.Free()
			return nil, err
		}
		i++
		if role == "" {
			continue
		}
		if err := engine.ChatAppendMessage(tokens, role, content); err != nil {
			tokens.Free()
			return nil, err
		}
		if msg.Role == "system" && toolsSection != "" && !appendedTools {
			appendedTools = true
		}
	}
	if err := engine.ChatAppendAssistantPrefix(tokens, ds4.ThinkHigh); err != nil {
		tokens.Free()
		return nil, err
	}
	return tokens, nil
}

func renderToolsSection(tools []chatTool) (string, error) {
	if len(tools) == 0 {
		return "", nil
	}
	out := make([]dsml.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "" && tool.Type != "function" {
			return "", fmt.Errorf("unsupported tool type %q", tool.Type)
		}
		out = append(out, dsml.Tool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return dsml.RenderToolsSection(out)
}

func hasSystemMessage(messages []chatMessage) bool {
	for _, msg := range messages {
		if msg.Role == "system" {
			return true
		}
	}
	return false
}

func renderMessage(msg chatMessage, toolsSection string, appendTools bool) (string, string, error) {
	switch msg.Role {
	case "system":
		content := msg.Content
		if appendTools && toolsSection != "" {
			if content == "" {
				content = toolsSection
			} else {
				content = toolsSection + "\n\n" + content
			}
		}
		return "system", content, nil
	case "user":
		return "user", msg.Content, nil
	case "assistant":
		renderedCalls, err := renderAssistantToolCalls(msg.ToolCalls)
		if err != nil {
			return "", "", err
		}
		return "assistant", msg.Content + renderedCalls, nil
	case "tool":
		result, err := dsml.RenderToolResult(msg.Content)
		if err != nil {
			return "", "", err
		}
		return "user", result, nil
	default:
		return "", "", nil
	}
}

// renderAssistantToolCalls re-renders prior assistant tool calls from their
// JSON arguments. This stateless endpoint does not use dsml.ReplayStore:
// exact sampled-DSML replay needs conversation-scoped state carried across
// requests, which a stateful server (not this example) would have to own.
func renderAssistantToolCalls(calls []chatToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	invokes := make([]string, len(calls))
	for i, call := range calls {
		invoke, err := dsml.RenderToolCall(dsml.ToolCall{
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
		if err != nil {
			return "", err
		}
		invokes[i] = invoke
	}
	return dsml.WrapToolCalls(invokes), nil
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
