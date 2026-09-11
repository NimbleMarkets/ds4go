package tui

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/NimbleMarkets/ds4go/internal/termline"
)

// terminalSizeFunc reports w's width, and whether w is a terminal at all.
// Overridden in tests.
var terminalSizeFunc termline.SizeFunc = termline.TerminalSize

// NewProgressTracker returns a reader wrapper that tracks progress.
func NewProgressTracker(out io.Writer, name string, start, total int64) models.ProgressTracker {
	return newProgressReader(out, name, start, total, http.NoBody)
}

var (
	colorAccent  = lipgloss.Color("#5FBE9E")
	colorPrimary = lipgloss.Color("#C9D1D9")
	colorSurface = lipgloss.Color("#30363D")
	colorDark    = lipgloss.Color("#0B1411")

	progressDone = lipgloss.NewStyle().Bold(true).Foreground(colorDark).Background(colorAccent)
	progressRest = lipgloss.NewStyle().Foreground(colorPrimary).Background(colorSurface)
)

type progressReader struct {
	out        io.Writer
	term       *termline.Line
	name       string
	current    int64
	initial    int64
	total      int64
	reader     io.ReadCloser
	started    time.Time
	lastRender time.Time
}

func newProgressReader(out io.Writer, name string, current, total int64, reader io.ReadCloser) *progressReader {
	line := termline.New(out)
	line.Size = func(w io.Writer) (int, bool) { return terminalSizeFunc(w) }
	p := &progressReader{
		out:     out,
		term:    line,
		name:    name,
		current: current,
		initial: current,
		total:   total,
		reader:  reader,
		started: time.Now(),
	}
	p.render(true)
	return p
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)
	p.current += int64(n)
	p.render(false)
	return n, err
}

func (p *progressReader) Close() error {
	return p.reader.Close()
}

// SwapReader replaces the underlying source. Used by the chunked downloader
// to stream multiple Range responses through one progress display so the
// rendered progress bar reflects whole-file completion rather than per-chunk.
func (p *progressReader) SwapReader(r io.ReadCloser) {
	p.reader = r
}

func (p *progressReader) Done(err error) {
	if err == nil && p.total > 0 {
		p.current = p.total
	}
	p.render(true)
	p.term.Finish()
}

// columns reports the width to render at: the real terminal size when the
// output is a terminal, then an explicit COLUMNS, then a default of 80.
func (p *progressReader) columns() int {
	return p.term.Columns()
}

func (p *progressReader) render(force bool) {
	if !force && time.Since(p.lastRender) < 100*time.Millisecond && p.current < p.total {
		return
	}
	// Redirected output has no cursor to rewind, so interstitial frames would
	// just pile up in the log. Keep the first and the final one.
	if !force && !p.term.IsTerminal() {
		return
	}
	p.lastRender = time.Now()
	// The width is read once per frame and used both to size the name and
	// to draw, so a resize between the two cannot mismatch them. termline
	// clears the previous frame by erasing rather than padding.
	p.term.Draw(p.lineAt(p.columns()))
}

// line renders the frame at the current terminal width.
func (p *progressReader) line() string {
	return p.lineAt(p.columns())
}

// lineAt renders the frame sized for width columns, leaving one column free
// so the row never reaches the wrap column.
func (p *progressReader) lineAt(width int) string {
	width--
	if p.total > 0 {
		pct := float64(p.current) / float64(p.total) * 100
		size := fmt.Sprintf("(%s / %s)", formatBytes(p.current), formatBytes(p.total))
		speed := formatBytes(p.bytesPerSecond()) + "/s"
		suffix := fmt.Sprintf(" %.1f%% %s %s", pct, size, speed)
		return "Downloading: " + p.progressName(width, lipgloss.Width("Downloading: ")+lipgloss.Width(suffix)) + suffix
	}
	size := fmt.Sprintf("(%s)", formatBytes(p.current))
	speed := formatBytes(p.bytesPerSecond()) + "/s"
	suffix := " " + size + " " + speed
	return "Downloading: " + shortenToWidth(p.name, width-lipgloss.Width("Downloading: ")-lipgloss.Width(suffix)) + suffix
}

func (p *progressReader) progressName(totalWidth, usedWidth int) string {
	width := totalWidth - usedWidth
	if width < 12 {
		width = 12
	}
	name := shortenToWidth(p.name, width)
	fill := 0
	if p.total > 0 {
		fill = int(float64(p.current) / float64(p.total) * float64(lipgloss.Width(name)))
	}
	if fill < 0 {
		fill = 0
	}
	if fill > lipgloss.Width(name) {
		fill = lipgloss.Width(name)
	}
	done, rest := splitByWidth(name, fill)
	return progressDone.Render(done) + progressRest.Render(rest)
}

func (p *progressReader) bytesPerSecond() int64 {
	elapsed := time.Since(p.started).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return int64(float64(p.current-p.initial) / elapsed)
}

func shortenToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	var b strings.Builder
	for _, r := range s {
		next := b.String() + string(r)
		if lipgloss.Width(next)+1 > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}

func splitByWidth(s string, width int) (string, string) {
	if width <= 0 {
		return "", s
	}
	var b strings.Builder
	used := 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > width {
			return b.String(), s[i:]
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String(), ""
}

func formatBytes(n int64) string {
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
