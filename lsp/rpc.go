package lsp

import (
	"context"

	pnlsp "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/charmbracelet/x/powernap/pkg/transport"
)

// rpcClient is the subset of *pnlsp.Client that Client depends on. Defining it
// as an interface lets unit tests inject a fake without spawning a subprocess.
type rpcClient interface {
	Initialize(ctx context.Context, enableSnippets bool) error
	Shutdown(ctx context.Context) error
	Exit() error
	Kill()
	IsRunning() bool
	RegisterNotificationHandler(method string, handler transport.NotificationHandler)
	NotifyDidOpenTextDocument(ctx context.Context, uri, languageID string, version int, text string) error
	NotifyDidChangeTextDocument(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error
	NotifyDidCloseTextDocument(ctx context.Context, uri string) error
	// Asymmetric argument types below are intentional and mirror powernap's
	// own API, not a mistake: RequestHover and RequestCompletion take a file
	// URI and pass it through unchanged, whereas RequestDocumentSymbols takes a
	// filesystem path and re-encodes it internally via protocol.URIFromPath.
	// Client.Symbols therefore converts URI->path (pathFromURI) before calling
	// the last one, while Hover/Completion forward the URI directly. Keep the
	// parameter names (uri vs filepath) in sync with this distinction.
	RequestHover(ctx context.Context, uri string, position protocol.Position) (*protocol.Hover, error)
	RequestCompletion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error)
	RequestDocumentSymbols(ctx context.Context, filepath string) ([]protocol.DocumentSymbolResult, error)
}

// compile-time check that the real powernap client satisfies rpcClient.
var _ rpcClient = (*pnlsp.Client)(nil)
