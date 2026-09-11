package tui

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestProgressReaderLineIncludesStyledNameAndSpeed(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	p := newProgressReader(io.Discard, "DeepSeek-V4-Flash-IQ2XXS.gguf", 512, 1024, io.NopCloser(strings.NewReader("")))
	p.initial = 0
	p.started = time.Now().Add(-time.Second)
	line := p.line()
	visible := stripANSI(line)
	if !strings.Contains(visible, "DeepSeek-V4-Flash-IQ2XXS.gguf") {
		t.Fatalf("line = %q, want full filename", line)
	}
	if !strings.Contains(line, "50.0%") {
		t.Fatalf("line = %q, want percentage", line)
	}
	if !strings.Contains(line, "(512 B / 1.0 KiB) ") || !strings.Contains(line, " B/s") {
		t.Fatalf("line = %q, want speed after size", line)
	}
	if !strings.Contains(line, "\x1b[") {
		t.Fatalf("line = %q, want ANSI background styling", line)
	}
}

func TestProgressReaderShortensOnlyToFit(t *testing.T) {
	t.Setenv("COLUMNS", "72")
	name := "DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix.gguf"
	p := newProgressReader(io.Discard, name, 512, 1024, io.NopCloser(strings.NewReader("")))
	p.started = time.Now().Add(-time.Second)
	line := p.line()
	if lipgloss.Width(line) > 72 {
		t.Fatalf("visible width = %d, want <= 72: %q", lipgloss.Width(line), line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("line = %q, want shortened filename", line)
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// The progress line is redrawn with a bare "\r", which only rewinds the
// current physical row. A line wider than the terminal wraps, so every redraw
// leaves the wrapped remainder on screen and the display repeats instead of
// updating in place. The rendered frame must therefore never exceed the
// terminal width.
func TestProgressLineFitsTerminalWidth(t *testing.T) {
	defer restoreTerminalSize(t)
	terminalSizeFunc = func(io.Writer) (int, bool) { return 80, true }
	t.Setenv("COLUMNS", "")

	var buf bytes.Buffer
	p := newProgressReader(&buf, "GLM-5.2-UD-IQ2_XXS_RoutedIQ2XXS_blk78Q2K.gguf",
		361299968, 211075856448, io.NopCloser(strings.NewReader("")))
	p.render(true)

	for _, frame := range renderedFrames(buf.String()) {
		if w := lipgloss.Width(frame); w > 80 {
			t.Errorf("rendered frame width = %d, want <= 80 (wraps and repeats above that)", w)
		}
	}
}

// With no terminal and no COLUMNS, assume a conservative 80 columns rather
// than a width most terminals do not have.
func TestProgressWidthFallsBackToEightyColumns(t *testing.T) {
	defer restoreTerminalSize(t)
	terminalSizeFunc = func(io.Writer) (int, bool) { return 0, false }
	t.Setenv("COLUMNS", "")

	var buf bytes.Buffer
	p := newProgressReader(&buf, "model.gguf", 1, 100, io.NopCloser(strings.NewReader("")))
	if got := p.columns(); got != 80 {
		t.Errorf("columns() = %d, want 80", got)
	}
}

// An explicit COLUMNS still wins when there is no measurable terminal, so
// callers can size output in pipelines.
func TestProgressWidthUsesColumnsEnv(t *testing.T) {
	defer restoreTerminalSize(t)
	terminalSizeFunc = func(io.Writer) (int, bool) { return 0, false }
	t.Setenv("COLUMNS", "100")

	var buf bytes.Buffer
	p := newProgressReader(&buf, "model.gguf", 1, 100, io.NopCloser(strings.NewReader("")))
	if got := p.columns(); got != 100 {
		t.Errorf("columns() = %d, want 100", got)
	}
}

// Redirected output has no cursor to rewind, so "\r" frames just accumulate in
// the log. Only the first and final frames belong there.
func TestProgressDoesNotSpamWhenNotATerminal(t *testing.T) {
	defer restoreTerminalSize(t)
	terminalSizeFunc = func(io.Writer) (int, bool) { return 0, false }

	var buf bytes.Buffer
	p := newProgressReader(&buf, "model.gguf", 0, 1000, io.NopCloser(strings.NewReader("")))
	for i := 0; i < 50; i++ {
		p.current += 20
		p.lastRender = time.Time{} // defeat the rate limiter
		p.render(false)
	}
	if n := strings.Count(buf.String(), "\r"); n > 1 {
		t.Errorf("wrote %d carriage-return frames to a non-terminal, want at most 1", n)
	}
}

func restoreTerminalSize(t *testing.T) {
	t.Helper()
	original := terminalSizeFunc
	t.Cleanup(func() { terminalSizeFunc = original })
}

// renderedFrames splits progress output into its individual redraws, with
// the erase sequence removed so widths measure the text alone.
func renderedFrames(out string) []string {
	var frames []string
	for _, f := range strings.Split(out, "\r") {
		f = strings.TrimSuffix(f, ansi.EraseScreenBelow)
		if f != "" {
			frames = append(frames, f)
		}
	}
	return frames
}

// The previous frame's tail is cleared by erasing to the end of the screen,
// and the cursor is parked at column 0, rather than padding to the width: a
// padded row reflows into two rows when the terminal shrinks, and "\r" then
// rewinds only the last of them, orphaning the head above every redraw.
func TestProgressRedrawErasesAndParksCursor(t *testing.T) {
	defer restoreTerminalSize(t)
	terminalSizeFunc = func(io.Writer) (int, bool) { return 80, true }

	var buf bytes.Buffer
	p := newProgressReader(&buf, "model.gguf", 500, 1000, io.NopCloser(strings.NewReader("")))
	buf.Reset()
	p.render(true)

	out := buf.String()
	if !strings.HasSuffix(out, ansi.EraseScreenBelow+"\r") {
		t.Fatalf("frame does not erase below and park the cursor: %q", out)
	}
	frames := renderedFrames(out)
	if last := frames[len(frames)-1]; strings.HasSuffix(last, " ") {
		t.Fatalf("frame is padded with spaces: %q", last)
	}
}

// Width is re-read per frame, so a shrink between frames yields a frame that
// fits the new width, sized consistently for name and padding alike.
func TestProgressFollowsResize(t *testing.T) {
	defer restoreTerminalSize(t)
	width := 140
	terminalSizeFunc = func(io.Writer) (int, bool) { return width, true }
	t.Setenv("COLUMNS", "")

	var buf bytes.Buffer
	p := newProgressReader(&buf, "GLM-5.3-Flash-Q2.gguf", 28*1024*1024*1024, 90*1024*1024*1024,
		io.NopCloser(strings.NewReader("")))
	p.render(true)
	width = 100
	p.current += 1024 * 1024 * 1024
	p.render(true)

	frames := renderedFrames(buf.String())
	last := frames[len(frames)-1]
	if w := lipgloss.Width(last); w >= 100 {
		t.Errorf("frame after shrink has width %d, want < 100: %q", w, last)
	}
	if !strings.Contains(stripANSI(last), "GLM-5.3-Flash-Q2.gguf") {
		t.Errorf("frame after shrink lost the filename: %q", stripANSI(last))
	}

	// Narrower still: the name is shortened rather than the row wrapping.
	width = 60
	p.current += 1024 * 1024 * 1024
	p.render(true)
	frames = renderedFrames(buf.String())
	last = frames[len(frames)-1]
	if w := lipgloss.Width(last); w >= 60 {
		t.Errorf("frame at 60 columns has width %d, want < 60: %q", w, last)
	}
	if !strings.Contains(last, "…") {
		t.Errorf("frame at 60 columns was not shortened: %q", stripANSI(last))
	}
}

// fakeClock drives a progressReader's notion of time.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func gib(n float64) int64 { return int64(n * 1024 * 1024 * 1024) }
func mib(n float64) int64 { return int64(n * 1024 * 1024) }

// Speed reflects the last few seconds, not the average since the download
// started: half an hour into a resumed download the lifetime average is
// frozen and tells the user nothing about what the network is doing now.
func TestProgressSpeedUsesMovingWindow(t *testing.T) {
	t.Setenv("COLUMNS", "140")
	clock := &fakeClock{t: time.Now()}
	p := newProgressReader(io.Discard, "model.gguf", gib(28), gib(90), io.NopCloser(strings.NewReader("")))
	p.now = clock.now
	p.started = clock.t.Add(-30 * time.Minute)
	p.initial = gib(28) - mib(9.5)*1800 // lifetime average 9.5 MiB/s

	// The last five seconds ran at 2 MiB/s.
	for i := 0; i < 50; i++ {
		clock.advance(100 * time.Millisecond)
		p.current += mib(0.2)
		p.observe()
	}
	line := stripANSI(p.line())
	if !strings.Contains(line, " 2.0 MiB/s") {
		t.Fatalf("line = %q, want the recent 2.0 MiB/s rather than the lifetime average", line)
	}

	// With no recent samples the lifetime average is the fallback.
	q := newProgressReader(io.Discard, "model.gguf", 0, gib(90), io.NopCloser(strings.NewReader("")))
	q.now = clock.now
	q.started = clock.t.Add(-10 * time.Second)
	q.current = mib(95)
	if line := stripANSI(q.line()); !strings.Contains(line, " 9.5 MiB/s") {
		t.Fatalf("line = %q, want lifetime average 9.5 MiB/s without samples", line)
	}
}

// Old samples fall out of the window, so a stall shows up as a falling rate.
func TestProgressSpeedWindowForgetsOldSamples(t *testing.T) {
	t.Setenv("COLUMNS", "140")
	clock := &fakeClock{t: time.Now()}
	p := newProgressReader(io.Discard, "model.gguf", 0, gib(90), io.NopCloser(strings.NewReader("")))
	p.now = clock.now
	p.started = clock.t
	for i := 0; i < 50; i++ {
		clock.advance(100 * time.Millisecond)
		p.current += mib(1)
		p.observe()
	}
	// Then nothing arrives for ten seconds.
	for i := 0; i < 100; i++ {
		clock.advance(100 * time.Millisecond)
		p.observe()
	}
	line := stripANSI(p.line())
	if !strings.Contains(line, " 0 B/s") {
		t.Fatalf("line = %q, want a stalled rate after ten idle seconds", line)
	}
}

// One decimal of GiB moves every ~11 s at 9.5 MiB/s; two decimals move about
// once a second, which is what makes the line read as live.
func TestProgressDownloadedShowsTwoDecimalsInGiB(t *testing.T) {
	t.Setenv("COLUMNS", "140")
	p := newProgressReader(io.Discard, "model.gguf", gib(28.44), gib(89.9), io.NopCloser(strings.NewReader("")))
	line := stripANSI(p.line())
	if !strings.Contains(line, "(28.44 GiB / 89.9 GiB)") {
		t.Fatalf("line = %q, want two decimals on the downloaded figure only", line)
	}
	// Below a GiB the existing formatting is unchanged.
	q := newProgressReader(io.Discard, "model.gguf", 512, 1024, io.NopCloser(strings.NewReader("")))
	if line := stripANSI(q.line()); !strings.Contains(line, "(512 B / 1.0 KiB)") {
		t.Fatalf("line = %q, want unchanged small-size formatting", line)
	}
}

func TestProgressLineShowsETA(t *testing.T) {
	t.Setenv("COLUMNS", "140")
	clock := &fakeClock{t: time.Now()}
	p := newProgressReader(io.Discard, "model.gguf", gib(86), gib(90), io.NopCloser(strings.NewReader("")))
	p.now = clock.now
	p.started = clock.t
	for i := 0; i < 50; i++ {
		clock.advance(100 * time.Millisecond)
		p.current += mib(0.2) // 2 MiB/s; 4 GiB - 10 MiB left = 2043 s = 34m03s
		p.observe()
	}
	line := stripANSI(p.line())
	if !strings.Contains(line, "eta 34m03s") {
		t.Fatalf("line = %q, want an ETA from the moving rate", line)
	}
	if got := formatETA(2 * time.Hour); got != "2h00m" {
		t.Errorf("formatETA(2h) = %q, want 2h00m", got)
	}
	if got := formatETA(45 * time.Second); got != "45s" {
		t.Errorf("formatETA(45s) = %q, want 45s", got)
	}
	// No rate, no ETA: better than a nonsense figure.
	q := newProgressReader(io.Discard, "model.gguf", 0, gib(90), io.NopCloser(strings.NewReader("")))
	q.now = clock.now
	q.started = clock.t
	if line := stripANSI(q.line()); strings.Contains(line, "eta") {
		t.Fatalf("line = %q, want no ETA without a rate", line)
	}
}

// Regression for the "frozen line" feel: at 9.5 MiB/s on a 90 GiB file the
// visible text used to change 24 times in two minutes of 10 Hz frames.
func TestProgressVisibleTextChangesEverySecond(t *testing.T) {
	t.Setenv("COLUMNS", "140")
	clock := &fakeClock{t: time.Now()}
	p := newProgressReader(io.Discard, "GLM-5.3-Flash-Q2.gguf", gib(28.4), gib(89.9), io.NopCloser(strings.NewReader("")))
	p.now = clock.now
	p.started = clock.t.Add(-30 * time.Minute)
	distinct, last := 0, ""
	for tick := 0; tick < 1200; tick++ {
		clock.advance(100 * time.Millisecond)
		p.current += mib(0.95)
		p.observe()
		if frame := stripANSI(p.line()); frame != last {
			distinct++
			last = frame
		}
	}
	if distinct < 100 {
		t.Fatalf("visible text changed %d times in 120 s, want at least 100", distinct)
	}
}
