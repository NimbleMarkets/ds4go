// Package ds4 provides Go-native conveniences for the ds4 inference engine.
//
// The lower-level github.com/NimbleMarkets/ds4go/ds4api package is the strict
// purego wrapper around ds4.h. This package owns runtime policy such as default
// paths, friendly diagnostics, and small convenience entry points.
package ds4

import (
	"fmt"
	"io"
	"sync"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

var defaultLibraryMu sync.Mutex
var defaultLibrary *ds4api.Library

type (
	// Library is a loaded libds4 shared library.
	Library = ds4api.Library
	// Engine wraps a ds4_engine.
	Engine = ds4api.Engine
	// Session wraps a ds4_session.
	Session = ds4api.Session
	// Tokens owns a ds4_tokens value allocated by libds4.
	Tokens = ds4api.Tokens
	// EngineOptions configures ds4_engine_open.
	EngineOptions = ds4api.EngineOptions
	// ContextMemory is ds4_context_memory.
	ContextMemory = ds4api.ContextMemory
	// TokenScore is ds4_token_score.
	TokenScore = ds4api.TokenScore
	// Backend selects the accelerator implementation compiled into libds4.
	Backend = ds4api.Backend
	// ThinkMode controls ds4's rendered chat thinking mode.
	ThinkMode = ds4api.ThinkMode
	// SessionRewriteResult is returned by session rewrite helpers.
	SessionRewriteResult = ds4api.SessionRewriteResult
	// LogType is the category used by libds4 diagnostics.
	LogType = ds4api.LogType
	// LogFunc receives one complete libds4 diagnostic message.
	LogFunc = ds4api.LogFunc
	// AbortFunc receives a libds4 fatal-invariant message immediately before abort.
	AbortFunc = ds4api.AbortFunc
	// TokenEmitFunc is called when ds4 emits a generated token.
	TokenEmitFunc = ds4api.TokenEmitFunc
	// GenerationDoneFunc is called after ds4 completes generation.
	GenerationDoneFunc = ds4api.GenerationDoneFunc
	// ProgressFunc receives ds4 progress events.
	ProgressFunc = ds4api.ProgressFunc
	// ArgmaxGenerateOptions controls ds4_engine_generate_argmax.
	ArgmaxGenerateOptions = ds4api.ArgmaxGenerateOptions
)

const (
	// DefaultTemperature is ds4's default sampling temperature.
	DefaultTemperature = ds4api.DefaultTemperature
	// DefaultTopP is ds4's default nucleus sampling probability.
	DefaultTopP = ds4api.DefaultTopP
	// DefaultMinP is ds4's default minimum relative-probability filter.
	DefaultMinP = ds4api.DefaultMinP

	// BackendMetal selects the Metal backend.
	BackendMetal = ds4api.BackendMetal
	// BackendCUDA selects the CUDA backend.
	BackendCUDA = ds4api.BackendCUDA
	// BackendCPU selects the CPU reference backend.
	BackendCPU = ds4api.BackendCPU

	// ThinkNone disables thinking markers in chat prompts.
	ThinkNone = ds4api.ThinkNone
	// ThinkHigh enables ordinary high-effort thinking.
	ThinkHigh = ds4api.ThinkHigh
	// ThinkMax requests maximum-effort thinking. ds4 may downgrade it to
	// ThinkHigh when the context is below ThinkMaxMinContext.
	ThinkMax = ds4api.ThinkMax

	// SessionRewriteError means the rewrite failed.
	SessionRewriteError = ds4api.SessionRewriteError
	// SessionRewriteOK means the rewrite completed in place.
	SessionRewriteOK = ds4api.SessionRewriteOK
	// SessionRewriteRebuildNeeded means the caller should restore or rebuild
	// the session state.
	SessionRewriteRebuildNeeded = ds4api.SessionRewriteRebuildNeeded

	// LogDefault is the default ds4 log style.
	LogDefault = ds4api.LogDefault
	// LogPrefill marks prefill messages.
	LogPrefill = ds4api.LogPrefill
	// LogGeneration marks generation messages.
	LogGeneration = ds4api.LogGeneration
	// LogKVCache marks KV-cache messages.
	LogKVCache = ds4api.LogKVCache
	// LogTool marks tool-calling messages.
	LogTool = ds4api.LogTool
	// LogWarning marks warnings.
	LogWarning = ds4api.LogWarning
	// LogTiming marks timing messages.
	LogTiming = ds4api.LogTiming
	// LogOK marks successful status messages.
	LogOK = ds4api.LogOK
	// LogError marks errors.
	LogError = ds4api.LogError

	// DefaultMTPDraftTokens is the default number of draft tokens speculative
	// decoding generates per step when MTP is enabled. A value of 0 disables
	// speculative decoding; set it explicitly to enable MTP.
	DefaultMTPDraftTokens = 0
	// DefaultMTPMargin is the default minimum margin (in tokens) between the
	// draft model's accepted sequence and the full target model output.
	DefaultMTPMargin = 3
)

// Load loads libds4 using ds4go's runtime path policy.
//
// Passing an empty path searches DS4_LIB, DS4_DIR/lib, executable-local
// library locations, and finally the platform loader path. The current
// working directory is not searched; see DefaultLibraryPath.
func Load(path string) (*ds4api.Library, error) {
	if path == "" {
		path = DefaultLibraryPath()
	}
	if path == "" {
		return nil, fmt.Errorf("ds4go: could not find %s; set DS4_LIB or DS4_DIR", libraryFileName())
	}
	return ds4api.Load(path)
}

// SetDefaultLibrary makes lib the low-level package default library.
func SetDefaultLibrary(lib *ds4api.Library) {
	defaultLibraryMu.Lock()
	defaultLibrary = lib
	defaultLibraryMu.Unlock()
	ds4api.SetDefaultLibrary(lib)
}

// SetLogFunc redirects libds4 diagnostics for the default library.
//
// This includes engine diagnostics and Metal/CUDA backend diagnostics routed
// through ds4_gpu_log. Passing nil restores libds4's native stderr logger. The
// logger is global inside libds4, so install it once during application
// startup.
func SetLogFunc(fn LogFunc) error {
	lib, err := defaultCallbackLibrary(fn != nil)
	if err != nil {
		return err
	}
	if lib == nil {
		return nil
	}
	return lib.SetLogFunc(fn)
}

// SetAbortFunc installs a last-chance libds4 fatal-invariant callback.
//
// libds4 invokes the callback after logging the fatal message and immediately
// before native abort(). Passing nil restores the default behavior. This hook
// is process-global inside libds4 and is intended for crash telemetry, flushing
// logs, or deliberate process termination; returning from the callback does not
// recover the engine.
func SetAbortFunc(fn AbortFunc) error {
	lib, err := defaultCallbackLibrary(fn != nil)
	if err != nil {
		return err
	}
	if lib == nil {
		return nil
	}
	return lib.SetAbortFunc(fn)
}

// SetLogOutput redirects libds4 diagnostics to w.
//
// This includes engine diagnostics and Metal/CUDA backend diagnostics routed
// through ds4_gpu_log. Passing nil restores libds4's native stderr logger.
// Non-nil writers receive the exact message emitted by libds4, including its
// prefix and trailing newline. Writes are serialized because libds4 may call
// the logger from native worker threads. Write errors are ignored because the
// native callback cannot report them.
func SetLogOutput(w io.Writer) error {
	if w == nil {
		return SetLogFunc(nil)
	}
	var mu sync.Mutex
	return SetLogFunc(func(_ LogType, msg string) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = io.WriteString(w, msg)
	})
}

// DiscardLogs discards libds4 diagnostics routed through ds4_log_set.
//
// This includes Metal/CUDA diagnostics routed through ds4_gpu_log. Native code
// paths that still write directly to stderr are unaffected.
func DiscardLogs() error {
	return SetLogOutput(io.Discard)
}

// NewEngine loads the default libds4 shared library and opens a ds4 engine.
func NewEngine(opts ds4api.EngineOptions) (*ds4api.Engine, error) {
	lib, err := Load("")
	if err != nil {
		return nil, err
	}
	SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(opts)
	if err != nil {
		return nil, EnrichEngineOpenError(err)
	}
	return engine, nil
}

func defaultCallbackLibrary(load bool) (*ds4api.Library, error) {
	defaultLibraryMu.Lock()
	lib := defaultLibrary
	defaultLibraryMu.Unlock()
	if lib != nil {
		return lib, nil
	}
	if !load {
		return nil, nil
	}
	lib, err := Load("")
	if err != nil {
		return nil, err
	}
	SetDefaultLibrary(lib)
	return lib, nil
}

// ApplyMTPDefaults populates MTPPath, MTPDraftTokens, and MTPMargin with
// sensible defaults when an MTP model is installed. It only fills fields
// that are currently empty or zero, so explicit caller settings are respected.
func ApplyMTPDefaults(opts *EngineOptions) {
	if opts == nil {
		return
	}
	if opts.MTPPath == "" {
		opts.MTPPath = DefaultMTPPath()
	}
	if opts.MTPPath != "" && opts.MTPDraftTokens <= 0 {
		opts.MTPDraftTokens = DefaultMTPDraftTokens
	}
	if opts.MTPPath != "" && opts.MTPMargin <= 0 {
		opts.MTPMargin = DefaultMTPMargin
	}
}
