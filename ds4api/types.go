// Package ds4api provides pure-Go bindings for the ds4 inference engine.
//
// The package does not use cgo. It loads a user-provided libds4 shared
// library at runtime through purego and wraps the public API from ds4.h.
package ds4api

import "fmt"

// Sampling defaults mirror the DS4_DEFAULT_* macros in ds4.h. The default
// sampler keeps top-p at 1.0 and uses min-p as the active filter.
const (
	// DefaultTemperature is ds4's default sampling temperature.
	DefaultTemperature float32 = 1.0
	// DefaultTopP is ds4's default nucleus sampling probability.
	DefaultTopP float32 = 1.0
	// DefaultMinP is ds4's default minimum relative-probability filter.
	DefaultMinP float32 = 0.05
)

// Backend selects the accelerator implementation compiled into libds4.
type Backend int32

const (
	// BackendMetal selects the Metal backend.
	BackendMetal Backend = iota
	// BackendCUDA selects the CUDA backend.
	BackendCUDA
	// BackendCPU selects the CPU reference backend.
	BackendCPU
)

// ThinkMode controls ds4's rendered chat thinking mode.
type ThinkMode int32

const (
	// ThinkNone disables thinking markers in chat prompts.
	ThinkNone ThinkMode = iota
	// ThinkHigh enables ordinary high-effort thinking.
	ThinkHigh
	// ThinkMax requests maximum-effort thinking. ds4_think_mode_for_context
	// may downgrade it to ThinkHigh when the context is below ThinkMaxMinContext.
	ThinkMax
)

// LogType is the category used by ds4_log.
type LogType int32

const (
	// LogDefault is the default ds4 log style.
	LogDefault LogType = iota
	// LogPrefill marks prefill messages.
	LogPrefill
	// LogGeneration marks generation messages.
	LogGeneration
	// LogKVCache marks KV-cache messages.
	LogKVCache
	// LogTool marks tool-calling messages.
	LogTool
	// LogWarning marks warnings.
	LogWarning
	// LogTiming marks timing messages.
	LogTiming
	// LogOK marks successful status messages.
	LogOK
	// LogError marks errors.
	LogError
)

// SessionRewriteResult is returned by ds4 session rewrite helpers.
type SessionRewriteResult int32

const (
	// SessionRewriteError means the rewrite failed.
	SessionRewriteError SessionRewriteResult = -1
	// SessionRewriteOK means the rewrite completed in place.
	SessionRewriteOK SessionRewriteResult = 0
	// SessionRewriteRebuildNeeded means the caller should restore or rebuild the session state.
	SessionRewriteRebuildNeeded SessionRewriteResult = 1
)

// EngineOptions configures ds4_engine_open.
type EngineOptions struct {
	// ModelPath is the path to the DeepSeek V4 Flash GGUF model.
	ModelPath string
	// MTPPath is the optional MTP draft model path.
	MTPPath string
	// Backend selects Metal, CUDA, or CPU according to the libds4 build.
	Backend Backend
	// NThreads controls CPU worker threads when the backend uses them.
	NThreads int
	// MTPDraftTokens controls speculative draft length.
	MTPDraftTokens int
	// MTPMargin controls speculative acceptance confidence.
	MTPMargin float32
	// DirectionalSteeringFile points at an optional directional steering file.
	DirectionalSteeringFile string
	// DirectionalSteeringAttn scales directional steering in attention blocks.
	DirectionalSteeringAttn float32
	// DirectionalSteeringFFN scales directional steering in FFN blocks.
	DirectionalSteeringFFN float32
	// WarmWeights asks ds4 to warm model weights after load.
	WarmWeights bool
	// Quality requests ds4's quality-oriented execution path where supported.
	Quality bool
}

// ContextMemory is ds4_context_memory.
type ContextMemory struct {
	// TotalBytes is the estimated total context memory.
	TotalBytes uint64
	// RawBytes is the raw KV-cache memory estimate.
	RawBytes uint64
	// CompressedBytes is the compressed KV-cache memory estimate.
	CompressedBytes uint64
	// ScratchBytes is the temporary scratch memory estimate.
	ScratchBytes uint64
	// PrefillCap is the prefill capacity.
	PrefillCap uint32
	// RawCap is the raw KV-cache row capacity.
	RawCap uint32
	// CompCap is the compressed KV-cache row capacity.
	CompCap uint32
}

// TokenScore is ds4_token_score.
type TokenScore struct {
	// ID is the token id.
	ID int
	// Logit is the raw model logit.
	Logit float32
	// Logprob is the log probability for the token.
	Logprob float32
}

// TokenEmitFunc is called when ds4 emits a generated token.
type TokenEmitFunc func(token int)

// GenerationDoneFunc is called after ds4 completes generation.
type GenerationDoneFunc func()

// ProgressFunc receives ds4 progress events.
type ProgressFunc func(event string, current, total int)

// ArgmaxGenerateOptions controls ds4_engine_generate_argmax.
type ArgmaxGenerateOptions struct {
	// NPredict is the number of tokens to generate.
	NPredict int
	// CtxSize is the context size used for this generation.
	CtxSize int
	// OnToken streams generated tokens.
	OnToken TokenEmitFunc
	// OnDone is called by ds4 when generation is complete.
	OnDone GenerationDoneFunc
	// OnProgress receives ds4 progress events.
	OnProgress ProgressFunc
}

func ds4Error(op string, code int32) error {
	if code == 0 {
		return nil
	}
	return fmt.Errorf("%s failed with ds4 status %d", op, code)
}
