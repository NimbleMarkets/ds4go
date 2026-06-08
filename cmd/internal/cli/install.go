package cli

import (
	"os"

	"github.com/NimbleMarkets/ds4go/cmd/internal/tui"
	"github.com/NimbleMarkets/ds4go/internal/install"
	"github.com/spf13/cobra"
)

func newInstallCommand() *cobra.Command {
	var opts install.Options
	cmd := &cobra.Command{
		Use:   "install [options]",
		Short: "Download a prebuilt libds4 shared library",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			if opts.Token == "" {
				opts.Token = os.Getenv("GITHUB_TOKEN")
			}
			opts.Out = os.Stdout
			opts.ProgressOut = os.Stderr
			opts.In = os.Stdin
			opts.Confirm = func(prompt string, defaultYes bool) (bool, error) {
				res, err := tui.Confirm(prompt, defaultYes, opts.In, opts.Out)
				return res == tui.ConfirmYes, err
			}
			_, err := install.Run(cmd.Context(), opts)
			return err
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&opts.DestDir, "lib", "", "directory where libds4 will be installed (default $DS4_DIR/lib or ~/.ds4/lib)")
	fs.StringVar(&opts.Repo, "repo", install.DefaultRepo, "GitHub repo that publishes libds4 releases")
	fs.StringVar(&opts.Version, "version", "latest", "release tag to install, or latest")
	fs.StringVar(&opts.Backend, "backend", "auto", "backend build to install: auto, metal, cuda, rocm, or cpu")
	fs.StringVar(&opts.GOOS, "os", "", "target operating system (default current)")
	fs.StringVar(&opts.GOARCH, "arch", "", "target architecture (default current)")
	fs.StringVar(&opts.Asset, "asset", "", "exact release asset name to download")
	fs.StringVar(&opts.URL, "url", "", "direct archive URL instead of GitHub release lookup")
	fs.StringVar(&opts.Pin, "pin", "", "install a developer-supplied libds4 from this local file and mark it pinned")
	fs.StringVar(&opts.Token, "token", "", "GitHub token for private repos or higher rate limits (defaults to GITHUB_TOKEN)")
	fs.BoolVar(&opts.Force, "force", false, "replace an existing libds4 file")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "print the selected asset without downloading it")
	fs.BoolVar(&opts.SkipChecksum, "skip-checksum", false, "skip GitHub API digest verification of the download")
	return cmd
}

func newValidateCommand() *cobra.Command {
	var opts install.Options
	cmd := &cobra.Command{
		Use:   "validate [options]",
		Short: "Validate the installed libds4 shared library",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			opts.Out = os.Stdout
			opts.ProgressOut = os.Stderr
			opts.In = os.Stdin
			return install.Validate(cmd.Context(), opts)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&opts.DestDir, "lib", "", "directory where libds4 is installed (default $DS4_DIR/lib or ~/.ds4/lib)")
	fs.StringVar(&opts.GOOS, "os", "", "target operating system (default current)")
	fs.StringVar(&opts.GOARCH, "arch", "", "target architecture (default current)")
	return cmd
}

func newStatusCommand() *cobra.Command {
	var opts install.Options
	cmd := &cobra.Command{
		Use:     "status [options]",
		Aliases: []string{"usage", "processes", "locks", "holders"},
		Short:   "Find processes holding or using the libds4 shared library",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			opts.Out = os.Stdout
			opts.ProgressOut = os.Stderr
			opts.In = os.Stdin
			return install.Status(cmd.Context(), opts)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&opts.DestDir, "lib", "", "directory where libds4 is installed (default $DS4_DIR/lib or ~/.ds4/lib)")
	fs.StringVar(&opts.GOOS, "os", "", "target operating system (default current)")
	fs.StringVar(&opts.GOARCH, "arch", "", "target architecture (default current)")
	return cmd
}

func newUninstallCommand() *cobra.Command {
	var opts install.Options
	cmd := &cobra.Command{
		Use:   "uninstall [options]",
		Short: "Uninstall the installed libds4 shared library",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			opts.Out = os.Stdout
			opts.ProgressOut = os.Stderr
			opts.In = os.Stdin
			opts.Confirm = func(prompt string, defaultYes bool) (bool, error) {
				res, err := tui.Confirm(prompt, defaultYes, opts.In, opts.Out)
				return res == tui.ConfirmYes, err
			}
			return install.Uninstall(cmd.Context(), opts)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&opts.DestDir, "lib", "", "directory where libds4 is installed (default $DS4_DIR/lib or ~/.ds4/lib)")
	fs.StringVar(&opts.GOOS, "os", "", "target operating system (default current)")
	fs.StringVar(&opts.GOARCH, "arch", "", "target architecture (default current)")
	fs.BoolVar(&opts.Force, "force", false, "uninstall without confirmation prompt")
	return cmd
}
