package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/spf13/cobra"
)

func newModelStatusCommand() *cobra.Command {
	var asJSON bool
	var sample, watch time.Duration
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show in-progress, stalled, and interrupted model downloads",
		Long: "Show every catalog model with partial data on disk: whether a downloader holds its\n" +
			"lock (and which PID), how far along it is, the rate over a short sample, and an ETA.\n" +
			"Nothing is read from the downloader itself, so this works for a download started by\n" +
			"any ds4go process, foreground or background.",
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
		list, err := m.DownloadStatus(sample)
		if err != nil {
			return err
		}
		if asJSON {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			if list == nil {
				list = []models.DownloadStatus{}
			}
			if err := enc.Encode(list); err != nil {
				return err
			}
		} else {
			if watch > 0 {
				fmt.Fprintf(w, "%s\n", time.Now().Format(time.TimeOnly))
			}
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

// renderDownloadStatus prints the status table.
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
