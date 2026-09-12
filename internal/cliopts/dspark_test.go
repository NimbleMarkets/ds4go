package cliopts

import (
	"testing"

	"github.com/NimbleMarkets/ds4go"

	"github.com/spf13/pflag"
)

// The DSpark flags mirror upstream ds4_cli.c: --dspark-confidence and
// --dspark-strict imply --dspark, --mtp-exact-sampling is independent.
func TestDsparkFlagsMapToEngineOptions(t *testing.T) {
	cases := []struct {
		args                  []string
		dspark, strict, exact bool
		threshold             float32
	}{
		{args: []string{"--dspark"}, dspark: true},
		{args: []string{"--dspark-confidence", "0.7"}, dspark: true, threshold: 0.7},
		{args: []string{"--dspark-strict"}, dspark: true, strict: true},
		{args: []string{"--mtp-exact-sampling"}, exact: true},
		{args: nil},
	}
	for _, tc := range cases {
		fs := pflag.NewFlagSet("cli", pflag.ContinueOnError)
		cfg := RegisterCLI(fs)
		if err := fs.Parse(tc.args); err != nil {
			t.Fatalf("Parse(%v): %v", tc.args, err)
		}
		got := cfg.EngineOptions()
		if got.Dspark != tc.dspark || got.DsparkStrict != tc.strict || got.DsparkExactSampling != tc.exact || got.DsparkConfidenceThreshold != tc.threshold {
			t.Errorf("CLI %v: dspark=%v strict=%v exact=%v threshold=%v, want %v/%v/%v/%v",
				tc.args, got.Dspark, got.DsparkStrict, got.DsparkExactSampling, got.DsparkConfidenceThreshold,
				tc.dspark, tc.strict, tc.exact, tc.threshold)
		}

		sfs := pflag.NewFlagSet("server", pflag.ContinueOnError)
		scfg := RegisterServer(sfs)
		if err := sfs.Parse(tc.args); err != nil {
			t.Fatalf("server Parse(%v): %v", tc.args, err)
		}
		sgot := scfg.EngineOptions()
		if sgot.Dspark != tc.dspark || sgot.DsparkStrict != tc.strict || sgot.DsparkExactSampling != tc.exact || sgot.DsparkConfidenceThreshold != tc.threshold {
			t.Errorf("server %v: dspark=%v strict=%v exact=%v threshold=%v, want %v/%v/%v/%v",
				tc.args, sgot.Dspark, sgot.DsparkStrict, sgot.DsparkExactSampling, sgot.DsparkConfidenceThreshold,
				tc.dspark, tc.strict, tc.exact, tc.threshold)
		}
	}
}

// Hardware and placement flags mirror upstream ds4_cli.c / ds4_server.c on
// both flag sets.
func TestHardwareFlagsMapToEngineOptions(t *testing.T) {
	type want struct {
		power           int
		mtp, timing, tp bool
		fullLayers      uint32
		fullLayersSet   bool
	}
	cases := []struct {
		args []string
		want want
	}{
		{args: nil, want: want{}},
		{args: []string{"--power", "60"}, want: want{power: 60}},
		{args: []string{"--mtp-timing"}, want: want{mtp: true, timing: true}},
		{args: []string{"--cuda-tensor-parallel"}, want: want{tp: true}},
		{args: []string{"--ssd-streaming-full-layers", "3"}, want: want{fullLayers: 3, fullLayersSet: true}},
		// Zero is an explicit "disable", distinct from unset.
		{args: []string{"--ssd-streaming-full-layers", "0"}, want: want{fullLayers: 0, fullLayersSet: true}},
	}
	check := func(label string, args []string, got ds4.EngineOptions, w want) {
		t.Helper()
		if got.PowerPercent != w.power || got.GLMMTP != w.mtp || got.GLMMTPTiming != w.timing ||
			got.CUDATensorParallel != w.tp || got.SSDStreamingFullLayers != w.fullLayers || got.SSDStreamingFullLayersSet != w.fullLayersSet {
			t.Errorf("%s %v: power=%d mtp=%v timing=%v tp=%v full=%d set=%v, want %+v",
				label, args, got.PowerPercent, got.GLMMTP, got.GLMMTPTiming, got.CUDATensorParallel,
				got.SSDStreamingFullLayers, got.SSDStreamingFullLayersSet, w)
		}
	}
	for _, tc := range cases {
		fs := pflag.NewFlagSet("cli", pflag.ContinueOnError)
		cfg := RegisterCLI(fs)
		if err := fs.Parse(tc.args); err != nil {
			t.Fatalf("Parse(%v): %v", tc.args, err)
		}
		check("CLI", tc.args, cfg.EngineOptions(), tc.want)

		sfs := pflag.NewFlagSet("server", pflag.ContinueOnError)
		scfg := RegisterServer(sfs)
		if err := sfs.Parse(tc.args); err != nil {
			t.Fatalf("server Parse(%v): %v", tc.args, err)
		}
		check("server", tc.args, scfg.EngineOptions(), tc.want)
	}
}

// --batched-session N becomes the two placement hints upstream derives from
// it; without it the server plans for a single session, as ds4-server does.
func TestBatchedSessionMapsToPlacementHints(t *testing.T) {
	sfs := pflag.NewFlagSet("server", pflag.ContinueOnError)
	scfg := RegisterServer(sfs)
	if err := sfs.Parse([]string{"--batched-session", "4"}); err != nil {
		t.Fatal(err)
	}
	if got := scfg.EngineOptions(); got.PlacementSessionCountHint != 4 || !got.ShareSessionPrefillWorkspace {
		t.Errorf("batched: hint=%d share=%v, want 4/true", got.PlacementSessionCountHint, got.ShareSessionPrefillWorkspace)
	}
	plain := RegisterServer(pflag.NewFlagSet("server", pflag.ContinueOnError))
	if got := plain.EngineOptions(); got.PlacementSessionCountHint != 1 || got.ShareSessionPrefillWorkspace {
		t.Errorf("unbatched: hint=%d share=%v, want 1/false", got.PlacementSessionCountHint, got.ShareSessionPrefillWorkspace)
	}
}

func TestCLIOnlyPromptAndDiagnosticFlagsParse(t *testing.T) {
	fs := pflag.NewFlagSet("cli", pflag.ContinueOnError)
	cfg := RegisterCLI(fs)
	err := fs.Parse([]string{"--raw", "--prefix-file", "p.txt", "--dump-logits", "l.json",
		"--decode-consistency", "8", "--perplexity-file", "t.txt", "--imatrix-min-expert-samples", "5"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.RawPrompt || cfg.PrefixFile != "p.txt" || cfg.DumpLogits != "l.json" || cfg.DecodeConsistency != 8 ||
		cfg.PerplexityFile != "t.txt" || cfg.IMatrixMinExpertSamples != 5 {
		t.Errorf("parsed config = %+v", *cfg)
	}
	// --raw-prompt is upstream's long spelling of --raw.
	fs2 := pflag.NewFlagSet("cli", pflag.ContinueOnError)
	cfg2 := RegisterCLI(fs2)
	if err := fs2.Parse([]string{"--raw-prompt"}); err != nil || !cfg2.RawPrompt {
		t.Errorf("--raw-prompt: err=%v raw=%v", err, cfg2.RawPrompt)
	}
	// Server: --chdir is server-only, like ds4-server.
	sfs := pflag.NewFlagSet("server", pflag.ContinueOnError)
	scfg := RegisterServer(sfs)
	if err := sfs.Parse([]string{"--chdir", "/srv/ds4"}); err != nil || scfg.Chdir != "/srv/ds4" {
		t.Errorf("--chdir: err=%v chdir=%q", err, scfg.Chdir)
	}
}
