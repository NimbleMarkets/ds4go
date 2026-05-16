// Command openai-compatible serves a minimal OpenAI-style chat endpoint backed
// by the ds4 engine.
//
// It accepts the same arguments as the upstream `ds4-server` (ds4_server.c);
// see --help. This example exercises the model, HTTP, and default-token flags;
// the disk-KV-cache flags are parsed for argument parity but not used.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/NimbleMarkets/ds4-go/ds4"
	"github.com/NimbleMarkets/ds4-go/internal/cliopts"
	"github.com/spf13/pflag"
)

type chatRequest struct {
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
	if cfg.Lib != "" {
		lib, err := ds4.Load(cfg.Lib)
		if err != nil {
			return err
		}
		ds4.SetDefaultLibrary(lib)
	}

	engine, err := ds4.NewEngine(cfg.EngineOptions())
	if err != nil {
		return err
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
		prompt, err := buildPrompt(engine, req.Messages)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer prompt.Free()

		maxTokens := req.MaxTokens
		if maxTokens <= 0 {
			maxTokens = cfg.Tokens
		}
		var text string
		_, err = session.GenerateTokens(prompt, ds4.GenerateOptions{
			MaxTokens: maxTokens,
			StopOnEOS: true,
			OnToken: func(token int) {
				if part, err := engine.TokenText(token); err == nil {
					text += part
				}
			},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatResponse{
			ID:     "chatcmpl-ds4-go",
			Object: "chat.completion",
			Choices: []chatChoice{{
				Index:   0,
				Message: chatMessage{Role: "assistant", Content: text},
			}},
		})
	})

	addr := cfg.Addr()
	fmt.Println("listening on http://" + addr)
	return http.ListenAndServe(addr, mux)
}

func buildPrompt(engine *ds4.Engine, messages []chatMessage) (*ds4.Tokens, error) {
	tokens, err := engine.NewTokens(nil)
	if err != nil {
		return nil, err
	}
	if err := engine.ChatBegin(tokens); err != nil {
		tokens.Free()
		return nil, err
	}
	for _, msg := range messages {
		if msg.Role == "assistant" || msg.Role == "user" || msg.Role == "system" {
			if err := engine.ChatAppendMessage(tokens, msg.Role, msg.Content); err != nil {
				tokens.Free()
				return nil, err
			}
		}
	}
	if err := engine.ChatAppendAssistantPrefix(tokens, ds4.ThinkHigh); err != nil {
		tokens.Free()
		return nil, err
	}
	return tokens, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
