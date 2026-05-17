package install

import (
	"fmt"
	"io"
	"strings"
	"time"
)

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
		width: 96,
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
	p.render(false)
	fmt.Fprintln(p.out)
}

func (p *downloadProgress) render(force bool) {
	now := time.Now()
	if !force && now.Sub(p.last) < 100*time.Millisecond && p.downloaded < p.total {
		return
	}
	p.last = now

	msg := p.line()
	if len(msg) < p.width {
		msg += strings.Repeat(" ", p.width-len(msg))
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
