package models

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

type progressReader struct {
	out        io.Writer
	name       string
	current    int64
	initial    int64
	total      int64
	reader     io.ReadCloser
	started    time.Time
	lastRender time.Time
}

func newProgressReader(out io.Writer, name string, current, total int64, reader io.ReadCloser) *progressReader {
	p := &progressReader{
		out:     out,
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

func (p *progressReader) Done(err error) {
	if err == nil && p.total > 0 {
		p.current = p.total
	}
	p.render(true)
	fmt.Fprintln(p.out)
}

func (p *progressReader) render(force bool) {
	if !force && time.Since(p.lastRender) < 100*time.Millisecond && p.current < p.total {
		return
	}
	p.lastRender = time.Now()
	line := p.line()
	width := terminalColumns()
	if w := lipgloss.Width(line); w < width {
		line += strings.Repeat(" ", width-w)
	}
	fmt.Fprintf(p.out, "\r%s", line)
}

func (p *progressReader) line() string {
	if p.total > 0 {
		pct := float64(p.current) / float64(p.total) * 100
		size := fmt.Sprintf("(%s / %s)", formatBytes(p.current), formatBytes(p.total))
		speed := formatBytes(p.bytesPerSecond()) + "/s"
		suffix := fmt.Sprintf(" %.1f%% %s %s", pct, size, speed)
		return "Downloading: " + p.progressName(lipgloss.Width("Downloading: ")+lipgloss.Width(suffix)) + suffix
	}
	size := fmt.Sprintf("(%s)", formatBytes(p.current))
	speed := formatBytes(p.bytesPerSecond()) + "/s"
	suffix := " " + size + " " + speed
	return "Downloading: " + shortenToWidth(p.name, terminalColumns()-lipgloss.Width("Downloading: ")-lipgloss.Width(suffix)) + suffix
}

func (p *progressReader) progressName(usedWidth int) string {
	width := terminalColumns() - usedWidth
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
	doneStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#0B1411")).
		Background(lipgloss.Color("#39FFB6")).
		Bold(true)
	restStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#C9D1D9")).
		Background(lipgloss.Color("#30363D"))
	return doneStyle.Render(done) + restStyle.Render(rest)
}

func (p *progressReader) bytesPerSecond() int64 {
	elapsed := time.Since(p.started).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return int64(float64(p.current-p.initial) / elapsed)
}

func terminalColumns() int {
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols >= 40 {
		return cols
	}
	return 120
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
