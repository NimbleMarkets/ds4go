package cli

import (
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/NimbleMarkets/ds4go/internal/openaiserver"
	"github.com/spf13/cobra"
)

func newServeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "serve", Short: "Serve local inference HTTP APIs", Args: cobra.NoArgs}
	cfg := cliopts.RegisterServer(cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error { cmd.SilenceUsage = true; return openaiserver.Run(cfg) }
	return cmd
}
