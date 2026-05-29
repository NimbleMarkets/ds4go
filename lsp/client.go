package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	pnlsp "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// Client drives a single persistent language-server subprocess with in-memory
// documents. It is safe for concurrent use.
type Client struct {
	rpc             rpcClient
	docs            *docStore
	diag            *diagBuffer
	root            string
	shutdownTimeout time.Duration
}

// New launches and initializes a language server per cfg, returning a ready
// Client. The server command must be on PATH.
func New(ctx context.Context, cfg ServerConfig) (*Client, error) {
	rootURI := ""
	var folders []protocol.WorkspaceFolder
	if cfg.RootDir != "" {
		rootURI = string(protocol.URIFromPath(cfg.RootDir))
		// Some servers (e.g. lua-language-server) drop textDocument
		// notifications unless at least one workspace folder is declared.
		folders = []protocol.WorkspaceFolder{{URI: rootURI, Name: filepath.Base(cfg.RootDir)}}
	}
	rpc, err := pnlsp.NewClient(pnlsp.ClientConfig{
		Command:          cfg.Command,
		Args:             cfg.Args,
		RootURI:          rootURI,
		WorkspaceFolders: folders,
		InitOptions:      cfg.InitOptions,
		Settings:         cfg.Settings,
		Environment:      cfg.Environment,
		Timeout:          cfg.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("lsp: start server: %w", err)
	}
	return newClientWithRPC(ctx, rpc, cfg)
}

// newClientWithRPC wires a Client around any rpcClient (real or fake) and runs
// the initialize handshake.
func newClientWithRPC(ctx context.Context, rpc rpcClient, cfg ServerConfig) (*Client, error) {
	c := &Client{
		rpc:             rpc,
		docs:            newDocStore(),
		diag:            newDiagBuffer(cfg.FirstWait, cfg.SettleWait),
		root:            cfg.RootDir,
		shutdownTimeout: cfg.ShutdownTimeout,
	}
	if c.shutdownTimeout <= 0 {
		c.shutdownTimeout = DefaultShutdownTimeout
	}
	c.rpc.RegisterNotificationHandler("textDocument/publishDiagnostics",
		func(_ context.Context, _ string, params json.RawMessage) {
			var p protocol.PublishDiagnosticsParams
			if err := json.Unmarshal(params, &p); err != nil {
				return
			}
			c.diag.publish(p)
		})
	if err := c.rpc.Initialize(ctx, false); err != nil {
		return nil, fmt.Errorf("lsp: initialize: %w", err)
	}
	return c, nil
}

// URI returns the file URI this Client uses for a document name (relative names
// are resolved under the configured RootDir).
func (c *Client) URI(name string) string { return uriFor(c.root, name) }

// Open registers an in-memory document and notifies the server.
func (c *Client) Open(ctx context.Context, uri, languageID, text string) error {
	if !c.rpc.IsRunning() {
		return ErrServerDown
	}
	// Drop any diagnostics left over from a previous open of this URI so a
	// reopen (which resets the document version to 1) cannot match a stale
	// version-1 snapshot.
	c.diag.forget(uri)
	version := c.docs.open(uri)
	if err := c.rpc.NotifyDidOpenTextDocument(ctx, uri, languageID, version, text); err != nil {
		return fmt.Errorf("lsp: didOpen: %w", err)
	}
	return nil
}

// Update replaces a document's text (whole-document change) and notifies the
// server. Returns the new document version.
func (c *Client) Update(ctx context.Context, uri, text string) (int, error) {
	if !c.rpc.IsRunning() {
		return 0, ErrServerDown
	}
	version := c.docs.update(uri)
	if version == 0 {
		return 0, fmt.Errorf("lsp: update: document %q not open", uri)
	}
	// Diagnostics for the previous version are now stale; drop them so a
	// slow server can't have them returned as if they applied to this edit.
	c.diag.forget(uri)
	changes := []protocol.TextDocumentContentChangeEvent{
		{Value: protocol.TextDocumentContentChangeWholeDocument{Text: text}},
	}
	if err := c.rpc.NotifyDidChangeTextDocument(ctx, uri, version, changes); err != nil {
		return version, fmt.Errorf("lsp: didChange: %w", err)
	}
	return version, nil
}

// Close stops tracking a document and notifies the server.
func (c *Client) Close(ctx context.Context, uri string) error {
	c.docs.close(uri)
	c.diag.forget(uri)
	if !c.rpc.IsRunning() {
		return nil
	}
	if err := c.rpc.NotifyDidCloseTextDocument(ctx, uri); err != nil {
		return fmt.Errorf("lsp: didClose: %w", err)
	}
	return nil
}

// OnDiagnostics registers a callback fired on every publishDiagnostics. Pass
// nil to clear.
func (c *Client) OnDiagnostics(cb func(uri string, version int32, diags []Diagnostic)) {
	c.diag.onChange(cb)
}

// Diagnostics returns the latest buffered diagnostics for uri.
func (c *Client) Diagnostics(uri string) []Diagnostic { return c.diag.snapshot(uri) }

// WaitForDiagnostics waits for diagnostics for the current version of uri,
// returning a best-effort snapshot. On timeout it returns the snapshot plus
// ErrDiagnosticsTimeout. The document must be open.
func (c *Client) WaitForDiagnostics(ctx context.Context, uri string, timeout time.Duration) ([]Diagnostic, error) {
	if !c.rpc.IsRunning() {
		return nil, ErrServerDown
	}
	version, ok := c.docs.version(uri)
	if !ok {
		return nil, fmt.Errorf("lsp: WaitForDiagnostics: document %q not open", uri)
	}
	return c.diag.wait(ctx, uri, version, timeout)
}

// Shutdown gracefully stops the server, falling back to Kill on timeout.
func (c *Client) Shutdown(ctx context.Context) error {
	sctx, cancel := context.WithTimeout(ctx, c.shutdownTimeout)
	defer cancel()
	if err := c.rpc.Shutdown(sctx); err != nil {
		c.rpc.Kill()
		return fmt.Errorf("lsp: shutdown: %w", err)
	}
	if err := c.rpc.Exit(); err != nil {
		return fmt.Errorf("lsp: shutdown: exit: %w", err)
	}
	return nil
}

// Hover returns hover markdown at a 1-based (line, col), or "" if none.
//
// The client advertises contentFormat support during initialization, so
// compliant servers return MarkupContent. Servers that return plain strings
// or MarkedStrings will surface an error from the underlying RPC layer.
func (c *Client) Hover(ctx context.Context, uri string, line, col int) (string, error) {
	if !c.rpc.IsRunning() {
		return "", ErrServerDown
	}
	h, err := c.rpc.RequestHover(ctx, uri, toPosition(line, col))
	if err != nil {
		return "", fmt.Errorf("lsp: hover: %w", err)
	}
	if h == nil {
		return "", nil
	}
	return h.Contents.Value, nil
}

// Symbols returns the document outline for uri. The URI is converted to a
// filesystem path before sending to the server because powernap's
// RequestDocumentSymbols accepts a filepath; callers should still use
// c.URI(name) to construct a valid uri argument.
func (c *Client) Symbols(ctx context.Context, uri string) ([]Symbol, error) {
	if !c.rpc.IsRunning() {
		return nil, ErrServerDown
	}
	res, err := c.rpc.RequestDocumentSymbols(ctx, pathFromURI(uri))
	if err != nil {
		return nil, fmt.Errorf("lsp: symbols: %w", err)
	}
	syms := make([]Symbol, 0, len(res))
	for _, r := range res {
		switch v := r.(type) {
		case *protocol.DocumentSymbol:
			// DocumentSymbol responses are hierarchical (e.g. a class's
			// methods nest under it); flatten the whole tree so nested
			// symbols stay visible in the outline.
			syms = appendDocumentSymbol(syms, v)
		case *protocol.SymbolInformation:
			syms = append(syms, Symbol{Name: v.Name, Line: int(v.Location.Range.Start.Line) + 1, Kind: int(v.Kind)})
		default:
			// Unknown concrete type: fall back to the interface accessors.
			syms = append(syms, Symbol{Name: r.GetName(), Line: int(r.GetRange().Start.Line) + 1})
		}
	}
	return syms, nil
}

// appendDocumentSymbol appends ds and all of its descendants (depth-first) to
// dst as flat Symbol entries, so a hierarchical DocumentSymbol outline is not
// truncated to just its top-level entries.
func appendDocumentSymbol(dst []Symbol, ds *protocol.DocumentSymbol) []Symbol {
	dst = append(dst, Symbol{Name: ds.Name, Line: int(ds.Range.Start.Line) + 1, Kind: int(ds.Kind)})
	for i := range ds.Children {
		dst = appendDocumentSymbol(dst, &ds.Children[i])
	}
	return dst
}

// Completion returns up to limit completion labels at a 1-based (line, col).
// A non-positive limit means no cap.
func (c *Client) Completion(ctx context.Context, uri string, line, col, limit int) (labels []string, truncated int, err error) {
	if !c.rpc.IsRunning() {
		return nil, 0, ErrServerDown
	}
	list, err := c.rpc.RequestCompletion(ctx, uri, toPosition(line, col))
	if err != nil {
		return nil, 0, fmt.Errorf("lsp: completion: %w", err)
	}
	if list == nil {
		return nil, 0, nil
	}
	for _, it := range list.Items {
		labels = append(labels, it.Label)
	}
	if limit > 0 && len(labels) > limit {
		truncated = len(labels) - limit
		labels = labels[:limit]
	}
	return labels, truncated, nil
}
