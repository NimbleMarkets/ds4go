// Command openai-compatible serves the same HTTP APIs as ds4go serve.
package main

import (
	"fmt"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/NimbleMarkets/ds4go/internal/openaiserver"
	"github.com/spf13/pflag"
	"os"
)

func main() {
	fs := pflag.NewFlagSet("openai-compatible", pflag.ContinueOnError)
	cfg := cliopts.RegisterServer(fs)
	fs.Usage = func() { fmt.Fprint(os.Stderr, "Usage: openai-compatible [options]\n\n"+fs.FlagUsagesWrapped(100)) }
	cliopts.Parse(fs, os.Args[1:])
	if err := openaiserver.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
