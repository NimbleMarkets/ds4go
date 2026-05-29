package lsp

import (
	"context"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/charmbracelet/x/powernap/pkg/transport"
)

// fakeRPC is a test double for rpcClient. Tests set the *Result fields and read
// the recorded call fields.
type fakeRPC struct {
	running       bool
	initErr       error
	notifyHandler transport.NotificationHandler

	openedURI, changedURI, closedURI string
	openText, changeText             string
	openVersion, changeVersion       int

	hoverResult  *protocol.Hover
	hoverErr     error
	symbolResult []protocol.DocumentSymbolResult
	symbolErr    error
	complResult  *protocol.CompletionList
	complErr     error

	shutdownCalled, exitCalled, killCalled bool
}

func newFakeRPC() *fakeRPC { return &fakeRPC{running: true} }

func (f *fakeRPC) Initialize(context.Context, bool) error { return f.initErr }
func (f *fakeRPC) Shutdown(context.Context) error         { f.shutdownCalled = true; return nil }
func (f *fakeRPC) Exit() error                            { f.exitCalled = true; return nil }
func (f *fakeRPC) Kill()                                  { f.killCalled = true }
func (f *fakeRPC) IsRunning() bool                        { return f.running }

func (f *fakeRPC) RegisterNotificationHandler(_ string, h transport.NotificationHandler) {
	f.notifyHandler = h
}

func (f *fakeRPC) NotifyDidOpenTextDocument(_ context.Context, uri, _ string, version int, text string) error {
	f.openedURI, f.openVersion, f.openText = uri, version, text
	return nil
}
func (f *fakeRPC) NotifyDidChangeTextDocument(_ context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	f.changedURI, f.changeVersion = uri, version
	if len(changes) == 1 {
		if w, ok := changes[0].Value.(protocol.TextDocumentContentChangeWholeDocument); ok {
			f.changeText = w.Text
		}
	}
	return nil
}
func (f *fakeRPC) NotifyDidCloseTextDocument(_ context.Context, uri string) error {
	f.closedURI = uri
	return nil
}
func (f *fakeRPC) RequestHover(context.Context, string, protocol.Position) (*protocol.Hover, error) {
	return f.hoverResult, f.hoverErr
}
func (f *fakeRPC) RequestCompletion(context.Context, string, protocol.Position) (*protocol.CompletionList, error) {
	return f.complResult, f.complErr
}
func (f *fakeRPC) RequestDocumentSymbols(context.Context, string) ([]protocol.DocumentSymbolResult, error) {
	return f.symbolResult, f.symbolErr
}

var _ rpcClient = (*fakeRPC)(nil)
