package cli

import (
	"github.com/spf13/cobra"
)

// Execute runs the root command for the ds4go CLI.
func Execute() error {
	root := newRootCommand()
	return root.Execute()
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "ds4go",
		Short: "Pure-Go ds4 inference tooling",
		Long:  "ds4go manages libds4, curated models, and prompt/chat inference for the ds4 engine.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.AddCommand(
		newPromptCommand(),
		newInstallCommand(),
		newValidateCommand(),
		newStatusCommand(),
		newUninstallCommand(),
		newModelCommand(),
		newWebCommand(),
	)
	root.SetHelpCommand(newHelpCommand(root))
	return root
}
