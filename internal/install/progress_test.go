package install

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func fakeTerminal(width *int) func(io.Writer) (int, bool) {
	return func(io.Writer) (int, bool) { return *width, true }
}

// visibleFrames splits progress output into redraws with the erase sequence
// removed, so widths measure the text alone.
func visibleFrames(out string) []string {
	var frames []string
	for _, f := range strings.Split(out, "\r") {
		f = strings.TrimSuffix(f, ansi.EraseScreenBelow)
		if f != "" {
			frames = append(frames, f)
		}
	}
	return frames
}

// A frame wider than the terminal wraps, and the redraw then rewinds only the
// last physical row, so the display repeats instead of updating in place. The
// frame must stay under the terminal width.
func TestInstallProgressFitsTerminalWidth(t *testing.T) {
	var buf bytes.Buffer
	width := 80
	p := newDownloadProgress(&buf, "libds4-metal-arm64.tar.gz", 211075856448)
	p.term.Size = fakeTerminal(&width)
	p.downloaded = 361299968
	p.render(true)

	for _, frame := range visibleFrames(buf.String()) {
		if w := ansi.StringWidth(frame); w >= width {
			t.Errorf("frame visible width = %d, want < %d", w, width)
		}
	}
}

// The previous frame's tail is cleared by erasing, not by padding to the
// terminal width: a padded row reflows into two rows when the terminal
// shrinks and the head is orphaned above every later redraw.
func TestInstallProgressErasesInsteadOfPadding(t *testing.T) {
	var buf bytes.Buffer
	width := 80
	p := newDownloadProgress(&buf, "asset.tar.gz", 1000)
	p.term.Size = fakeTerminal(&width)
	buf.Reset()
	p.downloaded = 1000
	p.render(true)

	out := buf.String()
	if !strings.HasSuffix(out, ansi.EraseScreenBelow+"\r") {
		t.Fatalf("frame does not erase below and park the cursor: %q", out)
	}
	frames := visibleFrames(out)
	if last := frames[len(frames)-1]; strings.HasSuffix(last, " ") {
		t.Fatalf("frame is padded with spaces: %q", last)
	}
}

// The width is re-read for every frame, so a terminal that shrank mid-download
// gets a frame that fits.
func TestInstallProgressFollowsResize(t *testing.T) {
	var buf bytes.Buffer
	width := 120
	p := newDownloadProgress(&buf, "libds4-v0.5.20260910-macos-arm64-metal.tar.gz", 211075856448)
	p.term.Size = fakeTerminal(&width)
	p.downloaded = 361299968
	p.render(true)
	width = 40
	p.downloaded = 361299968 * 2
	p.render(true)

	frames := visibleFrames(buf.String())
	last := frames[len(frames)-1]
	if w := ansi.StringWidth(last); w >= 40 {
		t.Errorf("frame after shrink has width %d, want < 40: %q", w, last)
	}
}
