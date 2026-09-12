// Package ds4api test infrastructure: a pure-Go mock of libds4.
//
// NewMockLibrary creates a Library whose raw function pointers are Go
// implementations backed by in-memory state.  This lets tests exercise the
// ds4go binding and generator layers without loading a real shared library.
package ds4api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// mockStderr emulates libds4's redirectable diagnostic stream for the mock
// library. set(fd) points it at a descriptor (-1 clears it); write(s) sends a
// message there, or drops it when unset. It wraps the descriptor without taking
// ownership: the finalizer is cleared so the wrapper never closes the caller's
// fd, mirroring that callers retain their own descriptor after ds4_set_stderr_fd.
var mockStderr mockStderrStream

type mockStderrStream struct {
	mu sync.Mutex
	f  *os.File
}

func (m *mockStderrStream) set(fd int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fd < 0 {
		m.f = nil
		return
	}
	f := os.NewFile(uintptr(fd), "ds4-mock-stderr")
	runtime.SetFinalizer(f, nil)
	m.f = f
}

func (m *mockStderrStream) write(msg string) {
	m.mu.Lock()
	f := m.f
	m.mu.Unlock()
	if f != nil {
		_, _ = f.WriteString(msg)
	}
}

// mockVocabSize is the canonical vocab size returned by the mock library.
// Must stay in sync between ds4_engine_vocab_size and ds4_session_copy_logits
// mocks: TestSessionCopyLogits asserts the two agree.
const mockVocabSize int32 = 129280

// mockEmbdDim is the row width the mock vision encoder reports and produces.
// Untyped so it compares directly against both Engine.EmbdDim's int and the
// raw ds4EngineEmbdDim int32 return without an explicit conversion.
const mockEmbdDim = 8

// mockImageToken is the placeholder id the mock chat/prompt appenders emit
// for each embedding row. mockImageStart and mockImageEnd bracket the block
// ds4_prompt_append_vision writes.
const mockImageToken int32 = 7
const mockImageStart, mockImageEnd int32 = 8, 9

// Mock special-token ids and prefill geometry. The role tokens double as GLM
// generation stops in the mock's ds4_token_is_stop.
const (
	mockUserToken       int32 = 2
	mockAssistantToken  int32 = 3
	mockThinkStartToken int32 = 4
	mockThinkEndToken   int32 = 5

	mockPrefillChunk uint32 = 2048
)

// MockControls tunes the model-family behaviour of a mock library after it is
// created.  The zero behaviour of a fresh mock is a DeepSeek V4 engine: not
// GLM, EOS is the only generation stop, and no thinking-control markers.  It
// is safe to call these from any goroutine.
type MockControls struct {
	mu              sync.RWMutex
	glm             bool
	vision          bool
	multimodalFail  int // image index at which the mock multimodal append fails; -1 never
	imatrix         *IMatrixCall
	stops           map[int32]bool
	thinking        map[int32]bool
	specArgmaxCall  int
	specSampledCall int
	syncCalls       int
	rewindCalls     int
	multimodalSyncs int
}

// SyncCalls reports how many ds4_session_sync calls the mock has served, so
// tests can tell a rewind that rebuilt its prefix from one that kept state.
func (c *MockControls) SyncCalls() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.syncCalls
}

// RewindCalls reports how many ds4_session_rewind calls the mock has served.
func (c *MockControls) RewindCalls() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rewindCalls
}

// MultimodalSyncCalls reports how many ds4_session_sync_multimodal calls the
// mock served.
func (c *MockControls) MultimodalSyncCalls() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.multimodalSyncs
}

func (c *MockControls) count(field *int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*field++
}

// SpeculativeCalls reports how many times each speculative entry point was
// invoked, so tests can assert which path a generator took.
func (c *MockControls) SpeculativeCalls() (argmax, sampled int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.specArgmaxCall, c.specSampledCall
}

func (c *MockControls) countSpeculative(sampled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sampled {
		c.specSampledCall++
		return
	}
	c.specArgmaxCall++
}

// SetGLM makes the mock engine report the GLM DSA family (ds4_engine_is_glm_dsa).
func (c *MockControls) SetGLM(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.glm = enabled
}

// SetVision makes the mock engine report a loaded vision encoder
// (ds4_engine_has_vision) so encode and multimodal calls succeed.
func (c *MockControls) SetVision(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vision = enabled
}

func (c *MockControls) hasVision() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vision
}

// IMatrixCall records the arguments of the last ds4_engine_collect_imatrix
// call the mock served.
type IMatrixCall struct {
	Dataset, Output                                  string
	CtxSize, MaxPrompts, MaxTokens, MinExpertSamples int
}

// LastIMatrixCall returns the most recent imatrix collection call, or nil.
func (c *MockControls) LastIMatrixCall() *IMatrixCall {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.imatrix
}

// SetMultimodalAppendFailAt makes ds4_chat_append_multimodal_message fail
// when it reaches image index (0-based) after appending the earlier ones, the
// way libds4 fails on a bad layout mid-message. Pass -1 to restore success.
func (c *MockControls) SetMultimodalAppendFailAt(index int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.multimodalFail = index
}

func (c *MockControls) multimodalAppendFailAt() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.multimodalFail
}

// SetStopTokens overrides the token ids the mock reports as generation stops
// (ds4_token_is_stop) in addition to EOS.  GLM stops on the system, user,
// assistant, and observation role tokens, so tests use this to exercise
// callers that must not assume EOS is the only stop.
func (c *MockControls) SetStopTokens(tokens ...int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops = tokenSet(tokens)
}

// SetThinkingControlTokens overrides the token ids the mock reports as <think>
// and </think> markers (ds4_token_is_thinking_control).
func (c *MockControls) SetThinkingControlTokens(tokens ...int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.thinking = tokenSet(tokens)
}

func (c *MockControls) isGLM() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.glm
}

func (c *MockControls) isStop(token int32) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stops[token]
}

func (c *MockControls) isThinking(token int32) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.thinking[token]
}

func tokenSet(tokens []int) map[int32]bool {
	set := make(map[int32]bool, len(tokens))
	for _, t := range tokens {
		set[int32(t)] = true
	}
	return set
}

// DisableSampledSpeculative drops ds4_session_eval_speculative from a mock
// library, modelling a libds4 build that predates upstream's exact stochastic
// DSpark work.
func (l *Library) DisableSampledSpeculative() {
	if l != nil {
		l.raw.ds4SessionEvalSpeculative = nil
	}
}

// NewMockLibrary returns a Library whose C symbols are backed by trivial
// in-memory state.  The mock supports engine/session lifecycle, tokenization,
// deterministic generation, and optional MTP metadata.
func NewMockLibrary() *Library {
	lib, _ := NewMockLibraryWithControls()
	return lib
}

// NewMockLibraryWithControls returns a mock library alongside the controls that
// tune its model-family behaviour.  See [MockControls].
func NewMockLibraryWithControls() (*Library, *MockControls) {
	lib := &Library{path: "mock", handle: 0}
	r := &lib.raw
	ctl := &MockControls{stops: map[int32]bool{}, thinking: map[int32]bool{}, multimodalFail: -1}
	mockStderr.set(-1)
	_ = loadCRuntime() // idempotent; cMalloc/cFree below require it.

	// Engine lifecycle.
	r.ds4EngineOpen = mockEngineOpen
	r.ds4EngineClose = mockEngineClose
	r.ds4EngineSummary = func(e uintptr) {}
	r.ds4BackendName = func(backend Backend) string { return "mock" }
	r.ds4ThinkModeEnabled = func(mode ThinkMode) bool { return true }
	r.ds4ThinkModeName = func(mode ThinkMode) string { return "think" }
	r.ds4ThinkMaxPrefix = func() string { return "<think_max>" }
	r.ds4ThinkMaxMinContext = func() uint32 { return 32768 }
	r.ds4ThinkModeForContext = func(mode ThinkMode, ctxSize int32) ThinkMode { return mode }
	r.ds4ContextMemoryEstimate = func(backend Backend, ctxSize int32) cContextMemory {
		return cContextMemory{TotalBytes: 1 << 30}
	}
	r.ds4ContextMemoryEstimateWithPrefill = func(backend Backend, ctxSize int32, prefillChunk uint32) cContextMemory {
		return cContextMemory{TotalBytes: 1 << 30}
	}
	r.ds4LogIsTTY = func(fp uintptr) bool { return false }
	// The mock mirrors the real engine's redirect semantics: ds4_log writes to
	// the descriptor installed via ds4_set_stderr_fd (unbuffered), or is dropped
	// when none is set (the real library would write to native stderr).
	r.ds4LogString = func(fp uintptr, typ LogType, format string, msg string) {
		mockStderr.write(msg)
	}
	r.ds4SetStderrFd = func(fd int32) { mockStderr.set(fd) }
	r.ds4AbortSet = func(fn uintptr, ud uintptr) {}

	// Engine tests & diagnostics.
	r.ds4EngineGenerateArgmax = func(e uintptr, prompt *cTokens, nPredict int32, ctxSize int32, emit uintptr, done uintptr, emitUD uintptr, progress uintptr, progressUD uintptr) int32 {
		return 0
	}
	r.ds4EngineCollectIMatrix = func(e uintptr, datasetPath string, outputPath string, ctxSize int32, maxPrompts int32, maxTokens int32, minExpertSamples int32) int32 {
		ctl.mu.Lock()
		ctl.imatrix = &IMatrixCall{Dataset: datasetPath, Output: outputPath, CtxSize: int(ctxSize), MaxPrompts: int(maxPrompts), MaxTokens: int(maxTokens), MinExpertSamples: int(minExpertSamples)}
		ctl.mu.Unlock()
		return 0
	}
	r.ds4EngineDumpTokens = func(e uintptr, tokens *cTokens) {}
	r.ds4DumpTextTokenization = func(modelPath string, text string, fp uintptr) int32 { return 0 }
	r.ds4EngineHeadTest = func(e uintptr, prompt *cTokens) int32 { return 0 }
	r.ds4EngineFirstTokenTest = func(e uintptr, prompt *cTokens) int32 { return 0 }
	r.ds4EngineMetalGraphTest = func(e uintptr, prompt *cTokens) int32 { return 0 }
	r.ds4EngineMetalGraphFullTest = func(e uintptr, prompt *cTokens) int32 { return 0 }
	r.ds4EngineMetalGraphPromptTest = func(e uintptr, prompt *cTokens, ctxSize int32) int32 { return 0 }

	// Tokens.
	r.ds4TokensPush = mockTokensPush
	r.ds4TokensFree = mockTokensFree
	r.ds4TokensCopy = mockTokensCopy
	r.ds4TokensStartsWith = mockTokensStartsWith

	// Tokenization & chat helpers.
	r.ds4TokenizeText = mockTokenizeText
	r.ds4TokenizeRenderedChat = mockTokenizeText
	r.ds4ChatBegin = mockChatBegin
	r.ds4EncodeChatPrompt = mockEncodeChatPrompt
	r.ds4ChatAppendMaxEffortPrefix = func(e uintptr, tokens *cTokens) {
		mockTokenizeText(e, "<think_max>", tokens)
	}
	r.ds4ChatAppendMessage = mockChatAppendMessage
	r.ds4ChatAppendAssistantPrefix = func(e uintptr, tokens *cTokens, thinkMode ThinkMode) {}

	// Token metadata.
	r.ds4TokenText = mockTokenText
	r.ds4TokenEOS = mockTokenEOS
	r.ds4TokenUser = func(e uintptr) int32 { return mockUserToken }
	r.ds4TokenAssistant = func(e uintptr) int32 { return mockAssistantToken }

	// GLM DSA. Defaults model a DeepSeek engine (not GLM, EOS-only stops, no
	// thinking-control markers); tests opt into GLM behaviour via MockControls.
	r.ds4EngineIsGLMDSA = func(e uintptr) bool { return ctl.isGLM() }
	r.ds4GLMReasoningEffortText = func(mode ThinkMode) string {
		switch mode {
		case ThinkHigh:
			return "Reasoning Effort: High"
		case ThinkMax:
			return "Reasoning Effort: Max"
		default:
			return ""
		}
	}
	r.ds4TokenIsStop = func(e uintptr, token int32) bool {
		return token == mockTokenEOS(e) || ctl.isStop(token)
	}
	r.ds4TokenIsThinkingControl = func(e uintptr, token int32) bool {
		return ctl.isThinking(token)
	}
	r.ds4TokenIsStopForThinkMode = func(e uintptr, token int32, mode ThinkMode) bool {
		if r.ds4TokenIsStop(e, token) {
			return true
		}
		return !mockThinkModeEnabled(mode) && r.ds4TokenIsThinkingControl(e, token)
	}
	r.ds4EnginePrefillChunk = func(e uintptr) uint32 { return mockPrefillChunk }
	r.ds4SessionPrefillCap = func(s uintptr) int32 { return int32(mockPrefillChunk) }

	// Session.
	r.ds4SessionCreate = mockSessionCreate
	r.ds4SessionFree = mockSessionFree
	r.ds4SessionPower = func(s uintptr) int32 {
		sess := mockSessionPtr(s)
		if sess == nil || sess.engine == nil {
			return 100
		}
		return sess.engine.powerPercent
	}
	r.ds4SessionSetPower = func(s uintptr, powerPercent int32) int32 {
		if powerPercent < 1 || powerPercent > 100 {
			return 1
		}
		sess := mockSessionPtr(s)
		if sess == nil || sess.engine == nil {
			return 1
		}
		sess.engine.powerPercent = powerPercent
		return 0
	}
	r.ds4SessionDirectionalSteeringFFN = func(s uintptr) float32 {
		sess := mockSessionPtr(s)
		if sess == nil || sess.engine == nil {
			return 0
		}
		return sess.engine.steeringFFN
	}
	r.ds4SessionSetDirectionalSteeringFFN = func(s uintptr, scale float32) int32 {
		// ds4.c rejects non-finite scales and magnitudes above 100.
		if scale != scale || scale < -100 || scale > 100 {
			return 1
		}
		sess := mockSessionPtr(s)
		if sess == nil || sess.engine == nil {
			return 1
		}
		sess.engine.steeringFFN = scale
		return 0
	}
	r.ds4SessionSetProgress = func(s uintptr, fn uintptr, ud uintptr) {}
	r.ds4SessionSetDisplayProgress = func(s uintptr, fn uintptr, ud uintptr) {}
	r.ds4SessionSetCancel = func(s uintptr, fn uintptr, ud uintptr) {
		if sess := mockSessionPtr(s); sess != nil {
			sess.cancelID = ud
		}
	}
	r.ds4SessionSync = func(s uintptr, prompt *cTokens, err unsafe.Pointer, errLen uintptr) int32 {
		ctl.count(&ctl.syncCalls)
		if sess := mockSessionPtr(s); sess != nil {
			sess.images = nil
		}
		return mockSessionSync(s, prompt, err, errLen)
	}
	r.ds4SessionRewriteRequiresRebuild = func(liveLen int32, canonicalLen int32, common int32) bool { return true }
	r.ds4SessionRewriteFromCommon = func(s uintptr, prompt *cTokens, common int32, err unsafe.Pointer, errLen uintptr) SessionRewriteResult {
		return SessionRewriteError
	}
	r.ds4SessionCommonPrefix = func(s uintptr, prompt *cTokens) int32 {
		sess := mockSessionPtr(s)
		if sess == nil || sess.checkpointInvalid || prompt == nil || prompt.V == nil {
			return 0
		}
		src := (*mockTokensSlice(prompt.V))[:prompt.Len]
		n := 0
		for n < len(src) && n < len(sess.evaluated) && src[n] == sess.evaluated[n] {
			n++
		}
		return int32(n)
	}
	r.ds4SessionArgmax = mockSessionArgmax
	r.ds4SessionArgmaxExcluding = func(s uintptr, excludedID int32) int32 { return mockSessionArgmax(s) }
	r.ds4SessionSample = mockSessionSample
	r.ds4SessionTopLogprobs = func(s uintptr, out *cTokenScore, k int32) int32 {
		sess := mockSessionPtr(s)
		if sess == nil {
			return 0
		}
		if k <= 0 {
			return 0
		}
		scores := unsafe.Slice(out, int(k))
		for i := 0; i < int(k); i++ {
			tokenID := sess.engine.nextToken + sess.pos + int32(i)
			scores[i] = cTokenScore{
				ID:      tokenID,
				Logit:   12.0 - float32(i)*0.8,
				Logprob: -float32(i) * 0.15,
			}
		}
		return k
	}
	r.ds4SessionCopyLogits = func(s uintptr, out unsafe.Pointer, capacity int32) int32 {
		if capacity < mockVocabSize {
			return -1
		}
		// Fill with zeros; mock doesn't carry real logits.
		buf := unsafe.Slice((*float32)(out), int(capacity))
		for i := range buf[:mockVocabSize] {
			buf[i] = 0
		}
		return mockVocabSize
	}
	// Upstream returns 1 on success and 0 on failure (bad session, token
	// out of range, non-finite logits), unlike the status-code entry points.
	r.ds4SessionTokenLogprob = func(s uintptr, token int32, out *cTokenScore) int32 {
		sess := mockSessionPtr(s)
		if sess == nil || token < 0 || token >= mockVocabSize {
			return 0
		}
		out.ID = token
		out.Logit = 8.5
		out.Logprob = -0.5
		return 1
	}
	r.ds4SessionEval = mockSessionEval
	r.ds4SessionEvalSpeculativeArgmax = func(s uintptr, firstToken, maxTokens, eosToken int32,
		accepted unsafe.Pointer, acceptedCap int32, err unsafe.Pointer, errLen uintptr) int32 {
		ctl.countSpeculative(false)
		return mockSessionEvalSpeculativeArgmax(s, firstToken, maxTokens, eosToken, accepted, acceptedCap, err, errLen)
	}
	r.ds4SessionEvalSpeculative = func(s uintptr, firstToken, maxTokens, eosToken int32,
		temperature float32, topK int32, topP, minP float32, rng *uint64,
		accepted unsafe.Pointer, acceptedCap int32, err unsafe.Pointer, errLen uintptr) int32 {
		ctl.countSpeculative(true)
		return mockSessionEvalSpeculative(s, firstToken, maxTokens, eosToken,
			temperature, topK, topP, minP, rng, accepted, acceptedCap, err, errLen)
	}
	r.ds4SessionInvalidate = func(s uintptr) {}
	r.ds4SessionRewind = func(s uintptr, pos int32) {
		ctl.count(&ctl.rewindCalls)
		sess := mockSessionPtr(s)
		if sess == nil {
			return
		}
		if pos < 0 {
			pos = 0
		}
		if pos >= int32(len(sess.evaluated)) {
			return
		}
		sess.evaluated = sess.evaluated[:pos]
		sess.pos = pos
		// DeepSeek compressors cannot be rolled back; GLM restores its state.
		if !ctl.isGLM() {
			sess.checkpointInvalid = true
		}
	}
	r.ds4SessionPos = mockSessionPos
	r.ds4SessionCtx = mockSessionCtx

	// Engine metadata.
	r.ds4EngineRoutedQuantBits = func(e uintptr) int32 { return 4 }
	r.ds4EngineHasOutputHead = mockEngineHasOutputHead
	r.ds4EngineHasMTP = mockEngineHasMTP
	r.ds4EngineMTPDraftTokens = mockEngineMTPDraftTokens
	r.ds4EngineMTPExactSampling = func(e uintptr) bool {
		if eng := mockEnginePtr(e); eng != nil {
			return eng.exactSampling
		}
		return false
	}
	r.ds4EnginePower = func(e uintptr) int32 {
		if eng := mockEnginePtr(e); eng != nil {
			return eng.powerPercent
		}
		return 100
	}
	r.ds4EngineSetPower = func(e uintptr, powerPercent int32) int32 {
		if powerPercent < 1 || powerPercent > 100 {
			return 1
		}
		eng := mockEnginePtr(e)
		if eng == nil {
			return 1
		}
		eng.powerPercent = powerPercent
		return 0
	}
	r.ds4EngineVocabSize = func(e uintptr) int32 {
		if mockEnginePtr(e) == nil {
			return 0
		}
		return mockVocabSize
	}
	r.ds4EngineModelName = func(e uintptr) string {
		if mockEnginePtr(e) == nil {
			return ""
		}
		return "Mock"
	}
	r.ds4EngineModelID = func(e uintptr) int32 {
		if mockEnginePtr(e) == nil {
			return 0
		}
		return 0
	}

	// Session persistence.
	r.ds4SessionTokens = func(s uintptr) *cTokens {
		sess := mockSessionPtr(s)
		if sess == nil {
			return nil
		}
		// Refresh the borrowed snapshot in place so earlier borrows stay valid.
		if sess.tokenSnapshot.V == nil {
			sess.tokenSnapshot.V = mockTokensAlloc()
		}
		if len(sess.evaluated) > mockTokenVecCap {
			panic("mock token vector overflow")
		}
		snap := mockTokensSlice(sess.tokenSnapshot.V)
		*snap = append((*snap)[:0], sess.evaluated...)
		sess.tokenSnapshot.Len = int32(len(*snap))
		sess.tokenSnapshot.Cap = int32(cap(*snap))
		return &sess.tokenSnapshot
	}
	r.ds4SessionPayloadBytes = func(s uintptr) uint64 { return 0 }
	r.ds4SessionSavePayload = func(s uintptr, fp uintptr, err unsafe.Pointer, errLen uintptr) int32 { return 0 }
	r.ds4SessionLoadPayload = func(s uintptr, fp uintptr, payloadBytes uint64, err unsafe.Pointer, errLen uintptr) int32 { return 0 }
	r.ds4SessionSaveSnapshot = func(s uintptr, snap *cSessionSnapshot, err unsafe.Pointer, errLen uintptr) int32 {
		sess := mockSessionPtr(s)
		if sess == nil {
			return -1
		}
		// Convert evaluated slice to bytes as a dummy snapshot representation
		var data []byte
		var jsonErr error
		if len(sess.evaluated) > 0 {
			data, jsonErr = json.Marshal(sess.evaluated)
			if jsonErr != nil {
				return -1
			}
		}
		if len(data) > 0 {
			snap.Len = uint64(len(data))
			snap.Cap = uint64(len(data))
			snap.Ptr = cMalloc(uintptr(len(data)))
			copy(unsafe.Slice((*byte)(snap.Ptr), len(data)), data)
		} else {
			snap.Len = 0
			snap.Cap = 0
			snap.Ptr = nil
		}
		return 0
	}
	r.ds4SessionLoadSnapshot = func(s uintptr, snap *cSessionSnapshot, err unsafe.Pointer, errLen uintptr) int32 {
		sess := mockSessionPtr(s)
		if sess == nil {
			return -1
		}
		if snap.Ptr == nil || snap.Len == 0 {
			sess.evaluated = nil
			sess.pos = 0
			return 0
		}
		data := unsafe.Slice((*byte)(snap.Ptr), int(snap.Len))
		var ev []int32
		if err := json.Unmarshal(data, &ev); err != nil {
			return -1
		}
		sess.evaluated = ev
		sess.pos = int32(len(ev))
		return 0
	}
	r.ds4SessionSnapshotFree = func(snap *cSessionSnapshot) {
		if snap.Ptr != nil {
			cFree(snap.Ptr)
			snap.Ptr = nil
		}
	}
	r.ds4SessionSetDirectionalSteering = func(s uintptr, file unsafe.Pointer, mode int32, ffn float32, attn float32, threshold float32, scope int32, err unsafe.Pointer, errLen uintptr) int32 {
		return 0
	}
	r.ds4SessionIsDistributed = func(s uintptr) bool {
		return false
	}
	r.ds4SessionDistributedRouteReady = func(s uintptr, err unsafe.Pointer, errLen uintptr) int32 {
		return 0
	}
	r.ds4SessionLayerSliceReset = func(s uintptr, err unsafe.Pointer, errLen uintptr) int32 {
		return 0
	}
	r.ds4SessionEvalLayerSlice = func(s uintptr, tokens *int32, nTokens uint32, pos0 uint32, layerStart uint32, layerEnd uint32, inputHC *float32, outputHC *float32, outputLogits bool, logits *float32, err unsafe.Pointer, errLen uintptr) int32 {
		return 0
	}
	r.ds4SessionEvalOutputHeadFromHC = func(s uintptr, hiddenHC *float32, nTokens uint32, logits *float32, err unsafe.Pointer, errLen uintptr) int32 {
		return 0
	}
	r.ds4SessionLayerPayloadBytes = func(s uintptr, layerStart uint32, layerEnd uint32) uint64 {
		return 0
	}
	r.ds4SessionSaveLayerPayload = func(s uintptr, fp uintptr, layerStart uint32, layerEnd uint32, err unsafe.Pointer, errLen uintptr) int32 {
		return 0
	}
	r.ds4SessionLoadLayerPayload = func(s uintptr, fp uintptr, payloadBytes uint64, tokens *int32, nTokens uint32, layerStart uint32, layerEnd uint32, err unsafe.Pointer, errLen uintptr) int32 {
		return 0
	}

	r.ds4EngineLayerCount = func(e uintptr) int32 {
		return 61
	}
	r.ds4EngineLayerCompressRatio = func(e uintptr, layer uint32) uint32 {
		return 100
	}

	// Vision.
	r.ds4EngineHasVision = func(e uintptr) bool { return mockEnginePtr(e) != nil && ctl.hasVision() }
	r.ds4EngineEmbdDim = func(e uintptr) int32 { return mockEmbdDim }
	r.ds4EngineVisionEncodeMemory = func(e uintptr, encoded unsafe.Pointer, n uintptr, out *cVisionEmbedding, err unsafe.Pointer, errCap uintptr) int32 {
		if !ctl.hasVision() {
			mockWriteError(err, errCap, "vision encoder is not loaded")
			return 0
		}
		mockVisionEncode(unsafe.Slice((*byte)(encoded), int(n)), out)
		return 1
	}
	r.ds4EngineVisionEncodeFile = func(e uintptr, path string, out *cVisionEmbedding, err unsafe.Pointer, errCap uintptr) int32 {
		if !ctl.hasVision() {
			mockWriteError(err, errCap, "vision encoder is not loaded")
			return 0
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			mockWriteError(err, errCap, rerr.Error())
			return 0
		}
		mockVisionEncode(data, out)
		return 1
	}
	r.ds4VisionEmbeddingFree = func(emb *cVisionEmbedding) {
		if emb != nil && emb.Data != nil {
			cFree(emb.Data)
		}
		*emb = cVisionEmbedding{}
	}
	r.ds4PromptAppendVision = func(e uintptr, tokens *cTokens, span *cVisionSpan, emb *cVisionEmbedding, err unsafe.Pointer, errCap uintptr) int32 {
		if !ctl.hasVision() || emb == nil || emb.Data == nil {
			mockWriteError(err, errCap, "invalid vision prompt input")
			return 0
		}
		mockTokensPush(tokens, mockImageStart)
		*span = cVisionSpan{TokenStart: uint32(tokens.Len)}
		for i := uint32(0); i < emb.TokenCount; i++ {
			mockTokensPush(tokens, mockImageToken)
		}
		mockTokensPush(tokens, mockImageEnd)
		span.Embedding = *emb
		*emb = cVisionEmbedding{}
		return 1
	}
	r.ds4ChatAppendMultimodalMessage = func(e uintptr, tokens *cTokens, role string, textParts unsafe.Pointer, embeddings unsafe.Pointer, imageCount uintptr, spans unsafe.Pointer, err unsafe.Pointer, errCap uintptr) int32 {
		parts := unsafe.Slice((**byte)(textParts), int(imageCount)+1)
		text := func(i int) string { return goString(unsafe.Pointer(parts[i])) }
		if imageCount == 0 {
			mockChatAppendMessage(e, tokens, role, text(0))
			return 1
		}
		if !ctl.hasVision() {
			mockWriteError(err, errCap, "model does not support image messages")
			return 0
		}
		embs := unsafe.Slice((*cVisionEmbedding)(embeddings), int(imageCount))
		outSpans := unsafe.Slice((*cVisionSpan)(spans), int(imageCount))
		oldLen := tokens.Len
		failAt := ctl.multimodalAppendFailAt()
		for _, word := range strings.Fields(role + ":") {
			mockTokensPush(tokens, mockWordToken(word))
		}
		for i := range embs {
			for _, word := range strings.Fields(text(i)) {
				mockTokensPush(tokens, mockWordToken(word))
			}
			if i == failAt {
				// Upstream unwinds by handing the already-moved images back
				// through the embeddings array (their replacement buffers,
				// the originals are gone) and rewinding the tokens.
				for j := 0; j < i; j++ {
					embs[j] = outSpans[j].Embedding
					outSpans[j] = cVisionSpan{}
				}
				tokens.Len = oldLen
				mockWriteError(err, errCap, "invalid DeepSeek vision embedding layout")
				return 0
			}
			outSpans[i] = cVisionSpan{TokenStart: uint32(tokens.Len)}
			for k := uint32(0); k < embs[i].TokenCount; k++ {
				mockTokensPush(tokens, mockImageToken)
			}
			// The DeepSeek path rebuilds the block and frees the caller's
			// buffer; the span carries the replacement.
			outSpans[i].Embedding = mockReplaceEmbedding(embs[i])
			embs[i] = cVisionEmbedding{}
		}
		for _, word := range strings.Fields(text(int(imageCount))) {
			mockTokensPush(tokens, mockWordToken(word))
		}
		return 1
	}

	mockIdentities := func(images unsafe.Pointer, n uintptr) []mockImageIdentity {
		if n == 0 {
			return nil
		}
		c := unsafe.Slice((*cVisionSpan)(images), int(n))
		out := make([]mockImageIdentity, len(c))
		for i := range c {
			out[i] = mockImageIdentity{start: int(c[i].TokenStart), count: int(c[i].Embedding.TokenCount), fp: c[i].Embedding.Fingerprint}
		}
		return out
	}
	r.ds4SessionSyncMultimodal = func(s uintptr, prompt *cTokens, images unsafe.Pointer, n uintptr, err unsafe.Pointer, errLen uintptr) int32 {
		ctl.count(&ctl.multimodalSyncs)
		sess := mockSessionPtr(s)
		if sess == nil {
			return -1
		}
		if n != 0 && !ctl.hasVision() {
			mockWriteError(err, errLen, "vision encoder is not loaded")
			return 1
		}
		ids := mockIdentities(images, n)
		prev := 0
		for _, id := range ids {
			if id.count == 0 || id.start < prev || id.start+id.count > int(prompt.Len) {
				mockWriteError(err, errLen, "invalid or overlapping image token span")
				return 1
			}
			prev = id.start + id.count
		}
		if rc := mockSessionSync(s, prompt, err, errLen); rc != 0 {
			return rc
		}
		sess.images = ids
		return 0
	}
	r.ds4SessionVisionPrefixMatches = func(s uintptr, images unsafe.Pointer, n uintptr) bool {
		sess := mockSessionPtr(s)
		if sess == nil || sess.checkpointInvalid {
			return false
		}
		ids := mockIdentities(images, n)
		if len(ids) < len(sess.images) {
			return false
		}
		for i, have := range sess.images {
			if ids[i] != have {
				return false
			}
		}
		for _, id := range ids[len(sess.images):] {
			if id.start < len(sess.evaluated) {
				return false
			}
		}
		return true
	}
	r.ds4SessionVisionStateMatches = func(s uintptr, images unsafe.Pointer, n uintptr) bool {
		sess := mockSessionPtr(s)
		return sess != nil && int(n) == len(sess.images) && r.ds4SessionVisionPrefixMatches(s, images, n)
	}
	r.ds4SessionRebaseVisionState = func(s uintptr, images unsafe.Pointer, n uintptr) bool {
		sess := mockSessionPtr(s)
		if sess == nil || int(n) != len(sess.images) {
			return false
		}
		c := unsafe.Slice((*cVisionSpan)(images), int(n))
		for i := range c {
			if c[i].Embedding.Fingerprint != sess.images[i].fp || int(c[i].Embedding.TokenCount) != sess.images[i].count {
				return false
			}
		}
		for i := range c {
			c[i].TokenStart = uint32(sess.images[i].start)
		}
		return true
	}
	r.ds4SessionHasVisionState = func(s uintptr) bool {
		sess := mockSessionPtr(s)
		return sess != nil && len(sess.images) > 0
	}

	return lib, ctl
}

// mockThinkModeEnabled mirrors ds4_think_mode_enabled for the mock: thinking
// markers are active for every mode except ThinkNone.
func mockThinkModeEnabled(mode ThinkMode) bool {
	return mode != ThinkNone
}

// ---------------------------------------------------------------------------
// Mock token-vector memory (replaces C heap for cTokens).
// ---------------------------------------------------------------------------

const mockTokenVecCap = 4096

var (
	mockTokensMu sync.Mutex
	mockTokens   = map[unsafe.Pointer]*[]int32{}
)

func mockTokensAlloc() unsafe.Pointer {
	arr := make([]int32, mockTokenVecCap)
	s := arr[:0]
	p := unsafe.Pointer(&arr[0])
	mockTokensMu.Lock()
	mockTokens[p] = &s
	mockTokensMu.Unlock()
	return p
}

func mockTokensSlice(p unsafe.Pointer) *[]int32 {
	mockTokensMu.Lock()
	defer mockTokensMu.Unlock()
	return mockTokens[p]
}

func mockTokensPush(tv *cTokens, token int32) {
	if tv.V == nil {
		tv.V = mockTokensAlloc()
	}
	s := mockTokensSlice(tv.V)
	if len(*s) >= mockTokenVecCap {
		panic("mock token vector overflow")
	}
	*s = append(*s, token)
	tv.Len = int32(len(*s))
	tv.Cap = int32(cap(*s))
}

func mockTokensFree(tv *cTokens) {
	if tv.V != nil {
		mockTokensMu.Lock()
		delete(mockTokens, tv.V)
		mockTokensMu.Unlock()
		tv.V = nil
	}
	tv.Len = 0
	tv.Cap = 0
}

func mockTokensCopy(dst, src *cTokens) {
	if src.V == nil || src.Len == 0 {
		*dst = cTokens{}
		return
	}
	orig := (*mockTokensSlice(src.V))[:src.Len]
	dst.V = mockTokensAlloc()
	s := mockTokensSlice(dst.V)
	*s = append(*s, orig...)
	dst.Len = int32(len(*s))
	dst.Cap = int32(cap(*s))
}

func mockTokensStartsWith(tokens *cTokens, prefix *cTokens) bool {
	if tokens == nil || prefix == nil || tokens.Len < prefix.Len {
		return false
	}
	a := (*mockTokensSlice(tokens.V))[:tokens.Len]
	b := (*mockTokensSlice(prefix.V))[:prefix.Len]
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Mock engine & session state.
// ---------------------------------------------------------------------------

type mockEngine struct {
	eosToken                     int32
	hasMTP                       bool
	hasOutputHead                bool
	mtpDraft                     int32
	nextToken                    int32
	powerPercent                 int32
	steeringFFN                  float32
	exactSampling                bool
	contextSize                  int32
	placementCtxHint             int32
	placementSessionCountHint    int32
	shareSessionPrefillWorkspace bool
	cudaTensorParallel           bool
	ssdStreamingFullLayers       uint32
	ssdStreamingFullLayersSet    bool
}

type mockSession struct {
	engine    *mockEngine
	pos       int32
	evaluated []int32
	// checkpointInvalid mirrors !s->checkpoint_valid after a DeepSeek rewind:
	// sampling returns -1 and eval fails until the prefix is synced again.
	checkpointInvalid bool
	ctxSize           int32
	tokenSnapshot     cTokens
	cancelID          uintptr
	images            []mockImageIdentity
}

// mockImageIdentity records one image's offset, row count, and content
// fingerprint as the mock session last synced it.
type mockImageIdentity struct {
	start, count int
	fp           [32]byte
}

var (
	mockStateMu   sync.Mutex
	mockEngines           = map[uintptr]*mockEngine{}
	mockSessions          = map[uintptr]*mockSession{}
	mockNextPtr   uintptr = 1
	mockTokenMap          = map[string]int32{}
	mockTokenNext int32   = 100
)

func mockAlloc(v any) uintptr {
	mockStateMu.Lock()
	defer mockStateMu.Unlock()
	p := mockNextPtr
	mockNextPtr++
	switch val := v.(type) {
	case *mockEngine:
		mockEngines[p] = val
	case *mockSession:
		mockSessions[p] = val
	}
	return p
}

func mockEnginePtr(p uintptr) *mockEngine {
	mockStateMu.Lock()
	defer mockStateMu.Unlock()
	return mockEngines[p]
}

func mockSessionPtr(p uintptr) *mockSession {
	mockStateMu.Lock()
	defer mockStateMu.Unlock()
	return mockSessions[p]
}

func mockFreeEngine(p uintptr) {
	mockStateMu.Lock()
	delete(mockEngines, p)
	mockStateMu.Unlock()
}

func mockFreeSession(p uintptr) {
	mockStateMu.Lock()
	delete(mockSessions, p)
	mockStateMu.Unlock()
}

// ---------------------------------------------------------------------------
// Mock symbol implementations.
// ---------------------------------------------------------------------------

// mockEngineOptions records the last cEngineOptions handed to ds4_engine_open
// so tests can assert that public options reach the C struct.
var (
	mockEngineOptionsMu   sync.Mutex
	mockLastEngineOptions cEngineOptions
)

// lastMockEngineOptions returns the options from the most recent mock
// ds4_engine_open call.
func lastMockEngineOptions() cEngineOptions {
	mockEngineOptionsMu.Lock()
	defer mockEngineOptionsMu.Unlock()
	return mockLastEngineOptions
}

func mockEngineOpen(out *uintptr, opt *cEngineOptions) int32 {
	if opt != nil {
		mockEngineOptionsMu.Lock()
		mockLastEngineOptions = *opt
		mockEngineOptionsMu.Unlock()
	}
	eng := &mockEngine{eosToken: 1, hasMTP: false, hasOutputHead: true, mtpDraft: 1, nextToken: 42}
	// The real engine enables MTP when a draft model is supplied, so the mock
	// does too: otherwise the speculative paths are unreachable in tests.
	if opt != nil && opt.MTPPath != nil {
		eng.hasMTP = true
		if opt.MTPDraftTokens > 1 {
			eng.mtpDraft = opt.MTPDraftTokens
		}
	}
	// GLM's embedded MTP block: upstream reports mtp_draft_tokens == 2 while
	// has_mtp stays false (that needs an external support model).
	if opt != nil && (opt.GLMMTP || opt.GLMMTPTiming) && opt.MTPPath == nil {
		eng.mtpDraft = 2
	}
	if opt != nil {
		eng.exactSampling = opt.DsparkExactSampling
		eng.powerPercent = opt.PowerPercent
		eng.steeringFFN = opt.DirectionalSteeringFFN
		eng.contextSize = opt.ContextSize
		eng.placementCtxHint = opt.PlacementCtxHint
		eng.placementSessionCountHint = opt.PlacementSessionCountHint
		eng.shareSessionPrefillWorkspace = opt.ShareSessionPrefillWorkspace
		eng.cudaTensorParallel = opt.CUDATensorParallel
		eng.ssdStreamingFullLayers = opt.SSDStreamingFullLayers
		eng.ssdStreamingFullLayersSet = opt.SSDStreamingFullLayersSet
		if eng.powerPercent == 0 {
			eng.powerPercent = 100
		}
	}
	*out = mockAlloc(eng)
	return 0
}

func mockEngineClose(e uintptr) { mockFreeEngine(e) }

func mockTokenEOS(e uintptr) int32 {
	if eng := mockEnginePtr(e); eng != nil {
		return eng.eosToken
	}
	return 1
}

func mockTokenText(e uintptr, token int32, length *uintptr) unsafe.Pointer {
	text := fmt.Sprintf("tok%d", token)
	*length = uintptr(len(text))
	ptr := cMalloc(*length + 1)
	if ptr == nil {
		return nil
	}
	b := unsafe.Slice((*byte)(ptr), int(*length+1))
	copy(b, text)
	b[len(text)] = 0
	return ptr
}

func mockEngineHasOutputHead(e uintptr) bool {
	if eng := mockEnginePtr(e); eng != nil {
		return eng.hasOutputHead
	}
	return false
}

func mockEngineHasMTP(e uintptr) bool {
	if eng := mockEnginePtr(e); eng != nil {
		return eng.hasMTP
	}
	return false
}

func mockEngineMTPDraftTokens(e uintptr) int32 {
	if eng := mockEnginePtr(e); eng != nil {
		return eng.mtpDraft
	}
	return 1
}

func mockTokenizeText(e uintptr, text string, out *cTokens) {
	for _, word := range strings.Fields(text) {
		mockTokensPush(out, mockWordToken(word))
	}
}

func mockWordToken(word string) int32 {
	mockStateMu.Lock()
	defer mockStateMu.Unlock()
	if id, ok := mockTokenMap[word]; ok {
		return id
	}
	mockTokenNext++
	mockTokenMap[word] = mockTokenNext
	return mockTokenNext
}

func mockChatBegin(e uintptr, tokens *cTokens) {}

func mockEncodeChatPrompt(e uintptr, system string, prompt string, thinkMode ThinkMode, out *cTokens) {
	if thinkMode == ThinkMax {
		mockTokenizeText(e, "<think_max>", out)
	}
	for _, word := range strings.Fields(system + " " + prompt) {
		mockTokensPush(out, mockWordToken(word))
	}
}

func mockChatAppendMessage(e uintptr, tokens *cTokens, role string, content string) {
	for _, word := range strings.Fields(role + ": " + content) {
		mockTokensPush(tokens, mockWordToken(word))
	}
}

func mockSessionCreate(out *uintptr, e uintptr, ctxSize int32) int32 {
	eng := mockEnginePtr(e)
	if eng == nil {
		return -1
	}
	sess := &mockSession{engine: eng, ctxSize: ctxSize}
	*out = mockAlloc(sess)
	return 0
}

func mockSessionFree(s uintptr) { mockFreeSession(s) }

func mockSessionSync(s uintptr, prompt *cTokens, err unsafe.Pointer, errLen uintptr) int32 {
	sess := mockSessionPtr(s)
	if sess == nil {
		return -1
	}
	if invokeCancelCallback(sess.cancelID) {
		return sessionSyncInterruptedCode
	}
	if prompt != nil && prompt.V != nil {
		src := *mockTokensSlice(prompt.V)
		sess.evaluated = append([]int32(nil), src[:prompt.Len]...)
		sess.pos = int32(len(sess.evaluated))
	}
	sess.checkpointInvalid = false
	return 0
}

// mockWriteError copies msg into a libds4-style error buffer.
func mockWriteError(err unsafe.Pointer, errLen uintptr, msg string) {
	if err == nil || errLen == 0 {
		return
	}
	buf := unsafe.Slice((*byte)(err), int(errLen))
	n := copy(buf[:len(buf)-1], msg)
	buf[n] = 0
}

// mockReplaceEmbedding mirrors ds4_prompt_append_deepseek4_vision's handling
// of the caller's buffer: the rows are copied into a fresh malloc block and
// the original is freed, so the embedding the span carries is a different
// allocation from the one the caller passed in.
func mockReplaceEmbedding(emb cVisionEmbedding) cVisionEmbedding {
	size := uintptr(emb.TokenCount) * uintptr(mockEmbdDim) * 4
	block := cMalloc(size)
	copy(unsafe.Slice((*byte)(block), size), unsafe.Slice((*byte)(emb.Data), size))
	cFree(emb.Data)
	emb.Data = block
	return emb
}

// mockVisionEncode builds a deterministic embedding: 1 + len%4 rows of
// mockEmbdDim floats holding the row index, fingerprint sha256(bytes).
func mockVisionEncode(bytes []byte, out *cVisionEmbedding) {
	rows := 1 + len(bytes)%4
	data := cMalloc(uintptr(rows) * uintptr(mockEmbdDim) * 4)
	floats := unsafe.Slice((*float32)(data), rows*int(mockEmbdDim))
	for i := range floats {
		floats[i] = float32(i / int(mockEmbdDim))
	}
	*out = cVisionEmbedding{Data: data, TokenCount: uint32(rows), Layout: 0, GridWidth: uint32(rows), GridHeight: 1, Width: 64, Height: 64, ContentWidth: 64, ContentHeight: 64, Fingerprint: sha256.Sum256(bytes)}
}

func mockSessionArgmax(s uintptr) int32 {
	sess := mockSessionPtr(s)
	if sess == nil {
		return 0
	}
	if sess.checkpointInvalid {
		return -1
	}
	return sess.engine.nextToken + sess.pos
}

func mockSessionSample(s uintptr, temperature float32, topK int32, topP float32, minP float32, rng *uint64) int32 {
	return mockSessionArgmax(s)
}

func mockSessionEval(s uintptr, token int32, err unsafe.Pointer, errLen uintptr) int32 {
	time.Sleep(500 * time.Microsecond)
	sess := mockSessionPtr(s)
	if sess == nil {
		return -1
	}
	if sess.checkpointInvalid {
		mockWriteError(err, errLen, "decode requires a synchronized checkpoint")
		return 1
	}
	sess.evaluated = append(sess.evaluated, token)
	sess.pos++
	return 0
}

// mockSessionEvalSpeculative mirrors the argmax mock; the sampling parameters
// do not change which drafts the mock accepts, only that the call is wired.
func mockSessionEvalSpeculative(s uintptr, firstToken int32, maxTokens int32, eosToken int32,
	temperature float32, topK int32, topP float32, minP float32, rng *uint64,
	accepted unsafe.Pointer, acceptedCap int32, err unsafe.Pointer, errLen uintptr) int32 {
	return mockSessionEvalSpeculativeArgmax(s, firstToken, maxTokens, eosToken, accepted, acceptedCap, err, errLen)
}

func mockSessionEvalSpeculativeArgmax(s uintptr, firstToken int32, maxTokens int32, eosToken int32, accepted unsafe.Pointer, acceptedCap int32, err unsafe.Pointer, errLen uintptr) int32 {
	sess := mockSessionPtr(s)
	if sess == nil {
		return -1
	}
	if maxTokens <= 0 {
		return 0
	}
	draft := make([]int32, 0, maxTokens)
	draft = append(draft, firstToken)
	sess.evaluated = append(sess.evaluated, firstToken)
	sess.pos++
	for int32(len(draft)) < maxTokens {
		time.Sleep(500 * time.Microsecond)
		next := sess.engine.nextToken + sess.pos
		if eosToken >= 0 && next == eosToken {
			break
		}
		draft = append(draft, next)
		sess.evaluated = append(sess.evaluated, next)
		sess.pos++
	}
	if accepted != nil && acceptedCap > 0 {
		n := len(draft)
		if n > int(acceptedCap) {
			n = int(acceptedCap)
		}
		dest := unsafe.Slice((*int32)(accepted), int(acceptedCap))
		copy(dest, draft[:n])
	}
	return int32(len(draft))
}

func mockSessionPos(s uintptr) int32 {
	sess := mockSessionPtr(s)
	if sess == nil {
		return 0
	}
	return sess.pos
}

func mockSessionCtx(s uintptr) int32 {
	sess := mockSessionPtr(s)
	if sess == nil {
		return 0
	}
	return sess.ctxSize
}
