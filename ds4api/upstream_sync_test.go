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
