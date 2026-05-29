package lsptool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go/lsp"
)

type fakeQuerier struct {
	updateVersion int
	waitDiags     []lsp.Diagnostic
	waitErr       error
	bufDiags      []lsp.Diagnostic
	hover         string
	symbols       []lsp.Symbol
	complLabels   []string
	complTrunc    int
}

func (f *fakeQuerier) Update(context.Context, string, string) (int, error) {
	return f.updateVersion, nil
}
func (f *fakeQuerier) WaitForDiagnostics(context.Context, string, time.Duration) ([]lsp.Diagnostic, error) {
	return f.waitDiags, f.waitErr
}
func (f *fakeQuerier) Diagnostics(string) []lsp.Diagnostic                     { return f.bufDiags }
func (f *fakeQuerier) Hover(context.Context, string, int, int) (string, error) { return f.hover, nil }
func (f *fakeQuerier) Symbols(context.Context, string) ([]lsp.Symbol, error)   { return f.symbols, nil }
func (f *fakeQuerier) Completion(context.Context, string, int, int, int) ([]string, int, error) {
	return f.complLabels, f.complTrunc, nil
}

func TestDiagnosticsTool_FormatsWithCode(t *testing.T) {
	q := &fakeQuerier{updateVersion: 2, waitDiags: []lsp.Diagnostic{
		{Line: 3, Col: 5, Severity: lsp.SeverityError, Message: "undefined 'foo'"},
	}}
	tool := NewDiagnosticsTool(q)
	out, err := tool.Invoke(context.Background(), json.RawMessage(`{"uri":"file:///a.lua","code":"x"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(out, "file:///a.lua:3:5: [error] undefined 'foo'") {
		t.Fatalf("unexpected output: %q", out)
	}
}

// A timeout still surfaces the best-effort buffered diagnostics, framed as a
// timeout rather than a hard failure.
func TestDiagnosticsTool_TimeoutReturnsBufferedResults(t *testing.T) {
	q := &fakeQuerier{
		updateVersion: 2,
		waitDiags:     []lsp.Diagnostic{{Line: 1, Col: 1, Severity: lsp.SeverityWarning, Message: "stale"}},
		waitErr:       lsp.ErrDiagnosticsTimeout,
	}
	tool := NewDiagnosticsTool(q)
	out, err := tool.Invoke(context.Background(), json.RawMessage(`{"uri":"file:///a.lua","code":"x"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(out, "timed out") || !strings.Contains(out, "stale") {
		t.Fatalf("expected timeout framing with buffered results, got %q", out)
	}
}

// A non-timeout error (e.g. the server is down) must NOT be mislabeled as a
// timeout — that would tell the caller to retry a wait that can never succeed.
func TestDiagnosticsTool_ServerDownNotLabeledTimeout(t *testing.T) {
	q := &fakeQuerier{updateVersion: 2, waitErr: lsp.ErrServerDown}
	tool := NewDiagnosticsTool(q)
	out, err := tool.Invoke(context.Background(), json.RawMessage(`{"uri":"file:///a.lua","code":"x"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if strings.Contains(out, "timed out") {
		t.Fatalf("server-down error mislabeled as timeout: %q", out)
	}
	if !strings.Contains(out, "diagnostics unavailable") {
		t.Fatalf("expected 'diagnostics unavailable', got %q", out)
	}
}

func TestDiagnosticsTool_NoDiagnostics(t *testing.T) {
	tool := NewDiagnosticsTool(&fakeQuerier{})
	out, _ := tool.Invoke(context.Background(), json.RawMessage(`{"uri":"file:///a.lua"}`))
	if out != "no diagnostics" {
		t.Fatalf("got %q", out)
	}
}

func TestCompletionTool_Truncation(t *testing.T) {
	q := &fakeQuerier{complLabels: []string{"aa", "bb"}, complTrunc: 7}
	tool := NewCompletionTool(q)
	out, _ := tool.Invoke(context.Background(), json.RawMessage(`{"uri":"u","line":1,"character":1}`))
	if !strings.Contains(out, "aa, bb") || !strings.Contains(out, "…7 more") {
		t.Fatalf("got %q", out)
	}
}

func TestDiagnosticsTool_RejectsMissingURI(t *testing.T) {
	tool := NewDiagnosticsTool(&fakeQuerier{})
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected error for missing uri")
	}
}

// All position/uri tools enforce the uri-required precondition uniformly.
func TestTools_RejectMissingURI(t *testing.T) {
	q := &fakeQuerier{}
	tools := []interface {
		Invoke(context.Context, json.RawMessage) (string, error)
	}{
		NewHoverTool(q), NewSymbolsTool(q), NewCompletionTool(q),
	}
	for _, tool := range tools {
		if _, err := tool.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
			t.Fatal("expected error for missing uri")
		}
	}
}

func TestSymbolsTool_FormatsWithKind(t *testing.T) {
	q := &fakeQuerier{symbols: []lsp.Symbol{
		{Name: "add", Line: 5, Kind: 12},
		{Name: "x", Line: 2, Kind: 13},
	}}
	tool := NewSymbolsTool(q)
	out, err := tool.Invoke(context.Background(), json.RawMessage(`{"uri":"file:///a.lua"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(out, "add (function, line 5)") || !strings.Contains(out, "x (variable, line 2)") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestSchemaName(t *testing.T) {
	if NewHoverTool(&fakeQuerier{}).Schema().Name != "lsp_hover" {
		t.Fatal("hover tool name mismatch")
	}
}
