// Package ds4 provides Go-native conveniences for the ds4 inference engine.
//
// The lower-level github.com/NimbleMarkets/ds4go/ds4api package is the strict
// purego wrapper around ds4.h. This package owns runtime policy such as default
// paths, friendly diagnostics, and small convenience entry points.
package ds4

import (
	"fmt"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

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
	// Backend selects the accelerator implementation compiled into libds4.
	Backend = ds4api.Backend
	// ThinkMode controls ds4's rendered chat thinking mode.
	ThinkMode = ds4api.ThinkMode
	// LogType is the category used by libds4 diagnostics.
	LogType = ds4api.LogType
	// LogFunc receives one complete libds4 diagnostic message.
	LogFunc = ds4api.LogFunc
	// TokenEmitFunc is called when ds4 emits a generated token.
	TokenEmitFunc = ds4api.TokenEmitFunc
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
	ds4api.SetDefaultLibrary(lib)
}

// SetLogFunc redirects libds4 diagnostics for the default library.
//
// Passing nil restores libds4's native stderr logger. The logger is global
// inside libds4, so install it once during application startup.
func SetLogFunc(fn LogFunc) error {
	return ds4api.SetLogFunc(fn)
}

// NewEngine loads the default libds4 shared library and opens a ds4 engine.
func NewEngine(opts ds4api.EngineOptions) (*ds4api.Engine, error) {
	lib, err := Load("")
	if err != nil {
		return nil, err
	}
	ds4api.SetDefaultLibrary(lib)
	engine, err := lib.NewEngine(opts)
	if err != nil {
		return nil, EnrichEngineOpenError(err)
	}
	return engine, nil
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
