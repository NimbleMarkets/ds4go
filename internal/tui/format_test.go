package tui

import (
	"strings"
	"testing"
)

func TestFormatPartialModel(t *testing.T) {
	got := FormatPartialModel(80*1024*1024*1024, 81.2)
	if !strings.Contains(got, "GiB") {
		t.Fatalf("FormatPartialModel() = %q, want GiB", got)
	}
	if !strings.Contains(got, "%") {
		t.Fatalf("FormatPartialModel() = %q, want percent", got)
	}
}
