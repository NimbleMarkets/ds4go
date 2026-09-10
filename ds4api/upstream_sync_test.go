package ds4api

import (
	"errors"
	"testing"
	"unsafe"
)

// Upstream ds4 commit fc8bf3c added vision_path to ds4_engine_options; the Go
// wrapper must hand it through so libds4 can load the encoder.
func TestNewEngineSetsVisionPath(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{VisionPath: "/models/encoder.gguf"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	got := lastMockEngineOptions()
	if got.VisionPath == nil {
		t.Fatal("vision_path = NULL, want the encoder path")
	}
	if s := goString(got.VisionPath); s != "/models/encoder.gguf" {
		t.Fatalf("vision_path = %q, want /models/encoder.gguf", s)
	}
}

func TestNewEngineLeavesVisionPathNullWhenUnset(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if lastMockEngineOptions().VisionPath != nil {
		t.Fatal("vision_path set without EngineOptions.VisionPath, want NULL")
	}
}

// ds4_engine_collect_imatrix gained a trailing min_expert_samples argument
// (upstream c8b6e4f). The binding must pass it, and the legacy wrapper must
// pass 0, the upstream CLI default.
func TestCollectIMatrixPassesMinExpertSamples(t *testing.T) {
	lib := NewMockLibrary()
	var gotMin int32 = -1
	var calls int
	lib.raw.ds4EngineCollectIMatrix = func(e uintptr, datasetPath string, outputPath string, ctxSize int32, maxPrompts int32, maxTokens int32, minExpertSamples int32) int32 {
		calls++
		gotMin = minExpertSamples
		return 0
	}
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if err := eng.CollectIMatrixWithMinExpertSamples("data.txt", "out.imatrix", 4096, 10, 100, 32); err != nil {
		t.Fatalf("CollectIMatrixWithMinExpertSamples: %v", err)
	}
	if gotMin != 32 {
		t.Fatalf("min_expert_samples = %d, want 32", gotMin)
	}
	if err := eng.CollectIMatrix("data.txt", "out.imatrix", 4096, 10, 100); err != nil {
		t.Fatalf("CollectIMatrix: %v", err)
	}
	if gotMin != 0 {
		t.Fatalf("legacy CollectIMatrix min_expert_samples = %d, want 0", gotMin)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// Upstream 87495f6 exported ds4_session_directional_steering_ffn and
// ds4_session_set_directional_steering_ffn for live steering changes.
func TestSessionDirectionalSteeringFFNRoundTrips(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{DirectionalSteeringFFN: 1.5})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(1024)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	if got := sess.DirectionalSteeringFFN(); got != 1.5 {
		t.Fatalf("DirectionalSteeringFFN() = %v, want 1.5 (inherited from engine)", got)
	}
	if err := sess.SetDirectionalSteeringFFN(-2); err != nil {
		t.Fatalf("SetDirectionalSteeringFFN(-2): %v", err)
	}
	if got := sess.DirectionalSteeringFFN(); got != -2 {
		t.Fatalf("DirectionalSteeringFFN() after set = %v, want -2", got)
	}
	// libds4 rejects non-finite values and magnitudes above 100.
	if err := sess.SetDirectionalSteeringFFN(101); err == nil {
		t.Fatal("SetDirectionalSteeringFFN(101) = nil, want error")
	}
}

func TestSessionDirectionalSteeringFFNUnsupported(t *testing.T) {
	lib := NewMockLibrary()
	lib.raw.ds4SessionDirectionalSteeringFFN = nil
	lib.raw.ds4SessionSetDirectionalSteeringFFN = nil
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(1024)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	if lib.SupportsLiveSteeringFFN() {
		t.Fatal("SupportsLiveSteeringFFN() = true without the symbols")
	}
	if got := sess.DirectionalSteeringFFN(); got != 0 {
		t.Fatalf("DirectionalSteeringFFN() = %v without the symbol, want 0", got)
	}
	if err := sess.SetDirectionalSteeringFFN(1); !errors.Is(err, ErrSteeringNotSupported) {
		t.Fatalf("SetDirectionalSteeringFFN error = %v, want ErrSteeringNotSupported", err)
	}
}

var _ = unsafe.Pointer(nil)

// GLM's embedded MTP block is enabled through glm_mtp / glm_mtp_timing, which
// the Go options never exposed even though the C mirror carried them.
func TestNewEngineSetsGLMMTP(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{GLMMTP: true, GLMMTPTiming: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	got := lastMockEngineOptions()
	if !got.GLMMTP || !got.GLMMTPTiming {
		t.Fatalf("glm_mtp=%v glm_mtp_timing=%v, want both true", got.GLMMTP, got.GLMMTPTiming)
	}
	// Upstream reports the embedded block through mtp_draft_tokens > 1 while
	// has_mtp stays false (that needs an external support model).
	if eng.HasMTP() {
		t.Error("HasMTP() = true for embedded GLM MTP, want false")
	}
	if got := eng.MTPDraftTokens(); got <= 1 {
		t.Errorf("MTPDraftTokens() = %d, want > 1 with embedded MTP", got)
	}
}

// ds4_engine_mtp_exact_sampling (upstream 930ab73) reports whether exact
// stochastic acceptance is active for DSpark or the GLM MTP block.
func TestEngineMTPExactSampling(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{DsparkExactSampling: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if !eng.MTPExactSampling() {
		t.Error("MTPExactSampling() = false with DsparkExactSampling set, want true")
	}
	lib.raw.ds4EngineMTPExactSampling = nil
	if eng.MTPExactSampling() {
		t.Error("MTPExactSampling() = true without the symbol, want false")
	}
}

// Upstream 233eeb8: a plain rewind leaves DeepSeek without a valid checkpoint,
// so argmax returns -1 and common_prefix returns 0 until the retained prefix
// is synced again. GLM keeps its state.
func TestSessionRewindInvalidatesDeepSeekCheckpoint(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(64)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	prompt := []int{11, 12, 13, 14, 15}
	if err := sess.Sync(prompt); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := sess.Argmax(); got < 0 {
		t.Fatalf("Argmax() after sync = %d, want a token", got)
	}
	sess.Rewind(3)
	if got := sess.Pos(); got != 3 {
		t.Fatalf("Pos() after Rewind(3) = %d, want 3", got)
	}
	if got := sess.Argmax(); got != -1 {
		t.Errorf("Argmax() after DeepSeek rewind = %d, want -1 (no checkpoint)", got)
	}
	retained := sess.Tokens()
	if got := retained.Slice(); len(got) != 3 || got[0] != 11 || got[2] != 13 {
		t.Errorf("Tokens() after rewind = %v, want [11 12 13]", got)
	}
	if got := sess.CommonPrefix(retained); got != 0 {
		t.Errorf("CommonPrefix() after DeepSeek rewind = %d, want 0", got)
	}
	if err := sess.Eval(99); err == nil {
		t.Error("Eval() after DeepSeek rewind = nil, want checkpoint error")
	}

	ctl.SetGLM(true)
	if err := sess.Sync(prompt); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	sess.Rewind(2)
	if got := sess.Argmax(); got < 0 {
		t.Errorf("Argmax() after GLM rewind = %d, want a token (state retained)", got)
	}
	if got := sess.CommonPrefix(sess.Tokens()); got != 2 {
		t.Errorf("CommonPrefix() after GLM rewind = %d, want 2", got)
	}
}

// RewindSynced mirrors upstream agent_worker_rewind / server_generation_rewind:
// rewind, then re-sync the retained prefix only when the checkpoint was lost.
func TestSessionRewindSyncedRestoresCheckpoint(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(64)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	if err := sess.Sync([]int{11, 12, 13, 14, 15}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := sess.RewindSynced(3); err != nil {
		t.Fatalf("RewindSynced(3): %v", err)
	}
	if got := sess.Pos(); got != 3 {
		t.Errorf("Pos() = %d, want 3", got)
	}
	if got := sess.Argmax(); got < 0 {
		t.Errorf("Argmax() after RewindSynced = %d, want a token", got)
	}
	if got := ctl.SyncCalls(); got != 2 {
		t.Errorf("sync calls = %d, want 2 (initial + DeepSeek rebuild)", got)
	}
	if err := sess.Eval(99); err != nil {
		t.Errorf("Eval() after RewindSynced: %v", err)
	}

	// GLM keeps its recurrent state on rewind, so no rebuild is issued.
	ctl.SetGLM(true)
	if err := sess.RewindSynced(2); err != nil {
		t.Fatalf("RewindSynced(2) on GLM: %v", err)
	}
	if got := ctl.SyncCalls(); got != 2 {
		t.Errorf("sync calls after GLM rewind = %d, want 2 (no rebuild)", got)
	}
	if got := sess.Pos(); got != 2 {
		t.Errorf("Pos() = %d, want 2", got)
	}
}
