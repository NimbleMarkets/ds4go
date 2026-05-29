package lsp

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func newTestClient(t *testing.T, f *fakeRPC) *Client {
	t.Helper()
	c, err := newClientWithRPC(context.Background(), f, ServerConfig{RootDir: "/work", FirstWait: 50 * time.Millisecond, SettleWait: 30 * time.Millisecond})
	if err != nil {
		t.Fatalf("newClientWithRPC: %v", err)
	}
	return c
}

func TestClient_OpenSendsDidOpen(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	uri := c.URI("a.lua")
	if err := c.Open(context.Background(), uri, "lua", "print(1)"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if f.openedURI != uri || f.openVersion != 1 || f.openText != "print(1)" {
		t.Fatalf("didOpen recorded %q v%d %q", f.openedURI, f.openVersion, f.openText)
	}
}

func TestClient_UpdateBumpsVersionAndSendsWholeDoc(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	uri := c.URI("a.lua")
	_ = c.Open(context.Background(), uri, "lua", "v1")
	v, err := c.Update(context.Background(), uri, "v2")
	if err != nil || v != 2 {
		t.Fatalf("Update v=%d err=%v, want 2 nil", v, err)
	}
	if f.changeText != "v2" || f.changeVersion != 2 {
		t.Fatalf("didChange recorded %q v%d", f.changeText, f.changeVersion)
	}
}

func TestClient_QueryWhenServerDown(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	f.running = false
	err := c.Open(context.Background(), c.URI("a.lua"), "lua", "x")
	if !errors.Is(err, ErrServerDown) {
		t.Fatalf("Open after down: err=%v, want ErrServerDown", err)
	}
}

func TestClient_PublishedDiagnosticsFlowThrough(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	uri := c.URI("a.lua")
	_ = c.Open(context.Background(), uri, "lua", "x")
	// Simulate the server pushing diagnostics via the registered handler.
	f.notifyHandler(context.Background(), "textDocument/publishDiagnostics",
		[]byte(`{"uri":"`+uri+`","version":1,"diagnostics":[{"severity":1,"message":"bad","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}]}`))
	got, err := c.WaitForDiagnostics(context.Background(), uri, time.Second)
	if err != nil {
		t.Fatalf("WaitForDiagnostics: %v", err)
	}
	if len(got) != 1 || got[0].Message != "bad" || got[0].Line != 1 {
		t.Fatalf("got %+v", got)
	}
}

func publishDiag(t *testing.T, f *fakeRPC, uri string, version int, msg string) {
	t.Helper()
	f.notifyHandler(context.Background(), "textDocument/publishDiagnostics",
		[]byte(`{"uri":"`+uri+`","version":`+strconv.Itoa(version)+`,"diagnostics":[{"severity":1,"message":"`+msg+`","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}]}`))
}

// After an edit, the previous version's diagnostics must not be returned as if
// they applied to the new version (stale-on-update regression).
func TestClient_UpdateClearsStaleDiagnostics(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	uri := c.URI("a.lua")
	_ = c.Open(context.Background(), uri, "lua", "v1")
	publishDiag(t, f, uri, 1, "old-error")
	if _, err := c.Update(context.Background(), uri, "v2"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Server has not published for v2 yet; must not surface the v1 diagnostics.
	got, err := c.WaitForDiagnostics(context.Background(), uri, time.Second)
	if err != nil {
		t.Fatalf("WaitForDiagnostics: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got stale %+v, want none after edit", got)
	}
}

// Reopening a URI (which resets the version to 1) must not match a leftover
// version-1 snapshot from the previous open (stale-on-reopen regression).
func TestClient_ReopenClearsStaleDiagnostics(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	uri := c.URI("a.lua")
	_ = c.Open(context.Background(), uri, "lua", "v1")
	publishDiag(t, f, uri, 1, "old-error")
	_ = c.Close(context.Background(), uri)
	_ = c.Open(context.Background(), uri, "lua", "fresh")
	got, err := c.WaitForDiagnostics(context.Background(), uri, time.Second)
	if err != nil {
		t.Fatalf("WaitForDiagnostics: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got stale %+v, want none after reopen", got)
	}
}

func TestClient_WaitForDiagnosticsUnopenedURIErrors(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	_, err := c.WaitForDiagnostics(context.Background(), c.URI("missing.lua"), time.Second)
	if err == nil {
		t.Fatal("WaitForDiagnostics on unopened URI: want error, got nil")
	}
}

func TestClient_ShutdownCallsExit(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !f.shutdownCalled || !f.exitCalled {
		t.Fatalf("shutdown=%v exit=%v", f.shutdownCalled, f.exitCalled)
	}
}

func TestClient_Hover(t *testing.T) {
	f := newFakeRPC()
	f.hoverResult = &protocol.Hover{Contents: protocol.MarkupContent{Value: "## func add"}}
	c := newTestClient(t, f)
	got, err := c.Hover(context.Background(), c.URI("a.lua"), 1, 1)
	if err != nil || got != "## func add" {
		t.Fatalf("Hover = %q err=%v", got, err)
	}
}

func TestClient_CompletionCapsAndReportsTruncation(t *testing.T) {
	f := newFakeRPC()
	f.complResult = &protocol.CompletionList{Items: []protocol.CompletionItem{
		{Label: "a"}, {Label: "b"}, {Label: "c"},
	}}
	c := newTestClient(t, f)
	labels, trunc, err := c.Completion(context.Background(), c.URI("a.lua"), 1, 1, 2)
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if len(labels) != 2 || trunc != 1 {
		t.Fatalf("labels=%v trunc=%d, want 2 labels + 1 truncated", labels, trunc)
	}
}

func TestClient_Symbols(t *testing.T) {
	f := newFakeRPC()
	f.symbolResult = []protocol.DocumentSymbolResult{
		&protocol.DocumentSymbol{Name: "add", Kind: protocol.Function, Range: protocol.Range{Start: protocol.Position{Line: 4}}},
	}
	c := newTestClient(t, f)
	got, err := c.Symbols(context.Background(), c.URI("a.lua"))
	if err != nil || len(got) != 1 || got[0].Name != "add" || got[0].Line != 5 || got[0].Kind != 12 {
		t.Fatalf("Symbols = %+v err=%v", got, err)
	}
}

// Hierarchical DocumentSymbol responses must be flattened depth-first so nested
// symbols (e.g. a class's methods) are not dropped from the outline.
func TestClient_SymbolsFlattensNestedChildren(t *testing.T) {
	f := newFakeRPC()
	f.symbolResult = []protocol.DocumentSymbolResult{
		&protocol.DocumentSymbol{
			Name: "Calc", Kind: protocol.Class, Range: protocol.Range{Start: protocol.Position{Line: 0}},
			Children: []protocol.DocumentSymbol{
				{Name: "add", Kind: protocol.Method, Range: protocol.Range{Start: protocol.Position{Line: 1}}},
				{Name: "sub", Kind: protocol.Method, Range: protocol.Range{Start: protocol.Position{Line: 4}}},
			},
		},
	}
	c := newTestClient(t, f)
	got, err := c.Symbols(context.Background(), c.URI("a.lua"))
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d symbols, want 3 (parent + 2 children): %+v", len(got), got)
	}
	if got[0].Name != "Calc" || got[1].Name != "add" || got[1].Line != 2 || got[2].Name != "sub" || got[2].Line != 5 {
		t.Fatalf("nested flatten = %+v", got)
	}
}

// Test that docStore + diagBuffer normalization (pathFromURI) makes Open/Update/
// WaitForDiagnostics/Diagnostics robust to percent-encoded vs. decoded (or
// otherwise variant) URI strings for the same logical document. This exercises
// the fix for the URI keying inconsistency.
func TestClient_URIVariantEncodingRoundtrip(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)

	// Two representations of the "same" document (space vs %20). In real use
	// a server may echo a differently-encoded URI in publishDiagnostics.
	encoded := "file:///work/a%20b.lua"
	plain := "file:///work/a b.lua"

	// Open using the encoded form.
	if err := c.Open(context.Background(), encoded, "lua", "print(1)"); err != nil {
		t.Fatalf("Open(encoded): %v", err)
	}

	// WaitFor and Diagnostics using the plain (decoded) form must succeed and
	// see the document (version tracking + diag buffer now share normalized keys).
	publishDiag(t, f, encoded, 1, "syntax error")
	got, err := c.WaitForDiagnostics(context.Background(), plain, time.Second)
	if err != nil {
		t.Fatalf("WaitForDiagnostics(plain): %v", err)
	}
	if len(got) != 1 || got[0].Message != "syntax error" {
		t.Fatalf("WaitFor(plain) got %+v, want the published diag", got)
	}

	diags := c.Diagnostics(plain)
	if len(diags) != 1 {
		t.Fatalf("Diagnostics(plain) got %d, want 1", len(diags))
	}

	// Update using plain form must bump version.
	v, err := c.Update(context.Background(), plain, "print(2)")
	if err != nil || v != 2 {
		t.Fatalf("Update(plain) v=%d err=%v, want 2 nil", v, err)
	}

	// Close using encoded form must remove it.
	if err := c.Close(context.Background(), encoded); err != nil {
		t.Fatalf("Close(encoded): %v", err)
	}
	_, err = c.WaitForDiagnostics(context.Background(), plain, time.Second)
	if err == nil {
		t.Fatal("WaitFor after close: want error for unopened, got nil")
	}
}

// WaitForDiagnostics on a previously-open document must return ErrServerDown
// (fast) rather than blocking for the timeout when the server has died.
func TestClient_WaitForDiagnosticsServerDown(t *testing.T) {
	f := newFakeRPC()
	c := newTestClient(t, f)
	uri := c.URI("a.lua")
	_ = c.Open(context.Background(), uri, "lua", "x")

	f.running = false
	_, err := c.WaitForDiagnostics(context.Background(), uri, time.Second)
	if !errors.Is(err, ErrServerDown) {
		t.Fatalf("WaitFor after server down: err=%v, want ErrServerDown", err)
	}
}
