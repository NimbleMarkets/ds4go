package cliopts

import (
	"reflect"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go"
	"github.com/spf13/pflag"
)

// mustEngineOptions resolves EngineOptions and fails the test on error, for
// tests exercising valid flag values.
func mustEngineOptions[T interface {
	EngineOptions() (ds4.EngineOptions, error)
}](t *testing.T, c T) ds4.EngineOptions {
	t.Helper()
	opts, err := c.EngineOptions()
	if err != nil {
		t.Fatalf("EngineOptions: %v", err)
	}
	return opts
}

// Invalid flag values must surface as errors for the command boundary to
// report, not terminate the process from inside configuration code.
func TestEngineOptionsValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  EngineFlags
		flag string
	}{
		{"bad ssd cache experts", EngineFlags{SSDStreamingCacheExperts: "bogus"}, "--ssd-streaming-cache-experts"},
		{"bad simulate used memory", EngineFlags{SimulateUsedMemory: "x"}, "--simulate-used-memory"},
		{"power over 100", EngineFlags{Power: 200}, "--power"},
		{"power under 1", EngineFlags{Power: -1}, "--power"},
		{"dspark confidence over 1", EngineFlags{DsparkConfidence: 1.5}, "--dspark-confidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli := CLIConfig{EngineFlags: tc.cfg}
			if _, err := cli.EngineOptions(); err == nil || !strings.Contains(err.Error(), tc.flag) {
				t.Errorf("CLI EngineOptions() error = %v, want error naming %s", err, tc.flag)
			}
			server := ServerConfig{EngineFlags: tc.cfg}
			if _, err := server.EngineOptions(); err == nil || !strings.Contains(err.Error(), tc.flag) {
				t.Errorf("server EngineOptions() error = %v, want error naming %s", err, tc.flag)
			}
		})
	}
}

// The same shared flags must resolve to the same engine options on both
// programs; only the documented command-specific fields may differ.
func TestCLIServerEngineOptionsParity(t *testing.T) {
	args := []string{
		"--model", "/m/model.gguf", "--mtp", "/m/draft.gguf", "--vision", "/m/enc.gguf",
		"--ctx", "1234", "--threads", "3", "--quality", "--backend", "cpu",
		"--mtp-draft", "5", "--mtp-margin", "2.5", "--dspark-confidence", "0.7",
		"--mtp-exact-sampling", "--dir-steering-file", "f", "--dir-steering-ffn", "0.5",
		"--dir-steering-attn", "0.25", "--warm-weights", "--ssd-streaming",
		"--ssd-streaming-cache-experts", "7", "--ssd-streaming-preload-experts", "9",
		"--ssd-streaming-full-layers", "2", "--simulate-used-memory", "4GB",
		"--prefill-chunk", "64", "--power", "42", "--mtp-timing", "--cuda-tensor-parallel",
	}
	fs := pflag.NewFlagSet("cli", pflag.ContinueOnError)
	cli := RegisterCLI(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("CLI Parse: %v", err)
	}
	sfs := pflag.NewFlagSet("server", pflag.ContinueOnError)
	server := RegisterServer(sfs)
	if err := sfs.Parse(args); err != nil {
		t.Fatalf("server Parse: %v", err)
	}

	cliOpts := mustEngineOptions(t, cli)
	serverOpts := mustEngineOptions(t, server)

	// Normalize the documented command-specific fields before comparing.
	cliOpts.ExpertProfilePath = ""
	cliOpts.InspectOnly = false
	serverOpts.PlacementSessionCountHint = 0
	serverOpts.ShareSessionPrefillWorkspace = false

	if !reflect.DeepEqual(cliOpts, serverOpts) {
		t.Errorf("shared engine options diverge:\nCLI:    %+v\nserver: %+v", cliOpts, serverOpts)
	}
}

// The flag surfaces mirror ds4_cli.c and ds4_server.c, whose help strings
// differ for a few otherwise identical flags.
func TestPerProgramFlagHelpVariants(t *testing.T) {
	fs := pflag.NewFlagSet("cli", pflag.ContinueOnError)
	RegisterCLI(fs)
	sfs := pflag.NewFlagSet("server", pflag.ContinueOnError)
	RegisterServer(sfs)

	cases := []struct {
		flag, cli, server string
	}{
		{"ctx", "context size allocated for the session", "context size allocated at startup"},
		{"threads", "CPU helper threads for host-side or reference work", "CPU helper threads for lightweight host-side work"},
		{"warm-weights", "touch mapped tensor pages before generation", "touch mapped tensor pages before serving"},
	}
	for _, tc := range cases {
		cliFlag, serverFlag := fs.Lookup(tc.flag), sfs.Lookup(tc.flag)
		if cliFlag == nil || serverFlag == nil {
			t.Errorf("--%s missing: cli=%v server=%v", tc.flag, cliFlag, serverFlag)
			continue
		}
		if cliFlag.Usage != tc.cli {
			t.Errorf("CLI --%s usage = %q, want %q", tc.flag, cliFlag.Usage, tc.cli)
		}
		if serverFlag.Usage != tc.server {
			t.Errorf("server --%s usage = %q, want %q", tc.flag, serverFlag.Usage, tc.server)
		}
	}
}
