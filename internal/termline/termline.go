// Package termline redraws one status line in place on a terminal.
//
// A progress line that pads itself to the terminal width and rewinds with a
// bare "\r" breaks as soon as the terminal shrinks: macOS terminals reflow the
// full-width row into two physical rows, "\r" rewinds only the last of them,
// and the head of the old frame is left orphaned above every later redraw.
//
// Line avoids that by never padding: each frame is written, the rest of the
// screen below the cursor is erased, and the cursor is parked at column 0. A
// reflowing terminal keeps a parked cursor with the start of the row's
// content, so the next frame overwrites the head of the old one and the erase
// removes any wrapped remainder. Frames are also kept one column short of the
// width so they never reach the wrap column. Both the installer and the model
// downloader draw through this type; a richer view can replace it later.
package termline

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// DefaultColumns is used when the output is not a measurable terminal and
// COLUMNS is unset. 80 is the conservative choice: a frame narrower than the
// real terminal redraws correctly, a wider one wraps.
const DefaultColumns = 80

// minColumns rejects implausible size reports (a window mid-resize can report
// a few columns) so a frame is never squeezed to nothing.
const minColumns = 20

// SizeFunc reports a writer's width in columns and whether it is a terminal
// at all. A zero width with true means a terminal of unknown size.
type SizeFunc func(w io.Writer) (width int, isTerminal bool)

// TerminalSize is the default SizeFunc: it probes *os.File writers.
func TerminalSize(w io.Writer) (int, bool) {
	file, ok := w.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return 0, false
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width <= 0 {
		return 0, true
	}
	return width, true
}

// Line draws successive frames of one status line to a writer.
type Line struct {
	out io.Writer
	// Size probes the writer's width and terminal-ness for every frame, so
	// resizes are followed. Tests override it; it defaults to TerminalSize.
	Size SizeFunc
}

// New returns a Line drawing to out.
func New(out io.Writer) *Line {
	return &Line{out: out, Size: TerminalSize}
}

// IsTerminal reports whether the writer has a cursor to rewind.
func (l *Line) IsTerminal() bool {
	_, isTerm := l.Size(l.out)
	return isTerm
}

// Columns reports the width to render at: the real terminal size, then an
// explicit COLUMNS, then DefaultColumns. It is re-read for every frame.
func (l *Line) Columns() int {
	if width, isTerm := l.Size(l.out); isTerm && width >= minColumns {
		return width
	}
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols >= minColumns {
		return cols
	}
	return DefaultColumns
}

// Draw redraws the line as frame. On a terminal the frame is truncated to
// Columns()-1 visible cells (with an ellipsis), written after a carriage
// return, followed by an erase to the end of the screen, and the cursor is
// parked at column 0. On a non-terminal only the carriage return and the
// text are written, so a log shows the frame on its own line; callers gate
// how often they draw there.
func (l *Line) Draw(frame string) {
	if !l.IsTerminal() {
		fmt.Fprintf(l.out, "\r%s", frame)
		return
	}
	limit := l.Columns() - 1
	if ansi.StringWidth(frame) > limit {
		frame = ansi.Truncate(frame, limit, "…")
	}
	fmt.Fprintf(l.out, "\r%s%s\r", frame, ansi.EraseScreenBelow)
}

// Finish ends the line after the last frame.
func (l *Line) Finish() {
	fmt.Fprintln(l.out)
}
