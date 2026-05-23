// Package ds4api test infrastructure: a pure-Go mock of libds4.
//
// NewMockLibrary creates a Library whose raw function pointers are Go
// implementations backed by in-memory state.  This lets tests exercise the
// ds4go binding and generator layers without loading a real shared library.
package ds4api

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// NewMockLibrary returns a Library whose C symbols are backed by trivial
// in-memory state.  The mock supports engine/session lifecycle, tokenization,
// deterministic generation, and optional MTP metadata.
func NewMockLibrary() *Library {
	lib := &Library{path: "mock", handle: 0}
	r := &lib.raw
	var mockLogFn uintptr
	var mockLogID uintptr

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
	r.ds4LogIsTTY = func(fp uintptr) bool { return false }
	r.ds4LogString = func(fp uintptr, typ LogType, format string, msg string) {
		if mockLogFn == 0 {
			return
		}
		invokeLogCallback(mockLogID, typ, msg)
	}
	r.ds4LogSet = func(fn uintptr, ud uintptr) {
		mockLogFn = fn
		mockLogID = ud
	}
	r.ds4AbortSet = func(fn uintptr, ud uintptr) {}

	// Engine tests & diagnostics.
	r.ds4EngineGenerateArgmax = func(e uintptr, prompt *cTokens, nPredict int32, ctxSize int32, emit uintptr, done uintptr, emitUD uintptr, progress uintptr, progressUD uintptr) int32 {
		return 0
	}
	r.ds4EngineCollectIMatrix = func(e uintptr, datasetPath string, outputPath string, ctxSize int32, maxPrompts int32, maxTokens int32) int32 {
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
	r.ds4TokenUser = func(e uintptr) int32 { return 2 }
	r.ds4TokenAssistant = func(e uintptr) int32 { return 3 }

	// Session.
	r.ds4SessionCreate = mockSessionCreate
	r.ds4SessionFree = mockSessionFree
	r.ds4SessionSetProgress = func(s uintptr, fn uintptr, ud uintptr) {}
	r.ds4SessionSync = mockSessionSync
	r.ds4SessionRewriteRequiresRebuild = func(liveLen int32, canonicalLen int32, common int32) bool { return true }
	r.ds4SessionRewriteFromCommon = func(s uintptr, prompt *cTokens, common int32, err unsafe.Pointer, errLen uintptr) SessionRewriteResult {
		return SessionRewriteError
	}
	r.ds4SessionCommonPrefix = func(s uintptr, prompt *cTokens) int32 { return 0 }
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
	r.ds4SessionTokenLogprob = func(s uintptr, token int32, out *cTokenScore) int32 {
		sess := mockSessionPtr(s)
		if sess == nil {
			return -1
		}
		out.ID = token
		out.Logit = 8.5
		out.Logprob = -0.5
		return 0
	}
	r.ds4SessionEval = mockSessionEval
	r.ds4SessionEvalSpeculativeArgmax = mockSessionEvalSpeculativeArgmax
	r.ds4SessionInvalidate = func(s uintptr) {}
	r.ds4SessionRewind = func(s uintptr, pos int32) {}
	r.ds4SessionPos = mockSessionPos
	r.ds4SessionCtx = mockSessionCtx

	// Engine metadata.
	r.ds4EngineRoutedQuantBits = func(e uintptr) int32 { return 4 }
	r.ds4EngineHasMTP = mockEngineHasMTP
	r.ds4EngineMTPDraftTokens = mockEngineMTPDraftTokens

	// Session persistence.
	r.ds4SessionTokens = func(s uintptr) *cTokens {
		if sess := mockSessionPtr(s); sess != nil {
			return &sess.tokenSnapshot
		}
		return nil
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
	r.ds4SessionSetDirectionalSteering = func(s uintptr, file unsafe.Pointer, mode int32, ffn float32, attn float32, threshold float32, scope int32, err unsafe.Pointer, errLen uintptr) int32 { return 0 }

	return lib
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
	eosToken  int32
	hasMTP    bool
	mtpDraft  int32
	nextToken int32
}

type mockSession struct {
	engine        *mockEngine
	pos           int32
	evaluated     []int32
	ctxSize       int32
	tokenSnapshot cTokens
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

func mockEngineOpen(out *uintptr, opt *cEngineOptions) int32 {
	eng := &mockEngine{eosToken: 1, hasMTP: false, mtpDraft: 1, nextToken: 42}
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
	if prompt != nil && prompt.V != nil {
		src := *mockTokensSlice(prompt.V)
		sess.evaluated = append([]int32(nil), src[:prompt.Len]...)
		sess.pos = int32(len(sess.evaluated))
	}
	return 0
}

func mockSessionArgmax(s uintptr) int32 {
	sess := mockSessionPtr(s)
	if sess == nil {
		return 0
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
	sess.evaluated = append(sess.evaluated, token)
	sess.pos++
	return 0
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
