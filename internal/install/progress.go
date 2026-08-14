package install

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// defaultProgressColumns is used when the output is not a measurable terminal.
// A frame wider than the real terminal wraps, and the "\r" redraw then rewinds
// only the last physical row, so the display repeats instead of updating.
const defaultProgressColumns = 80

// progressColumns reports the render width for w.
func progressColumns(w io.Writer) int {
	if file, ok := w.(*os.File); ok && term.IsTerminal(file.Fd()) {
		if width, _, err := term.GetSize(file.Fd()); err == nil && width >= 20 {
			return width
		}
	}
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols >= 40 {
		return cols
	}
	return defaultProgressColumns
}

// progressIsTerminal reports whether w can have its cursor rewound.
func progressIsTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

type downloadProgress struct {
	out        io.Writer
	style      nimbleStyle
	name       string
	total      int64
	downloaded int64
	last       time.Time
	width      int
}

func newDownloadProgress(out io.Writer, name string, total int64) *downloadProgress {
	if name == "" || name == "." || name == "/" {
		name = "asset"
	}
	p := &downloadProgress{
		out:   out,
		style: defaultNimbleStyle(),
		name:  name,
		total: total,
		width: progressColumns(out),
	}
	p.render(true)
	return p
}

func (p *downloadProgress) Wrap(r io.ReadCloser) io.ReadCloser {
	return &progressReadCloser{reader: r, progress: p}
}

func (p *downloadProgress) Add(n int) {
	if n <= 0 {
		return
	}
	p.downloaded += int64(n)
	if time.Since(p.last) < 100*time.Millisecond && p.downloaded < p.total {
		return
	}
	p.render(false)
}

func (p *downloadProgress) Done(err error) {
	if err == nil && p.total > 0 {
		p.downloaded = p.total
	}
	// Forced: the final frame must be emitted even when the rate limiter or the
	// non-terminal guard would skip an ordinary redraw.
	p.render(true)
	fmt.Fprintln(p.out)
}

func (p *downloadProgress) render(force bool) {
	now := time.Now()
	if !force && now.Sub(p.last) < 100*time.Millisecond && p.downloaded < p.total {
		return
	}
	// Redirected output has no cursor to rewind, so interstitial frames would
	// only pile up in a log. Keep the first and the final one.
	if !force && !progressIsTerminal(p.out) {
		return
	}
	p.last = now

	msg := p.line()
	// Pad by display width: len() counts ANSI escape bytes, so byte-based
	// padding never clears the previous frame.
	if w := lipgloss.Width(msg); w < p.width {
		msg += strings.Repeat(" ", p.width-w)
	}
	fmt.Fprintf(p.out, "\r%s", msg)
}

func (p *downloadProgress) line() string {
	if p.total > 0 {
		pct := float64(p.downloaded) / float64(p.total) * 100
		return fmt.Sprintf("%s %s %.1f%% (%s / %s)",
			p.style.Action("Downloading"),
			p.style.Asset(p.name),
			pct,
			formatBytes(p.downloaded),
			formatBytes(p.total),
		)
	}
	return fmt.Sprintf("%s %s %s",
		p.style.Action("Downloading"),
		p.style.Asset(p.name),
		formatBytes(p.downloaded),
	)
}

type progressReadCloser struct {
	reader   io.ReadCloser
	progress *downloadProgress
}

func (r *progressReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.progress.Add(n)
	return n, err
}

func (r *progressReadCloser) Close() error {
	return r.reader.Close()
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
