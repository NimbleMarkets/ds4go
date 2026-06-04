// Package ds4 provides Go-native conveniences for the ds4 inference engine.
//
// The lower-level github.com/NimbleMarkets/ds4go/ds4api package is the strict
// purego wrapper around ds4.h. This package owns runtime policy such as default
// paths, friendly diagnostics, and small convenience entry points.
package ds4

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/install"
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

// SetStderr redirects libds4's diagnostic output to f for the default library.
// Passing nil restores the native stderr.
//
// libds4 dups the descriptor internally and writes its diagnostics there
// unbuffered, so f may be closed once it is no longer the active target. The
// redirect target is process-global inside libds4; install it once at startup,
// before generation is active. Calling Fd on f detaches it from the Go runtime
// poller and puts it in blocking mode, so do not pass a file you also use for
// asynchronous I/O.
//
// Not supported on Windows; see SetStderrFd.
func SetStderr(f *os.File) error {
	if f == nil {
		return SetStderrFd(-1)
	}
	return SetStderrFd(int(f.Fd()))
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

// DiscardLogs redirects libds4's diagnostic output to the null device for the
// default library. The native stderr is restored by SetStderr(nil).
//
// Not supported on Windows; see SetStderrFd.
func DiscardLogs() error {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return SetStderr(f)
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

// ProcessInfo represents a process holding or using a ds4 resource.
type ProcessInfo struct {
	PID  int
	Name string
}

// LibraryHolders returns a list of processes currently holding onto the libds4 shared library.
// If libPath is empty, it uses the default library path. To ensure security, queries are restricted
// to paths within authorized directories (DefaultDir, executable directory, or explicit DS4_LIB).
func LibraryHolders(libPath string) ([]ProcessInfo, error) {
	if libPath == "" {
		libPath = DefaultLibraryPath()
	}
	if libPath == "" {
		return nil, fmt.Errorf("ds4go: default library path not resolved")
	}

	if !isPathWithinSafeDomain(libPath) {
		return nil, fmt.Errorf("ds4go: query path %q is outside the authorized security domain", libPath)
	}

	procs, err := install.FindLibraryHolders(libPath)
	if err != nil {
		return nil, err
	}

	var res []ProcessInfo
	for _, p := range procs {
		res = append(res, ProcessInfo{PID: p.PID, Name: p.Name})
	}
	return res, nil
}

// EngineHolders returns a map of process PIDs to the list of model files they are currently running.
// If modelsDir is empty, it uses the default models directory. To ensure security, queries are restricted
// to paths within authorized directories (DefaultDir or executable directory).
func EngineHolders(modelsDir string) (map[int][]string, error) {
	if modelsDir == "" {
		modelsDir = DefaultModelsDir()
	}

	if !isPathWithinSafeDomain(modelsDir) {
		return nil, fmt.Errorf("ds4go: query directory %q is outside the authorized security domain", modelsDir)
	}

	holders, err := install.FindDirHolders(modelsDir)
	if err != nil {
		return nil, err
	}
	return holders, nil
}

func isPathWithinSafeDomain(path string) bool {
	if path == "" {
		return false
	}
	// Check if it is a bare library filename (system loader path)
	if filepath.Base(path) == path && (path == "libds4.dylib" || path == "libds4.dll" || path == "libds4.so") {
		return true
	}

	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}

	// 1. Check if it is within DefaultDir()
	defaultDir, err := filepath.Abs(DefaultDir())
	if err == nil {
		if absPath == defaultDir || strings.HasPrefix(absPath, defaultDir+string(filepath.Separator)) {
			return true
		}
	}

	// 2. Check if it is in the executable directory
	if exe, err := os.Executable(); err == nil {
		exeDir, err := filepath.Abs(filepath.Dir(exe))
		if err == nil {
			if absPath == exeDir || strings.HasPrefix(absPath, exeDir+string(filepath.Separator)) {
				return true
			}
		}
	}

	// 3. Check if it matches DS4_LIB
	if ds4Lib := os.Getenv("DS4_LIB"); ds4Lib != "" {
		absDs4Lib, err := filepath.Abs(filepath.Clean(ds4Lib))
		if err == nil && absPath == absDs4Lib {
			return true
		}
	}

	return false
}
