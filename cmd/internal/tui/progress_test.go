package tui

import (
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
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
