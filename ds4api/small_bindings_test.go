package ds4api

import (
	"strings"
	"testing"
)

// The small session and engine entry points added since the V4.1 sync are
// bound as optional symbols: they work on the mock, and a library without
// them answers with the documented fallback instead of a nil dereference.

func smallBindingsSession(t *testing.T) (*Library, *MockControls, *Engine, *Session) {
	t.Helper()
	lib, ctl := NewMockLibraryWithControls()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	sess, err := eng.NewSession(256)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	return lib, ctl, eng, sess
}

func TestSetLogitsRoundTripsAndValidatesLength(t *testing.T) {
	_, _, eng, sess := smallBindingsSession(t)
	logits := make([]float32, eng.VocabSize())
	logits[3] = 9
	if err := sess.SetLogits(logits); err != nil {
		t.Fatalf("SetLogits: %v", err)
	}
	got, err := sess.CopyLogits()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(logits) || got[3] != 9 || got[0] != 0 {
		t.Errorf("CopyLogits after SetLogits = %v...", got[:4])
	}
	if err := sess.SetLogits(logits[:len(logits)-1]); err == nil {
		t.Error("SetLogits accepted a short vector")
	}
	if err := sess.SetLogits(nil); err == nil {
		t.Error("SetLogits accepted nil")
	}
}

func TestArgmaxIgnoringEOSSkipsStopTokens(t *testing.T) {
	_, ctl, eng, sess := smallBindingsSession(t)
	eos := eng.TokenEOS()
	logits := make([]float32, eng.VocabSize())
	logits[eos] = 10
	logits[5] = 8
	logits[7] = 6
	if err := sess.SetLogits(logits); err != nil {
		t.Fatal(err)
	}
	if got := sess.ArgmaxIgnoringEOS(ThinkNone); got != 5 {
		t.Errorf("ArgmaxIgnoringEOS(ThinkNone) = %d, want 5 (EOS skipped)", got)
	}
	// A thinking-control token is a stop only when thinking is off, so it
	// is skipped under ThinkNone and eligible under ThinkHigh.
	ctl.SetThinkingControlTokens(5)
	if got := sess.ArgmaxIgnoringEOS(ThinkNone); got != 7 {
		t.Errorf("ArgmaxIgnoringEOS(ThinkNone) with 5 as a thinking control = %d, want 7", got)
	}
	if got := sess.ArgmaxIgnoringEOS(ThinkHigh); got != 5 {
		t.Errorf("ArgmaxIgnoringEOS(ThinkHigh) = %d, want 5", got)
	}
}

func TestSampleLogitsGreedyPicksTheMax(t *testing.T) {
	lib, _, eng, _ := smallBindingsSession(t)
	logits := make([]float32, eng.VocabSize())
	logits[11] = 3
	logits[2] = 1
	var rng uint64 = 42
	if got := lib.SampleLogits(logits, 0, 0, 1, 0, &rng); got != 11 {
		t.Errorf("SampleLogits(greedy) = %d, want 11", got)
	}
	if got := lib.SampleLogits(nil, 0, 0, 1, 0, &rng); got != 0 {
		t.Errorf("SampleLogits(nil) = %d, want 0 as upstream returns", got)
	}
}

func TestEvalSpeculativeArgmaxIgnoringEOSMatchesPlainForm(t *testing.T) {
	_, _, eng, sess := smallBindingsSession(t)
	toks, err := eng.TokenizeText("a b c")
	if err != nil {
		t.Fatal(err)
	}
	defer toks.Free()
	if err := sess.Sync(toks.Slice()); err != nil {
		t.Fatal(err)
	}
	first := sess.Argmax()
	got, err := sess.EvalSpeculativeArgmaxIgnoringEOS(first, 3, eng.TokenEOS(), ThinkHigh)
	if err != nil {
		t.Fatalf("EvalSpeculativeArgmaxIgnoringEOS: %v", err)
	}
	if len(got) == 0 || got[0] != first {
		t.Errorf("accepted = %v, want the fed token first", got)
	}
	if got, err := sess.EvalSpeculativeArgmaxIgnoringEOS(first, 0, eng.TokenEOS(), ThinkHigh); err != nil || got != nil {
		t.Errorf("maxTokens 0 = %v, %v", got, err)
	}
}

func TestReportProgressReachesTheCallback(t *testing.T) {
	_, _, _, sess := smallBindingsSession(t)
	var events []string
	if err := sess.SetProgress(func(event string, current, total int) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	sess.ReportProgress("prefill", 1, 4)
	if len(events) != 1 || events[0] != "prefill" {
		t.Errorf("events = %v, want [prefill]", events)
	}
	sess.GPUWarmup() // no-op on the mock, must not fail
	if err := sess.SetProgress(nil); err != nil {
		t.Fatal(err)
	}
	sess.ReportProgress("prefill", 2, 4)
	if len(events) != 1 {
		t.Errorf("progress delivered after the callback was cleared: %v", events)
	}
}

func TestIsGLM53AndDumpChatTokenization(t *testing.T) {
	lib, ctl, eng, _ := smallBindingsSession(t)
	if eng.IsGLM53() {
		t.Error("fresh mock reports GLM 5.3")
	}
	ctl.SetGLM53(true)
	if !eng.IsGLM53() {
		t.Error("IsGLM53() = false after SetGLM53(true)")
	}
	if err := lib.DumpChatTokenization("model.gguf", "sys", "hi", ThinkHigh, 4096, 0); err != nil {
		t.Errorf("DumpChatTokenization: %v", err)
	}
}

// Without the symbols, the wrappers degrade the way the doc comments say.
func TestSmallBindingsDegradeWithoutSymbols(t *testing.T) {
	lib, _, eng, sess := smallBindingsSession(t)
	lib.raw.ds4EngineIsGLM53 = nil
	lib.raw.ds4SessionArgmaxIgnoringEOS = nil
	lib.raw.ds4SessionEvalSpeculativeArgmaxIgnoringEOS = nil
	lib.raw.ds4SessionSetLogits = nil
	lib.raw.ds4SampleLogits = nil
	lib.raw.ds4SessionGPUWarmup = nil
	lib.raw.ds4SessionReportProgress = nil
	lib.raw.ds4DumpChatTokenization = nil
	if eng.IsGLM53() {
		t.Error("IsGLM53 without the symbol = true")
	}
	if got := sess.ArgmaxIgnoringEOS(ThinkNone); got != -1 {
		t.Errorf("ArgmaxIgnoringEOS without the symbol = %d, want -1", got)
	}
	if _, err := sess.EvalSpeculativeArgmaxIgnoringEOS(1, 2, 0, ThinkNone); err == nil || !strings.Contains(err.Error(), "ds4_session_eval_speculative_argmax_ignoring_eos") {
		t.Errorf("EvalSpeculativeArgmaxIgnoringEOS without the symbol: %v", err)
	}
	if err := sess.SetLogits(make([]float32, eng.VocabSize())); err == nil {
		t.Error("SetLogits without the symbol succeeded")
	}
	if got := lib.SampleLogits([]float32{1, 2}, 0, 0, 1, 0, nil); got != -1 {
		t.Errorf("SampleLogits without the symbol = %d, want -1", got)
	}
	sess.GPUWarmup()
	sess.ReportProgress("x", 0, 1)
	if err := lib.DumpChatTokenization("m", "", "p", ThinkNone, 1, 0); err == nil {
		t.Error("DumpChatTokenization without the symbol succeeded")
	}
}
