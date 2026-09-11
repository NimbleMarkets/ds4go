package install

import (
	"fmt"
	"io"
	"time"

	"github.com/NimbleMarkets/ds4go/internal/termline"
)

type downloadProgress struct {
	out        io.Writer
	term       *termline.Line
	style      nimbleStyle
	name       string
	total      int64
	downloaded int64
	last       time.Time
}

func newDownloadProgress(out io.Writer, name string, total int64) *downloadProgress {
	if name == "" || name == "." || name == "/" {
		name = "asset"
	}
	p := &downloadProgress{
		out:   out,
		term:  termline.New(out),
		style: defaultNimbleStyle(),
		name:  name,
		total: total,
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
	p.term.Finish()
}

func (p *downloadProgress) render(force bool) {
	now := time.Now()
	if !force && now.Sub(p.last) < 100*time.Millisecond && p.downloaded < p.total {
		return
	}
	// Redirected output has no cursor to rewind, so interstitial frames would
	// only pile up in a log. Keep the first and the final one.
	if !force && !p.term.IsTerminal() {
		return
	}
	p.last = now
	// termline re-reads the width per frame, truncates to fit, and clears the
	// previous frame by erasing rather than padding, so a resize mid-download
	// does not leave orphaned rows.
	p.term.Draw(p.line())
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
