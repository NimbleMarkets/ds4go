// Command ds4-go is a pure-Go CLI for the ds4 inference engine.
//
// It accepts the same arguments as the upstream `ds4` CLI (ds4_cli.c); see
// --help. The only addition is --lib, which points at the libds4 shared
// library the wrapper loads at runtime.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/NimbleMarkets/ds4-go/ds4"
	"github.com/NimbleMarkets/ds4-go/internal/cliopts"
	"github.com/spf13/pflag"
)

type cliMessage struct {
	role    string
	content string
}

func main() {
	fs := pflag.NewFlagSet("ds4-go", pflag.ContinueOnError)
	cfg := cliopts.RegisterCLI(fs)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr,
			"Usage: ds4-go [(-p PROMPT | --prompt-file FILE)] [options]\n\n"+
				"Run ds4 inference. With no prompt, starts an interactive chat (ds4>).\n\n"+
				"Options:\n")
		fmt.Fprint(os.Stderr, fs.FlagUsagesWrapped(100))
	}
	cliopts.Parse(fs, os.Args[1:])

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "ds4-go:", err)
		os.Exit(1)
	}
}

func run(cfg *cliopts.CLIConfig) error {
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

	// --inspect and imatrix collection run without a session timeline.
	switch {
	case cfg.Inspect:
		return engine.Summary()
	case cfg.IMatrixOut != "":
		return engine.CollectIMatrix(cfg.IMatrixDataset, cfg.IMatrixOut, cfg.Ctx, cfg.IMatrixMaxPrompts, cfg.IMatrixMaxTokens)
	}

	if diag := diagnostic(cfg); diag != "" {
		return runDiagnostic(engine, cfg, diag)
	}

	session, err := engine.NewSession(cfg.Ctx)
	if err != nil {
		return err
	}
	defer session.Close()

	promptText, err := cfg.PromptText()
	if err != nil {
		return err
	}
	if cfg.DumpLogprobs != "" {
		return dumpLogprobs(engine, session, cfg, promptText)
	}
	if promptText != "" {
		return generateOne(engine, session, cfg, promptText)
	}
	return chat(engine, session, cfg)
}

// diagnostic returns the name of the selected one-shot diagnostic, if any.
func diagnostic(cfg *cliopts.CLIConfig) string {
	switch {
	case cfg.DumpTokens:
		return "dump-tokens"
	case cfg.HeadTest:
		return "head-test"
	case cfg.FirstTokenTest:
		return "first-token-test"
	case cfg.MetalGraphTest:
		return "metal-graph-test"
	case cfg.MetalGraphFullTest:
		return "metal-graph-full-test"
	case cfg.MetalGraphPromptTest:
		return "metal-graph-prompt-test"
	default:
		return ""
	}
}

func runDiagnostic(engine *ds4.Engine, cfg *cliopts.CLIConfig, diag string) error {
	promptText, err := cfg.PromptText()
	if err != nil {
		return err
	}
	prompt, err := engine.EncodeChatPrompt(cfg.System, promptText, cfg.ThinkMode())
	if err != nil {
		return err
	}
	defer prompt.Free()

	switch diag {
	case "dump-tokens":
		return engine.DumpTokens(prompt)
	case "head-test":
		return engine.HeadTest(prompt)
	case "first-token-test":
		return engine.FirstTokenTest(prompt)
	case "metal-graph-test":
		return engine.MetalGraphTest(prompt)
	case "metal-graph-full-test":
		return engine.MetalGraphFullTest(prompt)
	case "metal-graph-prompt-test":
		return engine.MetalGraphPromptTest(prompt, cfg.Ctx)
	default:
		return fmt.Errorf("unknown diagnostic %q", diag)
	}
}

func generateOne(engine *ds4.Engine, session *ds4.Session, cfg *cliopts.CLIConfig, promptText string) error {
	tokens, err := engine.EncodeChatPrompt(cfg.System, promptText, cfg.ThinkMode())
	if err != nil {
		return err
	}
	defer tokens.Free()

	opts := cfg.GenerateOptions()
	opts.OnToken = func(token int) {
		if text, err := engine.TokenText(token); err == nil {
			fmt.Print(text)
		}
	}
	_, err = session.GenerateTokens(tokens, opts)
	fmt.Println()
	return err
}

func chat(engine *ds4.Engine, session *ds4.Session, cfg *cliopts.CLIConfig) error {
	var history []cliMessage
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("ds4> ")
		if !in.Scan() {
			return in.Err()
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return nil
		}
		history = append(history, cliMessage{role: "user", content: line})
		prompt, err := buildChatPrompt(engine, cfg.System, history, cfg.ThinkMode())
		if err != nil {
			return err
		}
		opts := cfg.GenerateOptions()
		var response strings.Builder
		opts.OnToken = func(token int) {
			if text, err := engine.TokenText(token); err == nil {
				response.WriteString(text)
				fmt.Print(text)
			}
		}
		_, err = session.GenerateTokens(prompt, opts)
		prompt.Free()
		fmt.Println()
		if err != nil {
			return err
		}
		history = append(history, cliMessage{role: "assistant", content: response.String()})
	}
}

func buildChatPrompt(engine *ds4.Engine, system string, history []cliMessage, think ds4.ThinkMode) (*ds4.Tokens, error) {
	tokens, err := engine.NewTokens(nil)
	if err != nil {
		return nil, err
	}
	if err := engine.ChatBegin(tokens); err != nil {
		tokens.Free()
		return nil, err
	}
	if system != "" {
		if err := engine.ChatAppendMessage(tokens, "system", system); err != nil {
			tokens.Free()
			return nil, err
		}
	}
	for _, msg := range history {
		if err := engine.ChatAppendMessage(tokens, msg.role, msg.content); err != nil {
			tokens.Free()
			return nil, err
		}
	}
	if err := engine.ChatAppendAssistantPrefix(tokens, think); err != nil {
		tokens.Free()
		return nil, err
	}
	return tokens, nil
}

// logprobStep is one greedy generation step recorded by --dump-logprobs.
type logprobStep struct {
	Token int            `json:"token"`
	Text  string         `json:"text"`
	Top   []logprobScore `json:"top"`
}

type logprobScore struct {
	ID      int     `json:"id"`
	Logit   float32 `json:"logit"`
	Logprob float32 `json:"logprob"`
}

func dumpLogprobs(engine *ds4.Engine, session *ds4.Session, cfg *cliopts.CLIConfig, promptText string) error {
	tokens, err := engine.EncodeChatPrompt(cfg.System, promptText, cfg.ThinkMode())
	if err != nil {
		return err
	}
	defer tokens.Free()
	if err := session.SyncTokens(tokens); err != nil {
		return err
	}

	eos := engine.TokenEOS()
	steps := make([]logprobStep, 0, cfg.Tokens)
	for i := 0; i < cfg.Tokens; i++ {
		top, err := session.TopLogprobs(cfg.LogprobsTopK)
		if err != nil {
			return err
		}
		token := session.Argmax()
		if token == eos {
			break
		}
		text, _ := engine.TokenText(token)
		scores := make([]logprobScore, len(top))
		for j, s := range top {
			scores[j] = logprobScore{ID: s.ID, Logit: s.Logit, Logprob: s.Logprob}
		}
		steps = append(steps, logprobStep{Token: token, Text: text, Top: scores})
		if err := session.Eval(token); err != nil {
			return err
		}
	}

	f, err := os.Create(cfg.DumpLogprobs)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(steps)
}
