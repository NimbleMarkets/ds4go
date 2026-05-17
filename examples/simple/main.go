// Command simple loads a ds4 model and generates one response.
//
// It accepts the same arguments as the upstream `ds4` CLI (ds4_cli.c); see
// --help. Only the model/prompt/generation flags affect this example, but the
// full ds4 CLI flag surface is parsed so the argument set stays identical.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/spf13/pflag"
)

const defaultPrompt = "Explain Redis streams in one paragraph."

func main() {
	fs := pflag.NewFlagSet("simple", pflag.ContinueOnError)
	cfg := cliopts.RegisterCLI(fs)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: simple [options]\n\nLoad a ds4 model and generate one response.\n\nOptions:\n")
		fmt.Fprint(os.Stderr, fs.FlagUsagesWrapped(100))
	}
	cliopts.Parse(fs, os.Args[1:])

	if err := run(cfg); err != nil {
		fatal(err)
	}
}

func run(cfg *cliopts.CLIConfig) error {
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

	tokens, err := engine.EncodeChatPrompt(cfg.System, promptText, cfg.ThinkMode())
	if err != nil {
		return err
	}
	defer tokens.Free()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	opts := cfg.GenerateOptions()
	opts.Context = ctx
	opts.OnToken = func(token int) {
		if text, err := engine.TokenText(token); err == nil {
			fmt.Print(text)
		}
	}
	if _, err := (ds4.Generator{Engine: engine, Session: session}).GenerateTokens(tokens, opts); err != nil && err != context.Canceled {
		return err
	}
	fmt.Println()
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
