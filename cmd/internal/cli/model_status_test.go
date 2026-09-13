package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

func TestRenderDownloadStatusTable(t *testing.T) {
	var buf bytes.Buffer
	renderDownloadStatus(&buf, []models.DownloadStatus{
		{Alias: "v41-q2", State: models.DownloadRunning, PID: 4242, Bytes: 120 << 30, Total: 340 << 30, BytesPerSec: 5 << 20, ETA: 12 * time.Hour, Idle: 2 * time.Second},
		{Alias: "vision-encoder", State: models.DownloadInterrupted, Bytes: 200 << 20, Total: 900 << 20, Idle: 90 * time.Minute},
	}, "/m")
	out := buf.String()
	for _, want := range []string{"ALIAS", "v41-q2", "downloading", "120.0 GiB / 340.0 GiB (35.3%)", "5.0 MiB/s", "12h0m0s", "4242", "vision-encoder", "interrupted", "1h30m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	buf.Reset()
	renderDownloadStatus(&buf, nil, "/m")
	if !strings.Contains(buf.String(), "No partial downloads in /m") {
		t.Errorf("empty table = %q", buf.String())
	}
}
