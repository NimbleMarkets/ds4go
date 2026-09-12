package cliopts

import (
	"testing"

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
