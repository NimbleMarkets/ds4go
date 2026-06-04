// Command ds4go is a pure-Go CLI for the ds4 inference engine.
//
// It accepts the same arguments as the upstream `ds4` CLI (ds4_cli.c); see
// --help. The only addition is --lib, which points at the libds4 shared
// library the wrapper loads at runtime.
package main

import (
	"os"

	"github.com/NimbleMarkets/ds4go/cmd/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
