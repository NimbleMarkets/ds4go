package ds4api

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

// Engine wraps a ds4_engine.
type Engine struct {
	lib      *Library
	ptr      uintptr
	once     sync.Once
	runLock  *models.FileLock
	lockPath string
	cleanup  runtime.Cleanup
}

// NewEngine opens a ds4 engine using the default shared library.
func NewEngine(opts EngineOptions) (*Engine, error) {
	lib, err := DefaultLibrary()
	if err != nil {
		return nil, err
	}
	return lib.NewEngine(opts)
}

// NewEngine opens a ds4 engine using this shared library.
func (l *Library) NewEngine(opts EngineOptions) (*Engine, error) {
	var runLock *models.FileLock
	var lockPath string
	if opts.ModelPath != "" && !opts.InspectOnly {
		lockPath = opts.ModelPath + ".run.lock"
		var err error
		runLock, err = models.AcquireEngineRunLock(opts.ModelPath)
		if err != nil {
			return nil, err
		}
	}

	modelBytes, modelPtr := cStringPointer(opts.ModelPath)
	mtpBytes, mtpPtr := cStringPointer(opts.MTPPath)
	visionBytes, visionPtr := cStringPointer(opts.VisionPath)
	steerBytes, steerPtr := cStringPointer(opts.DirectionalSteeringFile)
	expertProfileBytes, expertProfilePtr := cStringPointer(opts.ExpertProfilePath)
	listenHostBytes, listenHostPtr := cStringPointer(opts.Distributed.ListenHost)
	coordHostBytes, coordHostPtr := cStringPointer(opts.Distributed.CoordinatorHost)
	copts := cEngineOptions{
		ModelPath:                 modelPtr,
		MTPPath:                   mtpPtr,
		VisionPath:                visionPtr,
		Backend:                   opts.Backend,
		NThreads:                  int32(opts.NThreads),
		ContextSize:               int32(opts.ContextSize),
		PrefillChunk:              opts.PrefillChunk,
		MTPDraftTokens:            int32(opts.MTPDraftTokens),
		MTPMargin:                 opts.MTPMargin,
		GLMMTP:                    opts.GLMMTP,
		GLMMTPTiming:              opts.GLMMTPTiming,
		Dspark:                    opts.Dspark,
		DsparkStrict:              opts.DsparkStrict,
		DsparkExactSampling:       opts.DsparkExactSampling,
		DsparkConfidenceThreshold: opts.DsparkConfidenceThreshold,
		// ds4 only reads the threshold when its companion flag is set, so a
		// caller-supplied value would otherwise be silently ignored.
		DsparkConfidenceThresholdSet: opts.DsparkConfidenceThreshold != 0,
		DirectionalSteeringFile:      steerPtr,
		ExpertProfilePath:            expertProfilePtr,
		DirectionalSteeringAttn:      opts.DirectionalSteeringAttn,
		DirectionalSteeringFFN:       opts.DirectionalSteeringFFN,
		PowerPercent:                 int32(opts.PowerPercent),
		SSDStreamingCacheExperts:     opts.SSDStreamingCacheExperts,
		SSDStreamingCacheBytes:       opts.SSDStreamingCacheBytes,
		SSDStreamingPreloadExperts:   opts.SSDStreamingPreloadExperts,
		SSDStreamingFullLayers:       opts.SSDStreamingFullLayers,
		SSDStreamingFullLayersSet:    opts.SSDStreamingFullLayersSet,
		CUDATensorParallel:           opts.CUDATensorParallel,
		SimulateUsedMemoryBytes:      opts.SimulateUsedMemoryBytes,
		WarmWeights:                  opts.WarmWeights,
		Quality:                      opts.Quality,
		SSDStreaming:                 opts.SSDStreaming,
		SSDStreamingCold:             opts.SSDStreamingCold,
		InspectOnly:                  opts.InspectOnly,
		PlacementCtxHint:             int32(opts.PlacementCtxHint),
		PlacementSessionCountHint:    int32(opts.PlacementSessionCountHint),
		ShareSessionPrefillWorkspace: opts.ShareSessionPrefillWorkspace,
		LoadSlice:                    opts.LoadSlice,
		LoadLayerStart:               opts.LoadLayerStart,
		LoadLayerEnd:                 opts.LoadLayerEnd,
		LoadOutput:                   opts.LoadOutput,
		Distributed: cDistributedOptions{
			Role: int32(opts.Distributed.Role),
			Layers: cDistributedLayers{
				Start:     opts.Distributed.Layers.Start,
				End:       opts.Distributed.Layers.End,
				HasOutput: opts.Distributed.Layers.HasOutput,
				Set:       opts.Distributed.Layers.Set,
			},
			ListenHost:      listenHostPtr,
			ListenPort:      int32(opts.Distributed.ListenPort),
			CoordinatorHost: coordHostPtr,
			CoordinatorPort: int32(opts.Distributed.CoordinatorPort),
			PrefillChunk:    opts.Distributed.PrefillChunk,
			PrefillWindow:   opts.Distributed.PrefillWindow,
			ActivationBits:  opts.Distributed.ActivationBits,
			ReplayCheck:     opts.Distributed.ReplayCheck,
			Debug:           opts.Distributed.Debug,
		},
	}
	var out uintptr
	code := l.raw.ds4EngineOpen(&out, &copts)
	runtime.KeepAlive(modelBytes)
	runtime.KeepAlive(mtpBytes)
	runtime.KeepAlive(visionBytes)
	runtime.KeepAlive(steerBytes)
	runtime.KeepAlive(expertProfileBytes)
	runtime.KeepAlive(listenHostBytes)
	runtime.KeepAlive(coordHostBytes)
	if err := ds4Error("ds4_engine_open", code); err != nil {
		if runLock != nil {
			runLock.Close()
			os.Remove(lockPath)
		}
		return nil, err
	}
	e := &Engine{lib: l, ptr: out, runLock: runLock, lockPath: lockPath}
	e.cleanup = runtime.AddCleanup(e, cleanEngine, engineCleanupArg{
		lib:      l,
		ptr:      out,
		runLock:  runLock,
		lockPath: lockPath,
	})
	return e, nil
}

type engineCleanupArg struct {
	lib      *Library
	ptr      uintptr
	runLock  *models.FileLock
	lockPath string
}

func cleanEngine(arg engineCleanupArg) {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if arg.ptr != 0 {
		arg.lib.raw.ds4EngineClose(arg.ptr)
	}
	if arg.runLock != nil {
		arg.runLock.Close()
		if arg.lockPath != "" {
			os.Remove(arg.lockPath)
		}
	}
}

// Close releases the underlying ds4_engine.
func (e *Engine) Close() {
	if e == nil {
		return
	}
	e.once.Do(func() {
		e.cleanup.Stop()
		libCallMu.Lock()
		defer libCallMu.Unlock()
		if e.ptr != 0 {
			e.lib.raw.ds4EngineClose(e.ptr)
			e.ptr = 0
		}
		if e.runLock != nil {
			e.runLock.Close()
			if e.lockPath != "" {
				os.Remove(e.lockPath)
			}
			e.runLock = nil
		}
	})
}

// require locks the engine and verifies it is open. The returned unlock
// function MUST be called (typically via defer) to release the lock.
func (e *Engine) require() (unlock func(), err error) {
	libCallMu.Lock()
	if e == nil || e.ptr == 0 {
		libCallMu.Unlock()
		return nil, errClosed
	}
	return libCallMu.Unlock, nil
}

// Summary prints ds4's engine summary to its configured output.
func (e *Engine) Summary() error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	e.lib.raw.ds4EngineSummary(e.ptr)
	return nil
}

// BackendName returns ds4's printable name for backend.
func BackendName(backend Backend) string {
	lib, err := DefaultLibrary()
	if err != nil {
		return ""
	}
	return lib.raw.ds4BackendName(backend)
}

// ThinkModeEnabled reports whether mode emits thinking markers.
func ThinkModeEnabled(mode ThinkMode) bool {
	lib, err := DefaultLibrary()
	if err != nil {
		return false
	}
	return lib.raw.ds4ThinkModeEnabled(mode)
}

// ThinkModeName returns ds4's printable name for mode.
func ThinkModeName(mode ThinkMode) string {
	lib, err := DefaultLibrary()
	if err != nil {
		return ""
	}
	return lib.raw.ds4ThinkModeName(mode)
}

// ThinkMaxPrefix returns ds4's maximum-effort thinking prompt prefix.
func ThinkMaxPrefix() string {
	lib, err := DefaultLibrary()
	if err != nil {
		return ""
	}
	return lib.raw.ds4ThinkMaxPrefix()
}

// ThinkMaxMinContext returns the minimum context size ds4 recommends for ThinkMax.
func ThinkMaxMinContext() uint32 {
	lib, err := DefaultLibrary()
	if err != nil {
		return 0
	}
	return lib.raw.ds4ThinkMaxMinContext()
}

// ThinkModeForContext returns the effective thinking mode for a context size.
func ThinkModeForContext(mode ThinkMode, ctxSize int) ThinkMode {
	lib, err := DefaultLibrary()
	if err != nil {
		return mode
	}
	return lib.raw.ds4ThinkModeForContext(mode, int32(ctxSize))
}

// ContextMemoryEstimate estimates ds4 context memory for a backend and context
// size. libds4 derives the estimate from the active model shape selected by
// ds4_engine_open, so the result is only meaningful while at least one engine
// is open. Prefer [Engine.ContextMemoryEstimate] when you have an engine
// handle.
func ContextMemoryEstimate(backend Backend, ctxSize int) ContextMemory {
	lib, err := DefaultLibrary()
	if err != nil {
		return ContextMemory{}
	}
	cm := lib.raw.ds4ContextMemoryEstimate(backend, int32(ctxSize))
	return ContextMemory{
		TotalBytes:      cm.TotalBytes,
		RawBytes:        cm.RawBytes,
		CompressedBytes: cm.CompressedBytes,
		ScratchBytes:    cm.ScratchBytes,
		PrefillCap:      cm.PrefillCap,
		RawCap:          cm.RawCap,
		CompCap:         cm.CompCap,
	}
}

// ContextMemoryEstimateWithPrefill estimates ds4 context memory for a backend, context
// size, and prefill chunk size.
func ContextMemoryEstimateWithPrefill(backend Backend, ctxSize int, prefillChunk uint32) ContextMemory {
	lib, err := DefaultLibrary()
	if err != nil {
		return ContextMemory{}
	}
	var cm cContextMemory
	if lib.raw.ds4ContextMemoryEstimateWithPrefill != nil {
		cm = lib.raw.ds4ContextMemoryEstimateWithPrefill(backend, int32(ctxSize), prefillChunk)
	} else {
		cm = lib.raw.ds4ContextMemoryEstimate(backend, int32(ctxSize))
	}
	return ContextMemory{
		TotalBytes:      cm.TotalBytes,
		RawBytes:        cm.RawBytes,
		CompressedBytes: cm.CompressedBytes,
		ScratchBytes:    cm.ScratchBytes,
		PrefillCap:      cm.PrefillCap,
		RawCap:          cm.RawCap,
		CompCap:         cm.CompCap,
	}
}

// LogIsTTY calls ds4_log_is_tty for a C FILE*.
func LogIsTTY(fp File) bool {
	lib, err := DefaultLibrary()
	if err != nil {
		return false
	}
	return lib.raw.ds4LogIsTTY(uintptr(fp))
}

// LogString writes a plain string through ds4_log using a "%s" format.
func LogString(fp File, typ LogType, msg string) {
	lib, err := DefaultLibrary()
	if err != nil {
		return
	}
	lib.raw.ds4LogString(uintptr(fp), typ, "%s", msg)
}

// SetStderrFd redirects libds4's diagnostic stream to fd for the default
// library. Pass -1 to restore the native stderr.
//
// libds4 dups fd internally and writes its diagnostics there unbuffered, so the
// caller may close its own descriptor after this call. The redirect target is a
// process-global inside libds4; install it once during application startup,
// before generation is active.
func SetStderrFd(fd int) error {
	lib, err := DefaultLibrary()
	if err != nil {
		return err
	}
	return lib.SetStderrFd(fd)
}

// SetStderrFd redirects this library's diagnostic stream to fd. Pass -1 to
// restore the native stderr.
//
// libds4 dups fd internally (taking ownership of the dup) and writes its
// diagnostics there unbuffered, so the caller may close its own descriptor
// afterward. The redirect target is a process-global inside libds4, not a
// per-engine setting; install it once at startup, before generation, since it
// is not synchronized against concurrent logging from native worker threads.
func (l *Library) SetStderrFd(fd int) error {
	if l == nil {
		return errors.New("ds4: nil library")
	}
	libCallMu.Lock()
	l.raw.ds4SetStderrFd(int32(fd))
	libCallMu.Unlock()
	return nil
}

// SetAbortFunc installs a last-chance libds4 fatal-invariant callback for the
// default library.
//
// Passing nil restores libds4's default behavior: no callback, then native
// abort(). The setting is process-global inside libds4; install it once during
// application startup. Returning from the callback does not recover the engine:
// libds4 calls abort() immediately afterward.
func SetAbortFunc(fn AbortFunc) error {
	lib, err := DefaultLibrary()
	if err != nil {
		return err
	}
	return lib.SetAbortFunc(fn)
}

// SetAbortFunc installs a last-chance libds4 fatal-invariant callback for this
// loaded library.
//
// libds4 invokes the callback from ds4_die and allocation-guard failures after
// logging the same message at LogError and immediately before abort(). Passing
// nil restores the default behavior. This hook is for crash telemetry, flushing
// diagnostics, or deliberate process termination; it is not a normal recovery
// mechanism. If the callback returns, libds4 still aborts. The callback may be
// invoked from native worker threads and must be concurrency-safe.
func (l *Library) SetAbortFunc(fn AbortFunc) error {
	if l == nil {
		return errors.New("ds4: nil library")
	}
	id := registerAbortCallback(fn)
	fnPtr := uintptr(0)
	if id != 0 {
		fnPtr = abortCallback
	}

	libCallMu.Lock()
	l.raw.ds4AbortSet(fnPtr, id)
	libCallMu.Unlock()

	l.abortMu.Lock()
	old := l.abortID
	l.abortID = id
	l.abortMu.Unlock()
	unregisterAbortCallback(old)
	return nil
}

// CollectIMatrix calls ds4_engine_collect_imatrix with no minimum per-expert
// sample count, matching the upstream CLI default.
func (e *Engine) CollectIMatrix(datasetPath, outputPath string, ctxSize, maxPrompts, maxTokens int) error {
	return e.CollectIMatrixWithMinExpertSamples(datasetPath, outputPath, ctxSize, maxPrompts, maxTokens, 0)
}

// CollectIMatrixWithMinExpertSamples calls ds4_engine_collect_imatrix.
// minExpertSamples > 0 keeps collecting until every routed expert has that
// many samples (upstream --imatrix-min-expert-samples).
func (e *Engine) CollectIMatrixWithMinExpertSamples(datasetPath, outputPath string, ctxSize, maxPrompts, maxTokens, minExpertSamples int) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	code := e.lib.raw.ds4EngineCollectIMatrix(e.ptr, datasetPath, outputPath, int32(ctxSize), int32(maxPrompts), int32(maxTokens), int32(minExpertSamples))
	return ds4Error("ds4_engine_collect_imatrix", code)
}

// DumpTokens calls ds4_engine_dump_tokens.
func (e *Engine) DumpTokens(tokens *Tokens) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	e.lib.raw.ds4EngineDumpTokens(e.ptr, tokens.cptr())
	return nil
}

// DumpTextTokenization calls ds4_dump_text_tokenization.
func DumpTextTokenization(modelPath, text string, fp File) error {
	lib, err := DefaultLibrary()
	if err != nil {
		return err
	}
	code := lib.raw.ds4DumpTextTokenization(modelPath, text, uintptr(fp))
	return ds4Error("ds4_dump_text_tokenization", code)
}

// HeadTest calls ds4_engine_head_test.
func (e *Engine) HeadTest(prompt *Tokens) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	return ds4Error("ds4_engine_head_test", e.lib.raw.ds4EngineHeadTest(e.ptr, prompt.cptr()))
}

// FirstTokenTest calls ds4_engine_first_token_test.
func (e *Engine) FirstTokenTest(prompt *Tokens) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	return ds4Error("ds4_engine_first_token_test", e.lib.raw.ds4EngineFirstTokenTest(e.ptr, prompt.cptr()))
}

// MetalGraphTest calls ds4_engine_metal_graph_test.
func (e *Engine) MetalGraphTest(prompt *Tokens) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	return ds4Error("ds4_engine_metal_graph_test", e.lib.raw.ds4EngineMetalGraphTest(e.ptr, prompt.cptr()))
}

// MetalGraphFullTest calls ds4_engine_metal_graph_full_test.
func (e *Engine) MetalGraphFullTest(prompt *Tokens) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	return ds4Error("ds4_engine_metal_graph_full_test", e.lib.raw.ds4EngineMetalGraphFullTest(e.ptr, prompt.cptr()))
}

// MetalGraphPromptTest calls ds4_engine_metal_graph_prompt_test.
func (e *Engine) MetalGraphPromptTest(prompt *Tokens, ctxSize int) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	return ds4Error("ds4_engine_metal_graph_prompt_test", e.lib.raw.ds4EngineMetalGraphPromptTest(e.ptr, prompt.cptr(), int32(ctxSize)))
}

// GenerateArgmax calls ds4_engine_generate_argmax.
func (e *Engine) GenerateArgmax(prompt *Tokens, opts ArgmaxGenerateOptions) ([]int, error) {
	unlock, err := e.require()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var out []int
	onToken := opts.OnToken
	if onToken == nil {
		onToken = func(token int) { out = append(out, token) }
	}
	tokenID := registerTokenCallback(onToken)
	doneID := registerDoneCallback(opts.OnDone)
	progressID := registerProgressCallback(opts.OnProgress)
	defer unregisterTokenCallback(tokenID)
	defer unregisterDoneCallback(doneID)
	defer unregisterProgressCallback(progressID)

	var emitPtr, donePtr, progressPtr uintptr
	if tokenID != 0 {
		emitPtr = tokenEmitCallback
	}
	if doneID != 0 {
		donePtr = doneCallback
	}
	if progressID != 0 {
		progressPtr = progressCallback
	}
	code := e.lib.raw.ds4EngineGenerateArgmax(e.ptr, prompt.cptr(), int32(opts.NPredict), int32(opts.CtxSize), emitPtr, donePtr, tokenID, progressPtr, progressID)
	if err := ds4Error("ds4_engine_generate_argmax", code); err != nil {
		return out, err
	}
	return out, nil
}

// TokenizeText tokenizes plain text with ds4_tokenize_text.
func (e *Engine) TokenizeText(text string) (*Tokens, error) {
	unlock, err := e.require()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var out cTokens
	e.lib.raw.ds4TokenizeText(e.ptr, text, &out)
	return tokensFromC(e.lib, out), nil
}

// NewTokens creates a libds4-owned token vector associated with this engine's library.
func (e *Engine) NewTokens(ids []int) (*Tokens, error) {
	unlock, err := e.require()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return newTokensWithLibrary(e.lib, ids)
}

// TokenizeRenderedChat tokenizes a rendered chat prompt.
func (e *Engine) TokenizeRenderedChat(text string) (*Tokens, error) {
	unlock, err := e.require()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var out cTokens
	e.lib.raw.ds4TokenizeRenderedChat(e.ptr, text, &out)
	return tokensFromC(e.lib, out), nil
}

// ChatBegin appends ds4's chat preamble to tokens.
func (e *Engine) ChatBegin(tokens *Tokens) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	e.lib.raw.ds4ChatBegin(e.ptr, tokens.cptr())
	return nil
}

// EncodeChatPrompt encodes a system and user prompt with ds4's chat template.
func (e *Engine) EncodeChatPrompt(system, prompt string, thinkMode ThinkMode) (*Tokens, error) {
	unlock, err := e.require()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var out cTokens
	e.lib.raw.ds4EncodeChatPrompt(e.ptr, system, prompt, thinkMode, &out)
	return tokensFromC(e.lib, out), nil
}

// ChatAppendMaxEffortPrefix appends ds4's maximum-effort thinking prompt text.
//
// Use this when constructing a chat prompt incrementally with ChatBegin,
// ChatAppendMessage, and ChatAppendAssistantPrefix, and the effective thinking
// mode is ThinkMax. To match ds4_encode_chat_prompt, append it once after
// ChatBegin and before the system/message turns. It does not append the
// assistant marker or <think> marker; call ChatAppendAssistantPrefix with the
// same effective thinking mode at the end of the prompt.
//
// Do not call this in addition to EncodeChatPrompt: ds4_encode_chat_prompt
// already includes this prefix when thinkMode is ThinkMax.
func (e *Engine) ChatAppendMaxEffortPrefix(tokens *Tokens) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	e.lib.raw.ds4ChatAppendMaxEffortPrefix(e.ptr, tokens.cptr())
	return nil
}

// ChatAppendMessage appends a rendered role/content chat message.
func (e *Engine) ChatAppendMessage(tokens *Tokens, role, content string) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	e.lib.raw.ds4ChatAppendMessage(e.ptr, tokens.cptr(), role, content)
	return nil
}

// ChatAppendAssistantPrefix appends the assistant prefix for generation.
//
// Passing ThinkHigh or ThinkMax appends the assistant marker followed by the
// normal <think> marker. Passing ThinkMax here does not append the
// maximum-effort prompt text; for incremental prompts, call
// ChatAppendMaxEffortPrefix once near the beginning of the prompt when the
// effective thinking mode is ThinkMax. The two methods compose and should not
// be treated as mutually exclusive for ThinkMax.
func (e *Engine) ChatAppendAssistantPrefix(tokens *Tokens, thinkMode ThinkMode) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	e.lib.raw.ds4ChatAppendAssistantPrefix(e.ptr, tokens.cptr(), thinkMode)
	return nil
}

// TokenText decodes one token to text and frees the C allocation returned by ds4.
func (e *Engine) TokenText(token int) (string, error) {
	unlock, err := e.require()
	if err != nil {
		return "", err
	}
	defer unlock()
	var n uintptr
	ptr := e.lib.raw.ds4TokenText(e.ptr, int32(token), &n)
	if ptr == nil || n == 0 {
		cFree(ptr)
		return "", nil
	}
	bytes := unsafe.Slice((*byte)(ptr), int(n))
	text := string(bytes)
	cFree(ptr)
	return text, nil
}

// TokenEOS returns ds4's end-of-sequence token id.
func (e *Engine) TokenEOS() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4TokenEOS(e.ptr))
}

// TokenIsStop reports whether token ends generation for this model
// (ds4_token_is_stop). DeepSeek shapes stop only on EOS, but GLM DSA also
// stops on the system, user, assistant, and observation role tokens, so
// callers must use this instead of comparing against [Engine.TokenEOS].
//
// Libraries without ds4_token_is_stop fall back to an EOS comparison.
func (e *Engine) TokenIsStop(token int) bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	return e.tokenIsStopLocked(token)
}

// tokenIsStopLocked implements TokenIsStop with libCallMu already held.
func (e *Engine) tokenIsStopLocked(token int) bool {
	if e == nil || e.ptr == 0 {
		return false
	}
	if e.lib.raw.ds4TokenIsStop == nil {
		return int32(token) == e.lib.raw.ds4TokenEOS(e.ptr)
	}
	return e.lib.raw.ds4TokenIsStop(e.ptr, int32(token))
}

// TokenIsThinkingControl reports whether token is a <think> or </think> marker
// (ds4_token_is_thinking_control). Returns false on libraries that do not
// export the predicate.
func (e *Engine) TokenIsThinkingControl(token int) bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 || e.lib.raw.ds4TokenIsThinkingControl == nil {
		return false
	}
	return e.lib.raw.ds4TokenIsThinkingControl(e.ptr, int32(token))
}

// TokenIsStopForThinkMode reports whether token ends generation under mode
// (ds4_token_is_stop_for_think_mode). Beyond the ordinary stop set, a thinking
// control marker emitted while thinking is disabled is treated as a stop: the
// prompt already supplied the closing tag, so the marker is protocol noise
// rather than assistant content.
//
// Libraries without the predicate fall back to [Engine.TokenIsStop].
func (e *Engine) TokenIsStopForThinkMode(token int, mode ThinkMode) bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return false
	}
	if e.lib.raw.ds4TokenIsStopForThinkMode == nil {
		return e.tokenIsStopLocked(token)
	}
	return e.lib.raw.ds4TokenIsStopForThinkMode(e.ptr, int32(token), mode)
}

// IsGLMDSA reports whether the open model is a GLM DSA shape
// (ds4_engine_is_glm_dsa) rather than a DeepSeek V4 shape. GLM uses different
// chat markers, stop tokens, and tool-call markup, so callers building prompts
// or parsing completions must branch on this.
//
// Returns false on libraries predating GLM support; see [Library.SupportsGLM].
func (e *Engine) IsGLMDSA() bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 || e.lib.raw.ds4EngineIsGLMDSA == nil {
		return false
	}
	return e.lib.raw.ds4EngineIsGLMDSA(e.ptr)
}

// SupportsSampledSpeculative reports whether the loaded library can speculate
// at positive temperature (ds4_session_eval_speculative). When false, only
// greedy speculation is available; see [Library.SupportsSampledSpeculative].
func (e *Engine) SupportsSampledSpeculative() bool {
	return e != nil && e.lib.SupportsSampledSpeculative()
}

// GLMReasoningEffortText returns the GLM reasoning-effort system line for mode,
// or "" for ThinkNone and for libraries without GLM support. Callers should
// only emit it when [Engine.IsGLMDSA] reports true; DeepSeek shapes use
// [Engine.ChatAppendMaxEffortPrefix] instead.
func (e *Engine) GLMReasoningEffortText(mode ThinkMode) string {
	if e == nil {
		return ""
	}
	return e.lib.GLMReasoningEffortText(mode)
}

// PrefillChunk returns the engine's effective prefill chunk size
// (ds4_engine_prefill_chunk), or 0 on libraries without the accessor.
func (e *Engine) PrefillChunk() uint32 {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 || e.lib.raw.ds4EnginePrefillChunk == nil {
		return 0
	}
	return e.lib.raw.ds4EnginePrefillChunk(e.ptr)
}

// TokenUser returns ds4's user-role token id.
func (e *Engine) TokenUser() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4TokenUser(e.ptr))
}

// TokenAssistant returns ds4's assistant-role token id.
func (e *Engine) TokenAssistant() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4TokenAssistant(e.ptr))
}

// RoutedQuantBits returns the routed expert quantization bits used by the engine.
func (e *Engine) RoutedQuantBits() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4EngineRoutedQuantBits(e.ptr))
}

// LayerCount returns the total number of layers in the model, or 0 if not supported by the loaded library.
func (e *Engine) LayerCount() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 || e.lib.raw.ds4EngineLayerCount == nil {
		return 0
	}
	return int(e.lib.raw.ds4EngineLayerCount(e.ptr))
}

// LayerCompressRatio returns the compression/quantization ratio of the specified layer, or 0 if not supported.
func (e *Engine) LayerCompressRatio(layer int) int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 || e.lib.raw.ds4EngineLayerCompressRatio == nil {
		return 0
	}
	return int(e.lib.raw.ds4EngineLayerCompressRatio(e.ptr, uint32(layer)))
}

// HasOutputHead reports whether the loaded GGUF includes the output head. For a
// distributed split this is false on the coordinator half (e.g. layers 0:30) and
// true on the worker half that owns the tail (e.g. 31:output).
func (e *Engine) HasOutputHead() bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return false
	}
	return e.lib.raw.ds4EngineHasOutputHead(e.ptr)
}

// HasMTP reports whether this engine has an MTP draft model.
func (e *Engine) HasMTP() bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return false
	}
	return e.lib.raw.ds4EngineHasMTP(e.ptr)
}

// MTPExactSampling calls ds4_engine_mtp_exact_sampling: whether exact
// stochastic p/q acceptance is active for DSpark or GLM's embedded MTP block
// (EngineOptions.DsparkExactSampling). Under exact sampling, a speculative
// block drafted under one parser mode must be resampled once the mode flips;
// opportunistic drafts match greedy continuation in either mode and need no
// resample. It returns false when the loaded library predates the symbol.
func (e *Engine) MTPExactSampling() bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 || e.lib.raw.ds4EngineMTPExactSampling == nil {
		return false
	}
	return e.lib.raw.ds4EngineMTPExactSampling(e.ptr)
}

// MTPDraftTokens returns the configured MTP draft length.
func (e *Engine) MTPDraftTokens() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4EngineMTPDraftTokens(e.ptr))
}

// Power returns ds4_engine_power: the current power-throttle duty cycle
// percentage (1..100). 100 means no throttling.
func (e *Engine) Power() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4EnginePower(e.ptr))
}

// SetPower calls ds4_engine_set_power. powerPercent must be in 1..100.
func (e *Engine) SetPower(powerPercent int) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	return ds4Error("ds4_engine_set_power", e.lib.raw.ds4EngineSetPower(e.ptr, int32(powerPercent)))
}

// VocabSize returns ds4_engine_vocab_size: the model vocabulary size.
func (e *Engine) VocabSize() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4EngineVocabSize(e.ptr))
}

// ModelName returns ds4_engine_model_name: the printable name of the opened
// model shape (e.g. "Flash", "Pro").
func (e *Engine) ModelName() string {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return ""
	}
	return e.lib.raw.ds4EngineModelName(e.ptr)
}

// ModelID returns ds4_engine_model_id: a stable id for cache compatibility.
// 0 is the original Flash shape; Pro and later shapes use nonzero ids.
func (e *Engine) ModelID() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4EngineModelID(e.ptr))
}

// ContextMemoryEstimate calls ds4_context_memory_estimate using the active
// model shape selected by the underlying ds4_engine_open. Prefer this over
// the package-level [ContextMemoryEstimate] when an engine is open, since the
// libds4 estimate now depends on Flash-vs-Pro dimensions.
func (e *Engine) ContextMemoryEstimate(backend Backend, ctxSize int) ContextMemory {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return ContextMemory{}
	}
	cm := e.lib.raw.ds4ContextMemoryEstimate(backend, int32(ctxSize))
	return ContextMemory{
		TotalBytes:      cm.TotalBytes,
		RawBytes:        cm.RawBytes,
		CompressedBytes: cm.CompressedBytes,
		ScratchBytes:    cm.ScratchBytes,
		PrefillCap:      cm.PrefillCap,
		RawCap:          cm.RawCap,
		CompCap:         cm.CompCap,
	}
}

// ContextMemoryEstimateWithPrefill calls ds4_context_memory_estimate_with_prefill
// using the active model shape and prefill chunk size.
func (e *Engine) ContextMemoryEstimateWithPrefill(backend Backend, ctxSize int, prefillChunk uint32) ContextMemory {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 {
		return ContextMemory{}
	}
	var cm cContextMemory
	if e.lib.raw.ds4ContextMemoryEstimateWithPrefill != nil {
		cm = e.lib.raw.ds4ContextMemoryEstimateWithPrefill(backend, int32(ctxSize), prefillChunk)
	} else {
		cm = e.lib.raw.ds4ContextMemoryEstimate(backend, int32(ctxSize))
	}
	return ContextMemory{
		TotalBytes:      cm.TotalBytes,
		RawBytes:        cm.RawBytes,
		CompressedBytes: cm.CompressedBytes,
		ScratchBytes:    cm.ScratchBytes,
		PrefillCap:      cm.PrefillCap,
		RawCap:          cm.RawCap,
		CompCap:         cm.CompCap,
	}
}
