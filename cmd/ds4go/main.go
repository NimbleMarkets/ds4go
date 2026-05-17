// Command ds4go is a pure-Go CLI for the ds4 inference engine.
//
// It accepts the same arguments as the upstream `ds4` CLI (ds4_cli.c); see
// --help. The only addition is --lib, which points at the libds4 shared
// library the wrapper loads at runtime.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
	"github.com/NimbleMarkets/ds4go/internal/install"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type cliMessage struct {
	role    string
	content string
}

func main() {
	root := newRootCommand()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
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
	root.SilenceUsage = true
	root.AddCommand(newPromptCommand(), newInstallCommand(), newModelCommand())
	root.SetHelpCommand(newHelpCommand(root))
	return root
}

func newPromptCommand() *cobra.Command {
	fs := pflag.NewFlagSet("prompt", pflag.ContinueOnError)
	cfg := cliopts.RegisterCLI(fs)
	cmd := &cobra.Command{
		Use:   "prompt [(-p PROMPT | --prompt-file FILE)] [options]",
		Short: "Run prompt or interactive chat inference",
		Long:  "Run ds4 inference. With no prompt, starts an interactive chat (ds4>).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cfg)
		},
	}
	cmd.Flags().AddFlagSet(fs)
	return cmd
}

func newInstallCommand() *cobra.Command {
	var opts install.Options
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Download a prebuilt libds4 shared library",
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Token == "" {
				opts.Token = os.Getenv("GITHUB_TOKEN")
			}
			opts.Out = os.Stdout
			opts.ProgressOut = os.Stderr
			_, err := install.Run(cmd.Context(), opts)
			return err
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&opts.DestDir, "lib", "", "directory where libds4 will be installed (default $DS4_DIR/lib or ~/.ds4/lib)")
	fs.StringVar(&opts.Repo, "repo", install.DefaultRepo, "GitHub repo that publishes libds4 releases")
	fs.StringVar(&opts.Version, "version", "latest", "release tag to install, or latest")
	fs.StringVar(&opts.Backend, "backend", "auto", "backend build to install: auto, metal, cuda, or cpu")
	fs.StringVar(&opts.GOOS, "os", "", "target operating system (default current)")
	fs.StringVar(&opts.GOARCH, "arch", "", "target architecture (default current)")
	fs.StringVar(&opts.Asset, "asset", "", "exact release asset name to download")
	fs.StringVar(&opts.URL, "url", "", "direct archive URL instead of GitHub release lookup")
	fs.StringVar(&opts.Token, "token", "", "GitHub token for private repos or higher rate limits (defaults to GITHUB_TOKEN)")
	fs.BoolVar(&opts.Force, "force", false, "replace an existing libds4 file")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "print the selected asset without downloading it")
	fs.BoolVar(&opts.SkipChecksum, "skip-checksum", false, "skip GitHub API digest verification of the download")
	return cmd
}

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
	printModelGroup("Installed", list, true)
	fmt.Fprintln(os.Stdout)
	printModelGroup("Available to download", list, false)
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
		alias, err = pickModelAlias("Select a model for details", list, os.Stdin, os.Stderr)
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
		alias, err = pickModelAlias("Select installed default model", list, os.Stdin, os.Stderr)
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
		alias, err = pickModelAlias("Select a model to download", list, os.Stdin, os.Stderr)
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
		alias, err = pickModelAlias("Select a model to delete", removable, os.Stdin, os.Stderr)
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
		if !confirm(os.Stdin, os.Stdout, "Are you sure?") {
			fmt.Fprintln(os.Stdout, "Cancelled")
			return nil
		}
	}
	return m.Delete(alias)
}

// confirm prints prompt and returns true only if the user answers yes.
func confirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "y", "yes":
		return true
	default:
		return false
	}
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
		if model.Installed && !model.Optional {
			out = append(out, model)
		}
	}
	if len(out) == 0 {
		for _, model := range list {
			if !model.Optional {
				out = append(out, model)
			}
		}
	}
	return out
}

func printModelGroup(title string, list []models.Model, installed bool) {
	fmt.Fprintf(os.Stdout, "%s:\n", title)
	any := false

	aliasStyle := lipgloss.NewStyle().Width(14)
	sizeStyle := lipgloss.NewStyle().Width(10).Align(lipgloss.Right)
	ramStyle := lipgloss.NewStyle().Width(12)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#7D8590"))
	success := lipgloss.NewStyle().Foreground(lipgloss.Color("#39FFB6"))

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
			partial = muted.Render(" partial " + formatPartialModel(model.PartialBytes, model.SizeGB))
		}
		flags := modelFlags(model)
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
		fmt.Fprintf(os.Stdout, "Partial:         true (%s)\n", formatPartialModel(model.PartialBytes, model.SizeGB))
	}
	fmt.Fprintf(os.Stdout, "Default:         %t\n", model.Default)
	if model.SHA256 != "" {
		fmt.Fprintf(os.Stdout, "SHA256:          %s\n", model.SHA256)
	}
	if flags := modelFlags(model); flags != "" {
		fmt.Fprintf(os.Stdout, "Flags:           %s\n", flags)
	}
	if model.Notes != "" {
		fmt.Fprintf(os.Stdout, "Notes:           %s\n", model.Notes)
	}
}

func formatModelBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for next := div * unit; n >= next && exp < 4; next *= unit {
		div = next
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func formatPartialModel(partialBytes int64, sizeGiB float64) string {
	if sizeGiB <= 0 {
		return formatModelBytes(partialBytes)
	}
	currentGiB := float64(partialBytes) / (1024 * 1024 * 1024)
	pct := currentGiB / sizeGiB * 100
	if pct > 999 {
		pct = 999
	}
	return fmt.Sprintf("%.1f / ~%.1f GiB %.1f%%", currentGiB, sizeGiB, pct)
}

func modelFlags(model models.Model) string {
	var flags []string
	if model.Imatrix {
		flags = append(flags, "imatrix")
	}
	if model.Legacy {
		flags = append(flags, "legacy")
	}
	if model.Optional {
		flags = append(flags, "mtp")
	}
	return strings.Join(flags, ", ")
}

func run(cfg *cliopts.CLIConfig) error {
	if err := preflightPromptModel(cfg.Model); err != nil {
		return err
	}
	var engine *ds4.Engine
	var err error
	if cfg.Lib != "" {
		lib, err := ds4.Load(cfg.Lib)
		if err != nil {
			return err
		}
		ds4.SetDefaultLibrary(lib)
		engine, err = lib.NewEngine(cfg.EngineOptions())
	} else {
		engine, err = ds4.NewEngine(cfg.EngineOptions())
	}
	if err != nil {
		return ds4.EnrichEngineOpenError(err)
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

func preflightPromptModel(path string) error {
	if path == "" {
		return fmt.Errorf("no model path configured; run: ds4go model download %s", models.RecommendedModelAlias)
	}
	st, err := os.Stat(path)
	if err == nil && !st.IsDir() && st.Size() > 0 {
		return nil
	}
	if err == nil && st.IsDir() {
		return fmt.Errorf("model path is a directory: %s", path)
	}

	defaultPath := models.DefaultModelPath()
	if path == defaultPath {
		m := modelManager()
		list, cfg, listErr := m.List()
		if listErr != nil {
			return fmt.Errorf("model is not ready at %s; additionally failed to inspect model config: %w", path, listErr)
		}
		if cfg.DefaultModel == "" || activeDefault(list) == "" {
			return fmt.Errorf("no default model is installed at %s\nRun: ds4go model download %s\nOr:  ds4go model list", path, models.RecommendedModelAlias)
		}
		return fmt.Errorf("configured default model %q is not available at %s\nRun: ds4go model download %s\nOr:  ds4go model set <installed-alias>", cfg.DefaultModel, path, cfg.DefaultModel)
	}
	return fmt.Errorf("model file not found: %s\nUse --model PATH or run: ds4go model download %s", path, models.RecommendedModelAlias)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	opts := cfg.GenerateOptions()
	opts.Context = ctx
	opts.OnToken = func(token int) {
		if text, err := engine.TokenText(token); err == nil {
			fmt.Print(text)
		}
	}
	_, err = (ds4.Generator{Engine: engine, Session: session}).GenerateTokens(tokens, opts)
	fmt.Println()
	if err == context.Canceled {
		return nil
	}
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

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		opts := cfg.GenerateOptions()
		opts.Context = ctx
		var response strings.Builder
		opts.OnToken = func(token int) {
			if text, err := engine.TokenText(token); err == nil {
				response.WriteString(text)
				fmt.Print(text)
			}
		}
		_, err = (ds4.Generator{Engine: engine, Session: session}).GenerateTokens(prompt, opts)
		stop()
		prompt.Free()
		fmt.Println()
		if err != nil && err != context.Canceled {
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
