package ds4api

import "unsafe"

type cTokens struct {
	V   unsafe.Pointer
	Len int32
	Cap int32
}

type cTokenScore struct {
	ID      int32
	Logit   float32
	Logprob float32
}

type cDistributedLayers struct {
	Start     uint32
	End       uint32
	HasOutput bool
	Set       bool
}

type cDistributedOptions struct {
	Role            int32
	Layers          cDistributedLayers
	ListenHost      unsafe.Pointer
	ListenPort      int32
	CoordinatorHost unsafe.Pointer
	CoordinatorPort int32
	PrefillChunk    uint32
	PrefillWindow   uint32
	ActivationBits  uint32
	ReplayCheck     bool
	Debug           bool
}

type cEngineOptions struct {
	ModelPath               unsafe.Pointer
	MTPPath                 unsafe.Pointer
	Backend                 Backend
	NThreads                int32
	MTPDraftTokens          int32
	MTPMargin               float32
	DirectionalSteeringFile unsafe.Pointer
	DirectionalSteeringAttn float32
	DirectionalSteeringFFN  float32
	PowerPercent            int32
	WarmWeights             bool
	Quality                 bool
	InspectOnly             bool
	LoadSlice               bool
	LoadLayerStart          uint32
	LoadLayerEnd            uint32
	LoadOutput              bool
	Distributed             cDistributedOptions
}

type cContextMemory struct {
	TotalBytes      uint64
	RawBytes        uint64
	CompressedBytes uint64
	ScratchBytes    uint64
	PrefillCap      uint32
	RawCap          uint32
	CompCap         uint32
}

type cSessionSnapshot struct {
	Ptr unsafe.Pointer
	Len uint64
	Cap uint64
}

type rawSymbols struct {
	ds4EngineOpen                    func(out *uintptr, opt *cEngineOptions) int32
	ds4EngineClose                   func(e uintptr)
	ds4EngineSummary                 func(e uintptr)
	ds4EnginePower                   func(e uintptr) int32
	ds4EngineSetPower                func(e uintptr, powerPercent int32) int32
	ds4EngineVocabSize               func(e uintptr) int32
	ds4EngineModelName               func(e uintptr) string
	ds4EngineModelID                 func(e uintptr) int32
	ds4BackendName                   func(backend Backend) string
	ds4ThinkModeEnabled              func(mode ThinkMode) bool
	ds4ThinkModeName                 func(mode ThinkMode) string
	ds4ThinkMaxPrefix                func() string
	ds4ThinkMaxMinContext            func() uint32
	ds4ThinkModeForContext           func(mode ThinkMode, ctxSize int32) ThinkMode
	ds4ContextMemoryEstimate         func(backend Backend, ctxSize int32) cContextMemory
	ds4LogIsTTY                      func(fp uintptr) bool
	ds4LogString                     func(fp uintptr, typ LogType, format string, msg string)
	ds4LogSet                        func(fn uintptr, ud uintptr)
	ds4AbortSet                      func(fn uintptr, ud uintptr)
	ds4EngineGenerateArgmax          func(e uintptr, prompt *cTokens, nPredict int32, ctxSize int32, emit uintptr, done uintptr, emitUD uintptr, progress uintptr, progressUD uintptr) int32
	ds4EngineCollectIMatrix          func(e uintptr, datasetPath string, outputPath string, ctxSize int32, maxPrompts int32, maxTokens int32) int32
	ds4EngineDumpTokens              func(e uintptr, tokens *cTokens)
	ds4DumpTextTokenization          func(modelPath string, text string, fp uintptr) int32
	ds4EngineHeadTest                func(e uintptr, prompt *cTokens) int32
	ds4EngineFirstTokenTest          func(e uintptr, prompt *cTokens) int32
	ds4EngineMetalGraphTest          func(e uintptr, prompt *cTokens) int32
	ds4EngineMetalGraphFullTest      func(e uintptr, prompt *cTokens) int32
	ds4EngineMetalGraphPromptTest    func(e uintptr, prompt *cTokens, ctxSize int32) int32
	ds4TokensPush                    func(tv *cTokens, token int32)
	ds4TokensFree                    func(tv *cTokens)
	ds4TokensCopy                    func(dst *cTokens, src *cTokens)
	ds4TokensStartsWith              func(tokens *cTokens, prefix *cTokens) bool
	ds4TokenizeText                  func(e uintptr, text string, out *cTokens)
	ds4TokenizeRenderedChat          func(e uintptr, text string, out *cTokens)
	ds4ChatBegin                     func(e uintptr, tokens *cTokens)
	ds4EncodeChatPrompt              func(e uintptr, system string, prompt string, thinkMode ThinkMode, out *cTokens)
	ds4ChatAppendMaxEffortPrefix     func(e uintptr, tokens *cTokens)
	ds4ChatAppendMessage             func(e uintptr, tokens *cTokens, role string, content string)
	ds4ChatAppendAssistantPrefix     func(e uintptr, tokens *cTokens, thinkMode ThinkMode)
	ds4TokenText                     func(e uintptr, token int32, length *uintptr) unsafe.Pointer
	ds4TokenEOS                      func(e uintptr) int32
	ds4TokenUser                     func(e uintptr) int32
	ds4TokenAssistant                func(e uintptr) int32
	ds4SessionCreate                 func(out *uintptr, e uintptr, ctxSize int32) int32
	ds4SessionFree                   func(s uintptr)
	ds4SessionPower                  func(s uintptr) int32
	ds4SessionSetPower               func(s uintptr, powerPercent int32) int32
	ds4SessionSetProgress            func(s uintptr, fn uintptr, ud uintptr)
	ds4SessionSetDisplayProgress     func(s uintptr, fn uintptr, ud uintptr)
	ds4SessionSync                   func(s uintptr, prompt *cTokens, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionRewriteRequiresRebuild func(liveLen int32, canonicalLen int32, common int32) bool
	ds4SessionRewriteFromCommon      func(s uintptr, prompt *cTokens, common int32, err unsafe.Pointer, errLen uintptr) SessionRewriteResult
	ds4SessionCommonPrefix           func(s uintptr, prompt *cTokens) int32
	ds4SessionArgmax                 func(s uintptr) int32
	ds4SessionArgmaxExcluding        func(s uintptr, excludedID int32) int32
	ds4SessionSample                 func(s uintptr, temperature float32, topK int32, topP float32, minP float32, rng *uint64) int32
	ds4SessionTopLogprobs            func(s uintptr, out *cTokenScore, k int32) int32
	ds4SessionTokenLogprob           func(s uintptr, token int32, out *cTokenScore) int32
	ds4SessionCopyLogits             func(s uintptr, out unsafe.Pointer, cap int32) int32
	ds4SessionEval                   func(s uintptr, token int32, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionEvalSpeculativeArgmax  func(s uintptr, firstToken int32, maxTokens int32, eosToken int32, accepted unsafe.Pointer, acceptedCap int32, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionInvalidate             func(s uintptr)
	ds4SessionRewind                 func(s uintptr, pos int32)
	ds4SessionPos                    func(s uintptr) int32
	ds4SessionCtx                    func(s uintptr) int32
	ds4EngineRoutedQuantBits         func(e uintptr) int32
	ds4EngineHasOutputHead           func(e uintptr) bool
	ds4EngineHasMTP                  func(e uintptr) bool
	ds4EngineMTPDraftTokens          func(e uintptr) int32
	ds4SessionTokens                 func(s uintptr) *cTokens
	ds4SessionPayloadBytes           func(s uintptr) uint64
	ds4SessionSavePayload            func(s uintptr, fp uintptr, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionLoadPayload            func(s uintptr, fp uintptr, payloadBytes uint64, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionSaveSnapshot           func(s uintptr, snap *cSessionSnapshot, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionLoadSnapshot           func(s uintptr, snap *cSessionSnapshot, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionSnapshotFree           func(snap *cSessionSnapshot)
	ds4SessionSetDirectionalSteering func(s uintptr, file unsafe.Pointer, mode int32, ffn float32, attn float32, threshold float32, scope int32, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionIsDistributed          func(s uintptr) bool
	ds4SessionDistributedRouteReady  func(s uintptr, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionLayerSliceReset        func(s uintptr, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionEvalLayerSlice         func(s uintptr, tokens *int32, nTokens uint32, pos0 uint32, layerStart uint32, layerEnd uint32, inputHC *float32, outputHC *float32, outputLogits bool, logits *float32, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionEvalOutputHeadFromHC   func(s uintptr, hiddenHC *float32, nTokens uint32, logits *float32, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionLayerPayloadBytes      func(s uintptr, layerStart uint32, layerEnd uint32) uint64
	ds4SessionSaveLayerPayload       func(s uintptr, fp uintptr, layerStart uint32, layerEnd uint32, err unsafe.Pointer, errLen uintptr) int32
	ds4SessionLoadLayerPayload       func(s uintptr, fp uintptr, payloadBytes uint64, tokens *int32, nTokens uint32, layerStart uint32, layerEnd uint32, err unsafe.Pointer, errLen uintptr) int32
}
