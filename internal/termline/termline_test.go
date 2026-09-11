package termline

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func terminalOf(width int) func(io.Writer) (int, bool) {
	return func(io.Writer) (int, bool) { return width, true }
}

func notATerminal(io.Writer) (int, bool) { return 0, false }

// A frame is redrawn in place without space padding: the previous frame's
// tail is removed by erasing to the end of the screen, and the cursor is
// parked at column 0 so a terminal that reflows on resize keeps it with the
// start of the row. Then the next frame overwrites the head and the erase
// clears any wrapped remainder below it.
func TestDrawErasesAndParksCursor(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Size = terminalOf(80)
	l.Draw("Downloading: model.gguf 10%")
	want := "\rDownloading: model.gguf 10%" + ansi.EraseScreenBelow + "\r"
	if got := buf.String(); got != want {
		t.Fatalf("Draw wrote %q, want %q", got, want)
	}
	if strings.HasSuffix(strings.TrimSuffix(strings.TrimSuffix(buf.String(), "\r"), ansi.EraseScreenBelow), " ") {
		t.Fatal("frame was padded with spaces")
	}
}

// A frame must stay strictly narrower than the terminal so it never reaches
// the wrap column, whatever the emulator's deferred-wrap behaviour is.
func TestDrawTruncatesToWidthMinusOne(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Size = terminalOf(40)
	l.Draw(strings.Repeat("x", 100))
	frame := strings.TrimSuffix(strings.TrimPrefix(buf.String(), "\r"), ansi.EraseScreenBelow+"\r")
	if w := ansi.StringWidth(frame); w != 39 {
		t.Fatalf("frame width = %d, want 39 (columns-1): %q", w, frame)
	}
	if !strings.HasSuffix(frame, "…") {
		t.Fatalf("truncated frame lacks ellipsis: %q", frame)
	}
	// ANSI styling does not count toward the width.
	buf.Reset()
	l.Draw("\x1b[1m" + strings.Repeat("y", 30) + "\x1b[0m")
	frame = strings.TrimSuffix(strings.TrimPrefix(buf.String(), "\r"), ansi.EraseScreenBelow+"\r")
	if w := ansi.StringWidth(frame); w != 30 {
		t.Fatalf("styled frame width = %d, want 30: %q", w, frame)
	}
}

// The width is re-read for every frame, so a terminal that shrank between
// frames gets a frame that fits the new width.
func TestDrawFollowsResize(t *testing.T) {
	var buf bytes.Buffer
	width := 120
	l := New(&buf)
	l.Size = func(io.Writer) (int, bool) { return width, true }
	long := strings.Repeat("a", 110)
	l.Draw(long)
	width = 60
	l.Draw(long)
	frames := strings.Split(buf.String(), "\r")
	var visible []string
	for _, f := range frames {
		if f != "" {
			visible = append(visible, strings.TrimSuffix(f, ansi.EraseScreenBelow))
		}
	}
	if len(visible) != 2 {
		t.Fatalf("got %d frames, want 2: %q", len(visible), buf.String())
	}
	if w := ansi.StringWidth(visible[0]); w != 110 {
		t.Errorf("first frame width = %d, want 110", w)
	}
	if w := ansi.StringWidth(visible[1]); w != 59 {
		t.Errorf("second frame width after shrink = %d, want 59", w)
	}
}

// Redirected output has no cursor: no escape sequences, just the text and a
// carriage return so a log shows the frame on its own line.
func TestDrawOnNonTerminalWritesPlainText(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Size = notATerminal
	l.Draw("Downloading: model.gguf 10%")
	if got, want := buf.String(), "\rDownloading: model.gguf 10%"; got != want {
		t.Fatalf("Draw wrote %q, want %q", got, want)
	}
	if l.IsTerminal() {
		t.Fatal("IsTerminal() = true for a non-terminal writer")
	}
}

func TestFinishEndsTheLine(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Size = terminalOf(80)
	l.Draw("done")
	l.Finish()
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("Finish did not end the line: %q", buf.String())
	}
}

// Width resolution: the real terminal size, then COLUMNS, then 80.
func TestColumnsResolution(t *testing.T) {
	l := New(io.Discard)

	l.Size = terminalOf(132)
	t.Setenv("COLUMNS", "100")
	if got := l.Columns(); got != 132 {
		t.Errorf("terminal width: Columns() = %d, want 132", got)
	}

	l.Size = notATerminal
	if got := l.Columns(); got != 100 {
		t.Errorf("COLUMNS fallback: Columns() = %d, want 100", got)
	}

	t.Setenv("COLUMNS", "")
	if got := l.Columns(); got != DefaultColumns {
		t.Errorf("default: Columns() = %d, want %d", got, DefaultColumns)
	}

	// Implausibly narrow reports fall back too, so a frame is never squeezed
	// to nothing during a resize.
	l.Size = terminalOf(5)
	if got := l.Columns(); got != DefaultColumns {
		t.Errorf("narrow terminal: Columns() = %d, want %d", got, DefaultColumns)
	}
}

// The real size probe reports a non-terminal for a bytes.Buffer.
func TestDefaultSizeProbeOnBuffer(t *testing.T) {
	l := New(new(bytes.Buffer))
	if l.IsTerminal() {
		t.Fatal("bytes.Buffer reported as a terminal")
	}
}
