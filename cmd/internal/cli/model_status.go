package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/NimbleMarkets/ds4go/internal/install"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/spf13/cobra"
)

// loadedEngine is one process holding libds4 together with the model files
// it has open, mapped onto catalog aliases and roles.
type loadedEngine struct {
	PID     int                 `json:"pid"`
	Process string              `json:"process"`
	Files   []models.LoadedFile `json:"files"`
}

func newModelStatusCommand() *cobra.Command {
	var asJSON bool
	var sample, watch time.Duration
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show loaded engines and in-progress, stalled, or interrupted downloads",
		Long: "Show which processes have libds4 loaded and the models, MTP/DSpark companions, and\n" +
			"vision encoders they hold open, then every catalog model with partial data on disk:\n" +
			"whether a downloader holds its lock (and which PID), progress, a sampled rate, and an\n" +
			"ETA. Nothing is read from the engine or downloader itself, so this works for any\n" +
			"ds4go process, foreground or background.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return runModelStatus(os.Stdout, modelManager(), sample, watch, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	cmd.Flags().DurationVar(&sample, "sample", 2*time.Second, "how long to sample the transfer rate (0 = no rate or ETA)")
	cmd.Flags().DurationVar(&watch, "watch", 0, "repeat every interval until interrupted (e.g. 10s)")
	return cmd
}

func runModelStatus(w io.Writer, m *models.Manager, sample, watch time.Duration, asJSON bool) error {
	for {
		engines, err := loadedEngines(m)
		if err != nil {
			return err
		}
		list, err := m.DownloadStatus(sample)
		if err != nil {
			return err
		}
		if asJSON {
			if list == nil {
				list = []models.DownloadStatus{}
			}
			if engines == nil {
				engines = []loadedEngine{}
			}
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			if err := enc.Encode(struct {
				Loaded    []loadedEngine          `json:"loaded"`
				Downloads []models.DownloadStatus `json:"downloads"`
			}{engines, list}); err != nil {
				return err
			}
		} else {
			if watch > 0 {
				fmt.Fprintf(w, "%s\n", time.Now().Format(time.TimeOnly))
			}
			renderLoadedEngines(w, engines)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "DOWNLOADS")
			renderDownloadStatus(w, list, m.ModelsDir)
		}
		if watch <= 0 {
			return nil
		}
		time.Sleep(watch)
		if !asJSON {
			fmt.Fprintln(w)
		}
	}
}

// loadedEngines lists processes holding the installed libds4 and the GGUFs
// each has open. Liveness is the process's own open files and the per-model
// run lock, so nothing the engine writes is trusted.
func loadedEngines(m *models.Manager) ([]loadedEngine, error) {
	holders, err := install.FindLibraryHolders(install.DefaultLibraryPath())
	if err != nil {
		return nil, err
	}
	files, _ := install.FindDirHolders(m.ModelsDir)
	out := make([]loadedEngine, 0, len(holders))
	for _, h := range holders {
		out = append(out, loadedEngine{PID: h.PID, Process: h.Name, Files: models.ClassifyLoadedFiles(files[h.PID])})
	}
	return out, nil
}

// renderLoadedEngines prints the loaded-engines section.
func renderLoadedEngines(w io.Writer, engines []loadedEngine) {
	fmt.Fprintln(w, "LOADED")
	if len(engines) == 0 {
		fmt.Fprintln(w, "  No engines loaded")
		return
	}
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  PID\tPROCESS\tMODELS")
	for _, e := range engines {
		var parts []string
		for _, f := range e.Files {
			if f.Alias != "" {
				parts = append(parts, fmt.Sprintf("%s (%s)", f.Alias, f.Role))
			} else {
				parts = append(parts, f.Path)
			}
		}
		desc := "(no model open)"
		if len(parts) > 0 {
			desc = strings.Join(parts, ", ")
		}
		fmt.Fprintf(tw, "  %d\t%s\t%s\n", e.PID, e.Process, desc)
	}
	tw.Flush()
}

// renderDownloadStatus prints the downloads table.
func renderDownloadStatus(w io.Writer, list []models.DownloadStatus, dir string) {
	if len(list) == 0 {
		fmt.Fprintf(w, "No partial downloads in %s\n", dir)
		return
	}
	tw := tabwriter.NewWriter(w, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tSTATE\tPROGRESS\tRATE\tETA\tPID\tIDLE")
	for _, s := range list {
		progress := models.FormatBytes(s.Bytes)
		if s.Total > 0 {
			progress = fmt.Sprintf("%s / %s (%.1f%%)", models.FormatBytes(s.Bytes), models.FormatBytes(s.Total), 100*float64(s.Bytes)/float64(s.Total))
		}
		rate, eta, pid := "-", "-", "-"
		if s.BytesPerSec > 0 {
			rate = models.FormatBytes(int64(s.BytesPerSec)) + "/s"
		}
		if s.ETA > 0 {
			eta = s.ETA.Round(time.Minute).String()
		}
		if s.PID != 0 {
			pid = fmt.Sprint(s.PID)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Alias, s.State, progress, rate, eta, pid, s.Idle.Round(time.Second))
	}
	tw.Flush()
}
