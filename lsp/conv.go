package lsp

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// toPosition converts 1-based (line, col) to a 0-based LSP Position.
func toPosition(line, col int) protocol.Position {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	return protocol.Position{Line: uint32(line - 1), Character: uint32(col - 1)}
}

// convertDiagnostic maps a wire diagnostic to our 1-based local type.
func convertDiagnostic(d protocol.Diagnostic) Diagnostic {
	return Diagnostic{
		Line:     int(d.Range.Start.Line) + 1,
		Col:      int(d.Range.Start.Character) + 1,
		Severity: Severity(d.Severity),
		Message:  d.Message,
		Source:   d.Source,
	}
}

// uriFor builds a file URI for a document path under root. The path may be
// virtual (the file need not exist on disk for in-memory analysis).
func uriFor(root, name string) string {
	p := name
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, name)
	}
	return string(protocol.URIFromPath(p))
}

// pathFromURI extracts a filesystem path from a file:// URI, percent-decoding
// it. Falls back to a naive prefix trim if the URI does not parse.
func pathFromURI(uri string) string {
	if p, err := protocol.DocumentURI(uri).Path(); err == nil {
		return p
	}
	return strings.TrimPrefix(uri, "file://")
}
