package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/NimbleMarkets/ds4go/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newModelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "model",
		Aliases: []string{"models"},
		Short:   "Browse, download, and manage curated ds4 models",
		// With no arguments, default to listing models. Any argument here is
		// an unrecognized subcommand — a valid one would have been dispatched
		// by cobra — so show help instead of silently running "list".
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runModelList(nil)
			}
			if args[0] == "help" {
				return cmd.Help()
			}
			return unknownSubcommand(cmd, args[0])
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List installed and available models",
			Args:    cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runModelList(args)
			},
		},
		newModelInfoCommand(),
		&cobra.Command{
			Use:   "set [alias]",
			Short: "Set the default chat model",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runModelSet(args)
			},
		},
		newModelDownloadCommand(),
		newModelDeleteCommand(),
	)
	return cmd
}

func newModelInfoCommand() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "info [alias]",
		Short: "Show details for a curated model",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModelInfo(args, all, asJSON)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "show details for every curated model")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of formatted text")
	return cmd
}

func newModelDownloadCommand() *cobra.Command {
	var token string
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "download [alias]",
		Aliases: []string{"pull"},
		Short:   "Download a curated model from Hugging Face",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModelDownloadWithToken(args, token, dryRun)
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "Hugging Face token (defaults to HF_TOKEN or ~/.cache/huggingface/token)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be downloaded without downloading it")
	return cmd
}

func newModelDeleteCommand() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:     "delete [alias]",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a downloaded model from disk",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModelDelete(args, assumeYes)
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// unknownSubcommand prints the command's help text, then returns an error so
// the process exits non-zero when an invalid subcommand pattern is used.
func unknownSubcommand(cmd *cobra.Command, name string) error {
	if err := cmd.Help(); err != nil {
		return err
	}
	return fmt.Errorf("unknown command %q for %q", name, cmd.CommandPath())
}

func modelManager() *models.Manager {
	m := models.NewManager()
	m.Out = os.Stdout
	m.ProgressOut = os.Stderr
	return m
}

func runModelList(args []string) error {
	fs := pflag.NewFlagSet("model list", pflag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	m := modelManager()
	list, cfg, err := m.List()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%-19s%s\n", "Model directory:", m.ModelsDir)
	fmt.Fprintf(os.Stdout, "%-19s%s\n", "Library directory:", ds4.DefaultLibraryDir())
	fmt.Fprintln(os.Stdout)
	if active := activeDefault(list); active != "" {
		fmt.Fprintf(os.Stdout, "Default: %s", active)
		fmt.Fprint(os.Stdout, " (active)")
	} else if cfg.DefaultModel != "" {
		fmt.Fprintf(os.Stdout, "Default: none (configured %s is not installed)", cfg.DefaultModel)
	} else {
		fmt.Fprint(os.Stdout, "Default: none")
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout)
	// Size the alias and RAM columns to the widest value present so long names
	// (e.g. distributed split aliases) don't overflow and misalign the table.
	aliasW, ramW := 14, 12
	for _, mdl := range list {
		if w := lipgloss.Width(mdl.Alias); w > aliasW {
			aliasW = w
		}
		if w := lipgloss.Width(mdl.RecommendedRAM); w > ramW {
			ramW = w
		}
	}
	printModelGroup("Installed", list, true, aliasW, ramW)
	fmt.Fprintln(os.Stdout)
	printModelGroup("Available to download", list, false, aliasW, ramW)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Use: ds4go model set q4-imatrix")
	fmt.Fprintln(os.Stdout, "     ds4go model download q4-imatrix")
	fmt.Fprintln(os.Stdout, "     ds4go model delete q4-imatrix")
	return nil
}

func runModelInfo(args []string, all, asJSON bool) error {
	m := modelManager()
	list, _, err := m.List()
	if err != nil {
		return err
	}
	if all {
		if len(args) > 0 {
			return fmt.Errorf("ds4go model info: --all does not take an alias")
		}
		if asJSON {
			return printModelsJSON(list, m)
		}
		for i, model := range list {
			if i > 0 {
				fmt.Fprintln(os.Stdout)
			}
			printModelInfo(model, m)
		}
		return nil
	}
	var alias string
	switch len(args) {
	case 0:
		var err error
		alias, err = tui.PickModelAlias("Select a model for details", list, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
	case 1:
		alias = args[0]
	default:
		return fmt.Errorf("usage: ds4go model info [alias]")
	}
	for _, model := range list {
		if model.Alias == alias {
			if asJSON {
				return printModelsJSON([]models.Model{model}, m)
			}
			printModelInfo(model, m)
			return nil
		}
	}
	return fmt.Errorf("unknown model %q", alias)
}

// modelJSON is a curated model augmented with its resolved on-disk path.
type modelJSON struct {
	models.Model
	Path string `json:"path"`
}

// printModelsJSON emits the models as a JSON array to stdout.
func printModelsJSON(list []models.Model, m *models.Manager) error {
	out := make([]modelJSON, len(list))
	for i, model := range list {
		out[i] = modelJSON{Model: model, Path: m.ModelsDir + "/" + model.FileName}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func runModelSet(args []string) error {
	fs := pflag.NewFlagSet("model set", pflag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	alias := fs.Arg(0)
	if fs.NArg() == 0 {
		m := modelManager()
		list, _, err := m.List()
		if err != nil {
			return err
		}
		list = filterDefaultableInstalled(list)
		alias, err = tui.PickModelAlias("Select installed default model", list, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
	} else if fs.NArg() != 1 {
		return fmt.Errorf("usage: ds4go model set [alias]")
	}
	if err := modelManager().Set(alias); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Default model set to %s\n", alias)
	return nil
}

func runModelDownloadWithToken(args []string, token string, dryRun bool) error {
	alias := ""
	if len(args) > 0 {
		alias = args[0]
	}
	if len(args) == 0 {
		m := modelManager()
		list, _, err := m.List()
		if err != nil {
			return err
		}
		list = filterDownloadable(list)
		alias, err = tui.PickModelAlias("Select a model to download", list, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
	} else if len(args) != 1 {
		return fmt.Errorf("usage: ds4go model download [alias]")
	}
	if dryRun {
		_, err := modelManager().DownloadDryRun(context.Background(), alias, token)
		return err
	}
	model, err := modelManager().Download(context.Background(), alias, token)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Downloaded %s\n", model.Alias)
	return nil
}

func runModelDelete(args []string, assumeYes bool) error {
	m := modelManager()
	list, _, err := m.List()
	if err != nil {
		return err
	}
	var alias string
	switch len(args) {
	case 0:
		removable := filterRemovable(list)
		if len(removable) == 0 {
			return fmt.Errorf("no downloaded models to delete")
		}
		var err error
		alias, err = tui.PickModelAlias("Select a model to delete", removable, os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
	case 1:
		alias = args[0]
	default:
		return fmt.Errorf("usage: ds4go model delete [alias]")
	}

	var target models.Model
	found := false
	for _, model := range list {
		if model.Alias == alias {
			target, found = model, true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown model %q", alias)
	}
	if !target.Installed && !target.Partial {
		return fmt.Errorf("%s is not installed", alias)
	}

	if !assumeYes {
		fmt.Fprintf(os.Stdout, "About to delete %s (%s) from %s\n", target.Alias, target.FileName, m.ModelsDir)
		if target.Default {
			fmt.Fprintln(os.Stdout, "This is the active default model; the default will be cleared.")
		}
		result, err := tui.Confirm("Are you sure?", false, os.Stdin, os.Stdout)
		if err != nil {
			return fmt.Errorf("read prompt response: %w", err)
		}
		if result != tui.ConfirmYes {
			fmt.Fprintln(os.Stdout, "Cancelled")
			return nil
		}
	}
	return m.Delete(alias)
}

func filterDownloadable(list []models.Model) []models.Model {
	var out []models.Model
	for _, model := range list {
		if !model.Installed {
			out = append(out, model)
		}
	}
	if len(out) == 0 {
		return list
	}
	return out
}

func filterRemovable(list []models.Model) []models.Model {
	var out []models.Model
	for _, model := range list {
		if model.Installed || model.Partial {
			out = append(out, model)
		}
	}
	return out
}

func activeDefault(list []models.Model) string {
	for _, model := range list {
		if model.Default {
			return model.Alias
		}
	}
	return ""
}

func filterDefaultableInstalled(list []models.Model) []models.Model {
	var out []models.Model
	for _, model := range list {
		if model.Installed && !model.Optional && !model.Distributed {
			out = append(out, model)
		}
	}
	if len(out) == 0 {
		for _, model := range list {
			if !model.Optional && !model.Distributed {
				out = append(out, model)
			}
		}
	}
	return out
}

func printModelGroup(title string, list []models.Model, installed bool, aliasW, ramW int) {
	fmt.Fprintf(os.Stdout, "%s:\n", title)
	any := false

	aliasStyle := lipgloss.NewStyle().Width(aliasW)
	sizeStyle := lipgloss.NewStyle().Width(10).Align(lipgloss.Right)
	ramStyle := lipgloss.NewStyle().Width(ramW)
	muted := tui.MutedStyle
	success := tui.ActiveStyle

	for _, model := range list {
		if model.Installed != installed {
			continue
		}
		any = true
		check := " "
		if model.Installed {
			check = "✓"
		} else if model.Partial {
			check = "…"
		}

		checkStr := check
		if model.Installed {
			checkStr = success.Render(check)
		}

		def := ""
		if model.Default {
			def = success.Render(" (active default)")
		}
		partial := ""
		if model.Partial {
			partial = muted.Render(" partial " + tui.FormatPartialModel(model.PartialBytes, model.SizeGB))
		}
		flags := tui.ModelFlags(model)
		if flags != "" {
			flags = muted.Render("(" + flags + ")")
		}

		// Align columns using lipgloss to handle display width correctly.
		colAlias := aliasStyle.Render(model.Alias)
		colSize := sizeStyle.Render(fmt.Sprintf("%.1f GiB", model.SizeGB))
		colRAM := ramStyle.Render(model.RecommendedRAM)

		fmt.Fprintf(os.Stdout, "  %s %s  %s   %s  %s%s%s\n",
			checkStr, colAlias, colSize, colRAM, flags, def, partial)
	}
	if !any {
		fmt.Fprintln(os.Stdout, "  none")
	}
}

func printModelInfo(model models.Model, m *models.Manager) {
	fmt.Fprintf(os.Stdout, "Alias:           %s\n", model.Alias)
	fmt.Fprintf(os.Stdout, "File:            %s\n", model.FileName)
	fmt.Fprintf(os.Stdout, "Path:            %s\n", m.ModelsDir+"/"+model.FileName)
	fmt.Fprintf(os.Stdout, "Size:            %.1f GiB\n", model.SizeGB)
	fmt.Fprintf(os.Stdout, "Recommended RAM: %s\n", model.RecommendedRAM)
	fmt.Fprintf(os.Stdout, "Installed:       %t\n", model.Installed)
	if model.Partial {
		fmt.Fprintf(os.Stdout, "Partial:         true (%s)\n", tui.FormatPartialModel(model.PartialBytes, model.SizeGB))
	}
	fmt.Fprintf(os.Stdout, "Default:         %t\n", model.Default)
	if model.SHA256 != "" {
		fmt.Fprintf(os.Stdout, "SHA256:          %s\n", model.SHA256)
	}
	if flags := tui.ModelFlags(model); flags != "" {
		fmt.Fprintf(os.Stdout, "Flags:           %s\n", flags)
	}
	if model.Notes != "" {
		fmt.Fprintf(os.Stdout, "Notes:           %s\n", model.Notes)
	}
}
