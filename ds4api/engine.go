package ds4api

import (
	"runtime"
	"sync"
	"unsafe"
)

// Engine wraps a ds4_engine.
type Engine struct {
	lib  *Library
	ptr  uintptr
	once sync.Once
	mu   sync.Mutex // guards calls into libds4; see Session for the same pattern
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
	modelBytes, modelPtr := cStringPointer(opts.ModelPath)
	mtpBytes, mtpPtr := cStringPointer(opts.MTPPath)
	steerBytes, steerPtr := cStringPointer(opts.DirectionalSteeringFile)
	copts := cEngineOptions{
		ModelPath:               modelPtr,
		MTPPath:                 mtpPtr,
		Backend:                 opts.Backend,
		NThreads:                int32(opts.NThreads),
		MTPDraftTokens:          int32(opts.MTPDraftTokens),
		MTPMargin:               opts.MTPMargin,
		DirectionalSteeringFile: steerPtr,
		DirectionalSteeringAttn: opts.DirectionalSteeringAttn,
		DirectionalSteeringFFN:  opts.DirectionalSteeringFFN,
		WarmWeights:             opts.WarmWeights,
		Quality:                 opts.Quality,
	}
	var out uintptr
	code := l.raw.ds4EngineOpen(&out, &copts)
	runtime.KeepAlive(modelBytes)
	runtime.KeepAlive(mtpBytes)
	runtime.KeepAlive(steerBytes)
	if err := ds4Error("ds4_engine_open", code); err != nil {
		return nil, err
	}
	e := &Engine{lib: l, ptr: out}
	runtime.SetFinalizer(e, (*Engine).Close)
	return e, nil
}

// Close releases the underlying ds4_engine.
func (e *Engine) Close() {
	if e == nil {
		return
	}
	e.once.Do(func() {
		runtime.SetFinalizer(e, nil)
		if e.ptr != 0 {
			e.lib.raw.ds4EngineClose(e.ptr)
			e.ptr = 0
		}
	})
}

// require locks the engine and verifies it is open. The returned unlock
// function MUST be called (typically via defer) to release the lock.
func (e *Engine) require() (unlock func(), err error) {
	e.mu.Lock()
	if e == nil || e.ptr == 0 {
		e.mu.Unlock()
		return nil, errClosed
	}
	return e.mu.Unlock, nil
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

// ContextMemoryEstimate estimates ds4 context memory for a backend and context size.
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

// CollectIMatrix calls ds4_engine_collect_imatrix.
func (e *Engine) CollectIMatrix(datasetPath, outputPath string, ctxSize, maxPrompts, maxTokens int) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	code := e.lib.raw.ds4EngineCollectIMatrix(e.ptr, datasetPath, outputPath, int32(ctxSize), int32(maxPrompts), int32(maxTokens))
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
	e.mu.Lock()
	defer e.mu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4TokenEOS(e.ptr))
}

// TokenUser returns ds4's user-role token id.
func (e *Engine) TokenUser() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4TokenUser(e.ptr))
}

// TokenAssistant returns ds4's assistant-role token id.
func (e *Engine) TokenAssistant() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4TokenAssistant(e.ptr))
}

// RoutedQuantBits returns the routed expert quantization bits used by the engine.
func (e *Engine) RoutedQuantBits() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4EngineRoutedQuantBits(e.ptr))
}

// HasMTP reports whether this engine has an MTP draft model.
func (e *Engine) HasMTP() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e == nil || e.ptr == 0 {
		return false
	}
	return e.lib.raw.ds4EngineHasMTP(e.ptr)
}

// MTPDraftTokens returns the configured MTP draft length.
func (e *Engine) MTPDraftTokens() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e == nil || e.ptr == 0 {
		return 0
	}
	return int(e.lib.raw.ds4EngineMTPDraftTokens(e.ptr))
}
