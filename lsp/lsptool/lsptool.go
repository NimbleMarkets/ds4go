// Package lsptool adapts an *lsp.Client into ds4go.ToolHandler values so a
// model running in a ds4go.ToolLoop can query a language server.
package lsptool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ds4go "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/lsp"
)

// DefaultDiagnosticsTimeout bounds the wait in the diagnostics tool.
const DefaultDiagnosticsTimeout = 5 * time.Second

// Querier is the subset of *lsp.Client the tools need. *lsp.Client satisfies
// it; tests substitute a fake.
type Querier interface {
	Update(ctx context.Context, uri, text string) (int, error)
	WaitForDiagnostics(ctx context.Context, uri string, timeout time.Duration) ([]lsp.Diagnostic, error)
	Diagnostics(uri string) []lsp.Diagnostic
	Hover(ctx context.Context, uri string, line, col int) (string, error)
	Symbols(ctx context.Context, uri string) ([]lsp.Symbol, error)
	Completion(ctx context.Context, uri string, line, col, limit int) ([]string, int, error)
}

var _ Querier = (*lsp.Client)(nil)

// uriArg is implemented by every tool's argument struct so newTool can decode
// and enforce the shared "uri is required" precondition in one place.
type uriArg interface{ uri() string }

// newTool builds a ds4go.ToolHandler from a schema and a typed handler,
// centralizing JSON argument decoding and the uri-required check that every
// tool needs. A is constrained to uriArg so newTool can decode into an
// addressable local variable and then call uri() on it.
func newTool[A uriArg](schema ds4go.ToolSchema, run func(context.Context, A) (string, error)) ds4go.ToolHandler {
	return ds4go.Tool{ToolSchema: schema, Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var a A
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("%s: bad args: %w", schema.Name, err)
		}
		if a.uri() == "" {
			return "", fmt.Errorf("%s: uri is required", schema.Name)
		}
		return run(ctx, a)
	}}
}

// --- argument types ---

type diagArgs struct {
	URI  string `json:"uri"`
	Code string `json:"code,omitempty"`
}

type posArgs struct {
	URI       string `json:"uri"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
}

type symArgs struct {
	URI string `json:"uri"`
}

func (a diagArgs) uri() string { return a.URI }
func (a posArgs) uri() string  { return a.URI }
func (a symArgs) uri() string  { return a.URI }

// --- diagnostics tool ---

// NewDiagnosticsTool returns a tool that reports diagnostics for a document. If
// "code" is supplied, the document is updated and re-checked first. Without
// "code" it returns the latest buffered diagnostics, which may lag the most
// recent edit.
func NewDiagnosticsTool(q Querier) ds4go.ToolHandler {
	schema := ds4go.ToolSchema{
		Name:        "lsp_diagnostics",
		Description: "Report language-server diagnostics (errors/warnings) for an open document. Optionally pass updated code to re-check first.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string","description":"document URI"},"code":{"type":"string","description":"optional new full document text to apply before checking"}},"required":["uri"]}`),
	}
	return newTool(schema, func(ctx context.Context, a diagArgs) (string, error) {
		var diags []lsp.Diagnostic
		if a.Code != "" {
			if _, err := q.Update(ctx, a.URI, a.Code); err != nil {
				return fmt.Sprintf("diagnostics unavailable: %v", err), nil
			}
			var err error
			diags, err = q.WaitForDiagnostics(ctx, a.URI, DefaultDiagnosticsTimeout)
			switch {
			case err == nil:
				// fall through to formatting below
			case errors.Is(err, lsp.ErrDiagnosticsTimeout):
				// Timed out, but the buffered snapshot is still useful.
				return fmt.Sprintf("timed out waiting for diagnostics; returning buffered results: %v\n%s", err, formatDiagnostics(a.URI, diags)), nil
			default:
				// e.g. ErrServerDown — not a timeout; report it as-is so the
				// caller doesn't retry a wait that will never succeed.
				return fmt.Sprintf("diagnostics unavailable: %v", err), nil
			}
		} else {
			diags = q.Diagnostics(a.URI)
		}
		return formatDiagnostics(a.URI, diags), nil
	})
}

var symbolKindNames = map[int]string{
	1:  "file",
	2:  "module",
	3:  "namespace",
	4:  "package",
	5:  "class",
	6:  "method",
	7:  "property",
	8:  "field",
	9:  "constructor",
	10: "enum",
	11: "interface",
	12: "function",
	13: "variable",
	14: "constant",
	15: "string",
	16: "number",
	17: "boolean",
	18: "array",
	19: "object",
	20: "key",
	21: "null",
	22: "enumMember",
	23: "struct",
	24: "event",
	25: "operator",
	26: "typeParameter",
}

func symbolKindName(kind int) string {
	if n, ok := symbolKindNames[kind]; ok {
		return n
	}
	return "unknown"
}

func formatDiagnostics(uri string, diags []lsp.Diagnostic) string {
	if len(diags) == 0 {
		return "no diagnostics"
	}
	var b strings.Builder
	for _, d := range diags {
		fmt.Fprintf(&b, "%s:%d:%d: [%s] %s\n", uri, d.Line, d.Col, d.Severity, d.Message)
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- hover tool ---

// NewHoverTool returns a tool that returns hover info at a 1-based line and
// character position in a document.
func NewHoverTool(q Querier) ds4go.ToolHandler {
	schema := ds4go.ToolSchema{
		Name:        "lsp_hover",
		Description: "Hover info (type/doc) at a 1-based line and character in a document.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string"},"line":{"type":"integer"},"character":{"type":"integer"}},"required":["uri","line","character"]}`),
	}
	return newTool(schema, func(ctx context.Context, a posArgs) (string, error) {
		md, err := q.Hover(ctx, a.URI, a.Line, a.Character)
		if err != nil {
			return fmt.Sprintf("hover unavailable: %v", err), nil
		}
		if md == "" {
			return "no hover info", nil
		}
		return md, nil
	})
}

// --- symbols tool ---

// NewSymbolsTool returns a tool that lists the document outline for a document.
func NewSymbolsTool(q Querier) ds4go.ToolHandler {
	schema := ds4go.ToolSchema{
		Name:        "lsp_symbols",
		Description: "List the document outline (functions/types) for a document.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string"}},"required":["uri"]}`),
	}
	return newTool(schema, func(ctx context.Context, a symArgs) (string, error) {
		syms, err := q.Symbols(ctx, a.URI)
		if err != nil {
			return fmt.Sprintf("symbols unavailable: %v", err), nil
		}
		if len(syms) == 0 {
			return "no symbols", nil
		}
		var b strings.Builder
		for _, s := range syms {
			fmt.Fprintf(&b, "%s (%s, line %d)\n", s.Name, symbolKindName(s.Kind), s.Line)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	})
}

// --- completion tool ---

const completionLimit = 25

// NewCompletionTool returns a tool that returns completion suggestions at a
// 1-based line and character position in a document.
func NewCompletionTool(q Querier) ds4go.ToolHandler {
	schema := ds4go.ToolSchema{
		Name:        "lsp_completion",
		Description: "Completion suggestions at a 1-based line and character in a document.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string"},"line":{"type":"integer"},"character":{"type":"integer"}},"required":["uri","line","character"]}`),
	}
	return newTool(schema, func(ctx context.Context, a posArgs) (string, error) {
		labels, truncated, err := q.Completion(ctx, a.URI, a.Line, a.Character, completionLimit)
		if err != nil {
			return fmt.Sprintf("completion unavailable: %v", err), nil
		}
		if len(labels) == 0 {
			return "no completions", nil
		}
		out := strings.Join(labels, ", ")
		if truncated > 0 {
			out += fmt.Sprintf(" …%d more", truncated)
		}
		return out, nil
	})
}
