// Command chat is an interactive ds4 REPL.
//
// It accepts the same arguments as the upstream `ds4` CLI (ds4_cli.c); see
// --help. Only the model/generation flags affect this example, but the full
// ds4 CLI flag surface is parsed so the argument set stays identical.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/NimbleMarkets/ds4-go/ds4"
	"github.com/NimbleMarkets/ds4-go/internal/cliopts"
	"github.com/spf13/pflag"
)

type message struct {
	role    string
	content string
}

func main() {
	fs := pflag.NewFlagSet("chat", pflag.ContinueOnError)
	cfg := cliopts.RegisterCLI(fs)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: chat [options]\n\nInteractive ds4 chat REPL. Type /quit to exit.\n\nOptions:\n")
		fmt.Fprint(os.Stderr, fs.FlagUsagesWrapped(100))
	}
	cliopts.Parse(fs, os.Args[1:])

	if err := run(cfg); err != nil {
		fatal(err)
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

	session, err := engine.NewSession(cfg.Ctx)
	if err != nil {
		return err
	}
	defer session.Close()

	var history []message
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
		history = append(history, message{"user", line})
		prompt, err := buildPrompt(engine, cfg.System, history, cfg.ThinkMode())
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
		history = append(history, message{"assistant", response.String()})
	}
}

func buildPrompt(engine *ds4.Engine, system string, history []message, think ds4.ThinkMode) (*ds4.Tokens, error) {
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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
