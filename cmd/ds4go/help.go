package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newHelpCommand(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long:  "Help about any command, or 'ds4go help cheat' for a quick-reference command tree.",
		// When run without a recognized subcommand, look up the target in
		// root's command tree (so "ds4go help model" still works) or fall back
		// to the default root help.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if err := root.Help(); err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout)
				fmt.Fprintln(os.Stdout, "Tip: run 'ds4go help cheat' for a quick-reference command tree.")
				return nil
			}
			target, _, err := root.Find(args)
			if err != nil || target == root {
				if err := root.Help(); err != nil {
					return err
				}
				fmt.Fprintln(os.Stdout)
				fmt.Fprintln(os.Stdout, "Tip: run 'ds4go help cheat' for a quick-reference command tree.")
				return nil
			}
			return target.Help()
		},
	}
	cmd.AddCommand(newHelpCheatCommand(root))
	return cmd
}

func newHelpCheatCommand(root *cobra.Command) *cobra.Command {
	var (
		asJSON bool
		asMD   bool
		depth1 bool
		depth2 bool
		depth3 bool
	)

	cmd := &cobra.Command{
		Use:   "cheat",
		Short: "Quick-reference command tree",
		Long:  "Print a compact cheat sheet showing every command and subcommand.\n\nDepth flags control how many levels to show (default: 3).",
		RunE: func(cmd *cobra.Command, args []string) error {
			depth := cheatDepth(depth1, depth2, depth3)
			switch {
			case asJSON:
				return printCheatSheetJSON(root, depth)
			case asMD:
				printCheatSheetMD(root, depth)
				return nil
			default:
				printCheatSheet(root, depth)
				return nil
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&asMD, "md", false, "output as Markdown")
	cmd.MarkFlagsMutuallyExclusive("json", "md")
	cmd.Flags().BoolVarP(&depth1, "one", "1", false, "show 1 level (top-level commands only)")
	cmd.Flags().BoolVarP(&depth2, "two", "2", false, "show 2 levels")
	cmd.Flags().BoolVarP(&depth3, "three", "3", false, "show 3 levels (default)")
	cmd.MarkFlagsMutuallyExclusive("one", "two", "three")
	return cmd
}

func cheatDepth(one, two, three bool) int {
	switch {
	case one:
		return 1
	case two:
		return 2
	default:
		return 3
	}
}

// visibleCommands returns non-hidden, non-help children of cmd.
func visibleCommands(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, c := range cmd.Commands() {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func printCheatSheet(root *cobra.Command, maxDepth int) {
	fmt.Println("ds4go — command cheat sheet")
	fmt.Println()

	visible := visibleCommands(root)
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	printCheatLevel(w, visible, "  ", maxDepth, 1)
	w.Flush()
	fmt.Println()
	fmt.Println("Run 'ds4go help <command>' for detailed usage.")
}

func printCheatLevel(w *tabwriter.Writer, cmds []*cobra.Command, prefix string, maxDepth, depth int) {
	for i, cmd := range cmds {
		isLast := i == len(cmds)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}
		fmt.Fprintf(w, "%s%s%s\t%s\n", prefix, connector, cmd.Name(), cmd.Short)

		if depth < maxDepth {
			children := visibleCommands(cmd)
			if len(children) > 0 {
				continuation := prefix + "│   "
				if isLast {
					continuation = prefix + "    "
				}
				printCheatLevel(w, children, continuation, maxDepth, depth+1)
			}
		}

		// Blank spacer between top-level sections.
		if depth == 1 && !isLast {
			fmt.Fprintf(w, "%s│\n", prefix)
		}
	}
}

func printCheatSheetMD(root *cobra.Command, maxDepth int) {
	fmt.Println("# ds4go — command cheat sheet")
	fmt.Println()
	visible := visibleCommands(root)
	printCheatMDLevel(visible, "", maxDepth, 1)
	fmt.Println()
	fmt.Println("Run `ds4go help <command>` for detailed usage.")
}

func printCheatMDLevel(cmds []*cobra.Command, prefix string, maxDepth, depth int) {
	for _, cmd := range cmds {
		fmt.Printf("%s- **%s** — %s\n", prefix, cmd.Name(), cmd.Short)
		if depth < maxDepth {
			children := visibleCommands(cmd)
			if len(children) > 0 {
				printCheatMDLevel(children, prefix+"  ", maxDepth, depth+1)
			}
		}
	}
}

type cheatCommand struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Subcommands []cheatCommand `json:"subcommands,omitempty"`
}

func printCheatSheetJSON(root *cobra.Command, maxDepth int) error {
	result := buildCheatJSON(visibleCommands(root), maxDepth, 1)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func buildCheatJSON(cmds []*cobra.Command, maxDepth, depth int) []cheatCommand {
	var result []cheatCommand
	for _, cmd := range cmds {
		entry := cheatCommand{Name: cmd.Name(), Description: cmd.Short}
		if depth < maxDepth {
			children := visibleCommands(cmd)
			if len(children) > 0 {
				entry.Subcommands = buildCheatJSON(children, maxDepth, depth+1)
			}
		}
		result = append(result, entry)
	}
	return result
}
