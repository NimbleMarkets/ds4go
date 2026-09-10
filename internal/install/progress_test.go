package install

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The installer redraws with a bare "\r", so a frame wider than the terminal
// wraps and the redraw rewinds only the last physical row -- the display then
// repeats instead of updating in place. Padding also uses len(msg), which
// counts ANSI escape bytes rather than display columns, so it never clears the
// previous frame correctly.
func TestInstallProgressFitsTerminalWidth(t *testing.T) {
	var buf bytes.Buffer
	p := newDownloadProgress(&buf, "libds4-metal-arm64.tar.gz", 211075856448)
	p.downloaded = 361299968
	p.render(true)

	for _, frame := range strings.Split(buf.String(), "\r") {
		if frame == "" {
			continue
		}
		if w := ansi.StringWidth(frame); w > defaultProgressColumns {
			t.Errorf("frame visible width = %d, want <= %d", w, defaultProgressColumns)
		}
	}
}

// Padding must be measured in display columns so a shorter frame fully clears
// the longer one before it.
func TestInstallProgressPadsByDisplayWidth(t *testing.T) {
	var buf bytes.Buffer
	p := newDownloadProgress(&buf, "asset.tar.gz", 1000)
	p.downloaded = 1000
	p.render(true)

	frames := strings.Split(buf.String(), "\r")
	last := frames[len(frames)-1]
	if w := ansi.StringWidth(last); w != defaultProgressColumns {
		t.Errorf("padded frame width = %d, want exactly %d", w, defaultProgressColumns)
	}
}
