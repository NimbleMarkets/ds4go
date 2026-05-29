package lsp

import (
	"errors"
	"time"
)

// Sentinel errors returned by Client.
var (
	// ErrServerDown is returned by query methods when the language server
	// process is not running. v1 does not auto-restart; recreate the Client.
	ErrServerDown = errors.New("lsp: language server not running")
	// ErrDiagnosticsTimeout is returned by WaitForDiagnostics when the wait
	// elapses before diagnostics settle. The returned snapshot is best-effort.
	ErrDiagnosticsTimeout = errors.New("lsp: timed out waiting for diagnostics")
)

// ServerConfig describes how to launch and initialize a language server.
type ServerConfig struct {
	Command     string            // executable, looked up on PATH
	Args        []string          // process arguments
	RootDir     string            // workspace root (absolute path); "" allowed
	InitOptions map[string]any    // LSP initializationOptions
	Settings    map[string]any    // workspace/configuration settings
	Environment map[string]string // extra environment variables

	// Timeout is the per-request timeout passed to the underlying RPC client.
	// Zero means no timeout.
	Timeout time.Duration

	// ShutdownTimeout bounds the graceful shutdown handshake before the server
	// is force-killed. Zero falls back to DefaultShutdownTimeout. This is
	// deliberately separate from Timeout (the per-request RPC timeout) so the
	// shutdown grace period can be tuned without affecting request latency.
	ShutdownTimeout time.Duration

	// FirstWait and SettleWait tune the time-settle fallback used by
	// WaitForDiagnostics when the server does not report a document version.
	// FirstWait also bounds how long WaitForDiagnostics waits for the first
	// publish before assuming a silent server is clean; once the server has
	// published a versioned diagnostic, the version-correlated wait honors the
	// full timeout instead. Set FirstWait above your server's cold first-
	// publish latency to avoid a premature "clean" result. Zero values fall
	// back to defaults (DefaultFirstWait/DefaultSettleWait).
	FirstWait  time.Duration
	SettleWait time.Duration
}

// Defaults for the diagnostics time-settle fallback.
const (
	// DefaultFirstWait bounds how long WaitForDiagnostics waits for the first
	// publish before assuming a silent server is clean. This is a tradeoff: too
	// low and a cold server that has not yet published its first diagnostics
	// gets a freshly-opened broken file reported as clean; too high and a server
	// that simply stays silent on clean files makes every first wait block this
	// long. 5s clears the cold first-publish latency of common servers (gopls,
	// LuaLS) for a single small file. Servers with heavier cold starts should
	// raise ServerConfig.FirstWait.
	DefaultFirstWait  = 5 * time.Second
	DefaultSettleWait = 300 * time.Millisecond
)

// DefaultShutdownTimeout bounds the graceful shutdown handshake when
// ServerConfig.ShutdownTimeout is unset.
const DefaultShutdownTimeout = 5 * time.Second

// Severity classifies a Diagnostic.
type Severity int

const (
	SeverityError Severity = iota + 1
	SeverityWarning
	SeverityInformation
	SeverityHint
)

func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInformation:
		return "information"
	case SeverityHint:
		return "hint"
	default:
		return "unknown"
	}
}

// Diagnostic is a single problem reported for a document. Line and Col are
// 1-based (LSP wire positions are 0-based; we convert at the boundary).
type Diagnostic struct {
	Line     int
	Col      int
	Severity Severity
	Message  string
	Source   string
}

// Symbol is one entry from a document outline. Line is 1-based.
type Symbol struct {
	Name string
	Line int
	Kind int // LSP SymbolKind (e.g. 12 = Function, 13 = Variable)
}
