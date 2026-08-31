package ds4api

import "testing"

// Positive-temperature DSpark: ds4_session_eval_speculative is the sampled
// counterpart to ds4_session_eval_speculative_argmax, so speculation no longer
// has to be abandoned whenever Temperature > 0.

func TestSessionEvalSpeculativeAcceptsSampledTokens(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	var rng uint64 = 7
	opts := SpeculativeOptions{Temperature: 0.8, TopK: 40, TopP: 0.95, MinP: 0.05, RNG: &rng}
	accepted, err := sess.EvalSpeculative(42, 4, eng.TokenEOS(), opts)
	if err != nil {
		t.Fatalf("EvalSpeculative: %v", err)
	}
	if len(accepted) == 0 {
		t.Fatal("EvalSpeculative accepted no tokens")
	}
	if accepted[0] != 42 {
		t.Errorf("accepted[0] = %d, want the supplied first token 42", accepted[0])
	}
}

func TestSessionEvalSpeculativeZeroMaxTokens(t *testing.T) {
	lib := NewMockLibrary()
	eng, _ := lib.NewEngine(EngineOptions{})
	defer eng.Close()
	sess, _ := eng.NewSession(128)
	defer sess.Close()

	got, err := sess.EvalSpeculative(1, 0, 2, SpeculativeOptions{})
	if err != nil {
		t.Fatalf("EvalSpeculative: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("accepted %d tokens for maxTokens=0, want 0", len(got))
	}
}

// Older libds4 builds predate the sampled entry point, so callers must be able
// to detect it rather than getting a nil-func panic.
func TestSupportsSampledSpeculative(t *testing.T) {
	lib := NewMockLibrary()
	if !lib.SupportsSampledSpeculative() {
		t.Error("SupportsSampledSpeculative() = false on a library exporting it")
	}
	lib.raw.ds4SessionEvalSpeculative = nil
	if lib.SupportsSampledSpeculative() {
		t.Error("SupportsSampledSpeculative() = true without ds4_session_eval_speculative")
	}

	eng, _ := lib.NewEngine(EngineOptions{})
	defer eng.Close()
	sess, _ := eng.NewSession(128)
	defer sess.Close()
	if _, err := sess.EvalSpeculative(1, 4, 2, SpeculativeOptions{Temperature: 0.8}); err == nil {
		t.Error("EvalSpeculative returned no error on a library without the symbol")
	}
}

// The DSpark engine options must reach the C struct; they are the only way to
// turn speculative decoding on at all.
func TestEngineOptionsCarryDsparkFields(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	_ = ctl
	opts := EngineOptions{
		Dspark:                    true,
		DsparkStrict:              true,
		DsparkExactSampling:       true,
		DsparkConfidenceThreshold: 0.75,
		MTPPath:                   "mtp.gguf",
	}
	eng, err := lib.NewEngine(opts)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	got := lastMockEngineOptions()
	if !got.Dspark || !got.DsparkStrict || !got.DsparkExactSampling {
		t.Errorf("dspark flags did not reach the C options: %+v", got)
	}
	if got.DsparkConfidenceThreshold != 0.75 {
		t.Errorf("DsparkConfidenceThreshold = %v, want 0.75", got.DsparkConfidenceThreshold)
	}
	// The threshold is only honoured when its _set companion is true, so the
	// wrapper must set it whenever a threshold is supplied.
	if !got.DsparkConfidenceThresholdSet {
		t.Error("DsparkConfidenceThresholdSet = false despite a threshold being given")
	}
}

func TestEngineOptionsLeaveThresholdUnsetWhenZero(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{Dspark: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	if got := lastMockEngineOptions(); got.DsparkConfidenceThresholdSet {
		t.Error("DsparkConfidenceThresholdSet = true with no threshold supplied")
	}
}
