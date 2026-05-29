package lsp

import (
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func TestToPosition_OneBasedToZeroBased(t *testing.T) {
	got := toPosition(3, 5)
	if got.Line != 2 || got.Character != 4 {
		t.Fatalf("toPosition(3,5) = {%d,%d}, want {2,4}", got.Line, got.Character)
	}
}

func TestConvertDiagnostic_ZeroBasedToOneBased(t *testing.T) {
	in := protocol.Diagnostic{
		Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 6}},
		Severity: protocol.SeverityWarning,
		Message:  "unused variable",
		Source:   "luals",
	}
	got := convertDiagnostic(in)
	want := Diagnostic{Line: 1, Col: 7, Severity: SeverityWarning, Message: "unused variable", Source: "luals"}
	if got != want {
		t.Fatalf("convertDiagnostic = %+v, want %+v", got, want)
	}
}

func TestURIFor_RoundTrips(t *testing.T) {
	uri := uriFor("/tmp/work", "a.lua")
	if uri == "" {
		t.Fatal("uriFor returned empty")
	}
	if path := pathFromURI(uri); path == "" {
		t.Fatalf("pathFromURI(%q) returned empty", uri)
	}
}
