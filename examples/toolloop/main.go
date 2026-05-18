// Command toolloop demonstrates end-to-end DSML tool calling with Go handlers.
//
// By default it loads a real ds4 model and lets the model emit DSML tool calls.
// Pass --mock to exercise the Go tool loop without libds4 or a model.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/spf13/pflag"
)

const (
	defaultPrompt = "Use the add tool to compute 19 + 23. After the tool result, answer with the number."
	toolSystem    = "When arithmetic is requested, call the add tool before answering."
)

func main() {
	fs := pflag.NewFlagSet("toolloop", pflag.ContinueOnError)
	cfg := cliopts.RegisterCLI(fs)
	mock := fs.Bool("mock", false, "run with ds4api.NewMockLibrary and scripted model output")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: toolloop [options]\n\nRun a DSML tool-calling loop with a Go add tool.\n\nOptions:\n")
		fmt.Fprint(os.Stderr, fs.FlagUsagesWrapped(100))
	}
	cliopts.Parse(fs, os.Args[1:])

	if err := run(cfg, *mock); err != nil {
		fatal(err)
	}
}

func run(cfg *cliopts.CLIConfig, mock bool) error {
	engine, err := openEngine(cfg, mock)
	if err != nil {
		return err
	}
	defer engine.Close()

	session, err := engine.NewSession(cfg.Ctx)
	if err != nil {
		return err
	}
	defer session.Close()

	promptText, err := cfg.PromptText()
	if err != nil {
		return err
	}
	if promptText == "" {
		promptText = defaultPrompt
	}

	reg := ds4.NewToolRegistry()
	if err := reg.RegisterFunc(ds4.ToolSchema{
		Name:        "add",
		Description: "Add two numbers and return the numeric sum.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`),
	}, addTool); err != nil {
		return err
	}

	system := cfg.System
	if system != "" {
		system += "\n\n"
	}
	system += toolSystem

	gen := cfg.GenerateOptions()
	gen.OnToken = func(token int) {
		if text, err := engine.TokenText(token); err == nil {
			fmt.Print(text)
		}
	}

	loop := ds4.ToolLoop{
		Engine:    engine,
		Session:   session,
		Tools:     reg,
		ThinkMode: cfg.ThinkMode(),
		Thinking:  cfg.ThinkMode() != ds4.ThinkNone,
	}
	if mock {
		loop.Thinking = false
		loop.ThinkMode = ds4.ThinkNone
		loop.CompleteFunc = mockCompletion()
	}

	fmt.Fprintf(os.Stderr, "user: %s\n\nassistant stream:\n", promptText)
	result, err := loop.Run(ds4.ToolLoopOptions{
		System: system,
		History: []ds4.ChatMessage{{
			Role:    "user",
			Content: promptText,
		}},
		Generate:  gen,
		MaxRounds: 4,
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n\nfinal assistant: %s\n", result.Assistant.Content)
	fmt.Fprintf(os.Stderr, "tool rounds: %d\n", result.ToolRounds)
	return nil
}

func openEngine(cfg *cliopts.CLIConfig, mock bool) (*ds4.Engine, error) {
	if mock {
		lib := ds4api.NewMockLibrary()
		ds4.SetDefaultLibrary(lib)
		return lib.NewEngine(ds4.EngineOptions{})
	}
	if cfg.Lib != "" {
		lib, err := ds4.Load(cfg.Lib)
		if err != nil {
			return nil, err
		}
		ds4.SetDefaultLibrary(lib)
		engine, err := lib.NewEngine(cfg.EngineOptions())
		if err != nil {
			return nil, ds4.EnrichEngineOpenError(err)
		}
		return engine, nil
	}
	return ds4.NewEngine(cfg.EngineOptions())
}

func addTool(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var args struct {
		A float64 `json:"a"`
		B float64 `json:"b"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("decode add args: %w", err)
	}
	sum := args.A + args.B
	fmt.Fprintf(os.Stderr, "\n[tool add] %.15g + %.15g = %.15g\n", args.A, args.B, sum)
	return fmt.Sprintf("%.15g", sum), nil
}

func mockCompletion() func(*ds4.Tokens, ds4.GenerateOptions) (string, error) {
	round := 0
	return func(prompt *ds4.Tokens, opts ds4.GenerateOptions) (string, error) {
		round++
		if round == 1 {
			out, err := dsml.RenderToolCalls([]dsml.ToolCall{{
				Name:      "add",
				Arguments: `{"a":19,"b":23}`,
			}})
			if err != nil {
				return "", err
			}
			fmt.Print("I will use the add tool." + out)
			return "I will use the add tool." + out, nil
		}
		fmt.Print("The answer is 42.")
		return "The answer is 42.", nil
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
