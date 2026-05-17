package main

import (
	"strings"
	"testing"
)

func TestFormatPartialModel(t *testing.T) {
	got := formatPartialModel(80*1024*1024*1024, 81.2)
	if !strings.Contains(got, "GiB") {
		t.Fatalf("formatPartialModel() = %q, want GiB", got)
	}
	if !strings.Contains(got, "%") {
		t.Fatalf("formatPartialModel() = %q, want percent", got)
	}
}
