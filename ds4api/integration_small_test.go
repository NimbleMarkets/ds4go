//go:build ds4_integration

package ds4api

import (
	"os"
	"testing"
)

// The small optional bindings against a real library: argmax-ignoring-EOS
// agrees with a manual scan of the copied logits, set_logits round-trips,
// and sample_logits at temperature 0 picks the max.
func TestRealLibrarySmallBindings(t *testing.T) {
	model := os.Getenv("DS4_MODEL")
	if model == "" {
		t.Skip("DS4_MODEL must be set")
	}
	lib := realLibrary(t)
	if lib.raw.ds4SessionSetLogits == nil {
		t.Skip("library predates ds4_session_set_logits")
	}
	eng, err := lib.NewEngine(EngineOptions{ModelPath: model, Backend: realBackend(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(4096)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	toks, err := eng.TokenizeText("The capital of France is")
	if err != nil {
		t.Fatal(err)
	}
	defer toks.Free()
	if err := sess.Sync(toks.Slice()); err != nil {
		t.Fatal(err)
	}
	logits, err := sess.CopyLogits()
	if err != nil {
		t.Fatal(err)
	}
	// Force EOS to the top, then the ignoring form must skip it.
	eos := eng.TokenEOS()
	plain := sess.Argmax()
	logits[eos] = 1e9
	if err := sess.SetLogits(logits); err != nil {
		t.Fatal(err)
	}
	if got := sess.Argmax(); got != eos {
		t.Fatalf("Argmax after SetLogits = %d, want EOS %d", got, eos)
	}
	if got := sess.ArgmaxIgnoringEOS(ThinkHigh); got != plain {
		t.Errorf("ArgmaxIgnoringEOS = %d, want the pre-EOS argmax %d", got, plain)
	}
	var rng uint64 = 7
	if got := lib.SampleLogits(logits, 0, 0, 1, 0, &rng); got != eos {
		t.Errorf("SampleLogits greedy = %d, want %d", got, eos)
	}
	t.Logf("glm53=%v qwen4=%v", eng.IsGLM53(), eng.IsQwen4())
	sess.GPUWarmup()
	sess.ReportProgress("test", 1, 1)
}
