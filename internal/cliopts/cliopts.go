// Package cliopts defines the command-line flag surface shared by the ds4go
// CLI and examples.
//
// The flag names, shorthands, and defaults mirror the upstream ds4 C programs
// so that ds4go binaries accept the same arguments:
//
//   - RegisterCLI    mirrors the `ds4` CLI       (ds4_cli.c)
//   - RegisterServer mirrors the `ds4-server`    (ds4_server.c)
//
// Every flag is parsed even when a given program does not exercise the
// corresponding feature, so the argument surface stays identical across
// programs. The one addition with no C equivalent is --lib, which points at
// the libds4 shared library the pure-Go wrapper loads at runtime.
//
// The engine and runtime flags common to both programs live on EngineFlags,
// embedded in CLIConfig and ServerConfig. Invalid flag values surface as
// errors from EngineOptions; only Parse itself exits the process.
package cliopts

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/NimbleMarkets/ds4go"
	"github.com/spf13/pflag"
)

// CLIConfig holds the `ds4` CLI option surface (ds4_cli.c).
type CLIConfig struct {
	// EngineFlags is the engine and runtime option surface shared with
	// ds4-server.
	EngineFlags

	// Model and runtime (CLI-only).
	Images                  []string
	ExpertProfile           string
	RawPrompt               bool
	PrefixFile              string
	DumpLogits              string
	DecodeConsistency       int
	PerplexityFile          string
	IMatrixMinExpertSamples int

	// Prompt and generation.
	Prompt     string
	PromptFile string
	System     string
	Tokens     int
	Temp       float32
	TopP       float32
	MinP       float32
	Seed       uint64
	Think      bool
	ThinkMax   bool
	ThinkLevel ThinkLevelFlag // --think-level (V4.1 reasoning effort); unset unless given
	NoThink    bool

	// Diagnostics.
	Inspect              bool
	DumpTokens           bool
	DumpLogprobs         string
	LogprobsTopK         int
	IMatrixDataset       string
	IMatrixOut           string
	IMatrixMaxPrompts    int
	IMatrixMaxTokens     int
	HeadTest             bool
	FirstTokenTest       bool
	MetalGraphTest       bool
	MetalGraphFullTest   bool
	MetalGraphPromptTest bool
}

// RegisterCLI registers the full `ds4` CLI flag set on fs and returns the
// config that fs.Parse will populate.
func RegisterCLI(fs *pflag.FlagSet) *CLIConfig {
	c := &CLIConfig{}

	// Model and runtime.
	registerEngineFlags(fs, &c.EngineFlags, engineFlagHelp{
		ctx:         "context size allocated for the session",
		threads:     "CPU helper threads for host-side or reference work",
		warmWeights: "touch mapped tensor pages before generation",
	})
	fs.StringArrayVar(&c.Images, "image", nil, "PNG or JPEG to attach to the prompt; repeatable, attached in order after the text")
	fs.StringVar(&c.ExpertProfile, "expert-profile", "", "load one f32 expert profile from FILE")

	// Prompt and generation.
	fs.StringVarP(&c.Prompt, "prompt", "p", "", "prompt to generate from")
	fs.StringVar(&c.PromptFile, "prompt-file", "", "read the prompt text from FILE")
	fs.BoolVar(&c.RawPrompt, "raw", false, "tokenize the one-shot prompt without chat markers")
	fs.BoolVar(&c.RawPrompt, "raw-prompt", false, "same as --raw")
	fs.StringVar(&c.PrefixFile, "prefix-file", "", "preload complete alternating USER:/ASSISTANT: turns from FILE before the live conversation")
	fs.StringVar(&c.System, "system", "You are a helpful assistant", "system prompt; empty string disables the default")
	fs.IntVarP(&c.Tokens, "tokens", "n", 50000, "maximum tokens to generate")
	fs.Float32Var(&c.Temp, "temp", ds4.DefaultTemperature, "sampling temperature; 0 is greedy/deterministic")
	fs.Float32Var(&c.TopP, "top-p", ds4.DefaultTopP, "nucleus sampling probability")
	fs.Float32Var(&c.MinP, "min-p", ds4.DefaultMinP, "keep tokens scoring at least F times the top token")
	fs.Uint64Var(&c.Seed, "seed", 0, "sampling seed for reproducible non-greedy runs (0 = time-based)")
	fs.BoolVar(&c.Think, "think", false, "use normal thinking mode (the default)")
	fs.BoolVar(&c.ThinkMax, "think-max", false, "use Think Max when --ctx is large enough; otherwise normal thinking")
	fs.BoolVar(&c.NoThink, "nothink", false, "start assistant turns with </think> for direct non-thinking replies")
	fs.Var(&c.ThinkLevel, "think-level", "V4.1 thinking effort, 1..100; 0 disables thinking")

	// Diagnostics.
	fs.BoolVar(&c.Inspect, "inspect", false, "load the model and print a summary only")
	fs.BoolVar(&c.DumpTokens, "dump-tokens", false, "tokenize the prompt exactly as written, then exit")
	fs.StringVar(&c.DumpLogprobs, "dump-logprobs", "", "write greedy continuation top-logprobs as JSON to FILE")
	fs.IntVar(&c.LogprobsTopK, "logprobs-top-k", 20, "number of local alternatives stored by --dump-logprobs")
	fs.StringVar(&c.IMatrixDataset, "imatrix-dataset", "", "rendered DS4 prompt dataset for imatrix collection")
	fs.StringVar(&c.IMatrixOut, "imatrix-out", "", "collect a routed-MoE activation imatrix and write llama-compatible .dat")
	fs.IntVar(&c.IMatrixMaxPrompts, "imatrix-max-prompts", 0, "stop imatrix collection after N prompts (0 = no limit)")
	fs.IntVar(&c.IMatrixMaxTokens, "imatrix-max-tokens", 0, "stop imatrix collection after N prompt tokens (0 = no limit)")
	fs.IntVar(&c.IMatrixMinExpertSamples, "imatrix-min-expert-samples", 0, "continue imatrix collection until every routed expert has N samples")
	fs.StringVar(&c.DumpLogits, "dump-logits", "", "write full next-token logits for the prompt as JSON to FILE")
	fs.IntVar(&c.DecodeConsistency, "decode-consistency", 0, "compare N-token decode logits with a fresh full prefill")
	fs.StringVar(&c.PerplexityFile, "perplexity-file", "", "score the raw text in FILE with teacher-forced NLL")
	fs.BoolVar(&c.HeadTest, "head-test", false, "run the output HC/logits head after the native slice")
	fs.BoolVar(&c.FirstTokenTest, "first-token-test", false, "run an exact CPU whole-model pass for the first prompt token")
	fs.BoolVar(&c.MetalGraphTest, "metal-graph-test", false, "compare first GPU-resident graph stages with CPU")
	fs.BoolVar(&c.MetalGraphFullTest, "metal-graph-full-test", false, "run the GPU-resident self-token graph across all layers")
	fs.BoolVar(&c.MetalGraphPromptTest, "metal-graph-prompt-test", false, "compare CPU and GPU graph logits for the full prompt")

	return c
}

// ThinkMode resolves the thinking mode from --think/--think-max/--nothink.
func (c *CLIConfig) ThinkMode() ds4.ThinkMode {
	switch {
	case c.ThinkLevel.Given:
		return ds4.ThinkLevel(c.ThinkLevel.Level)
	case c.NoThink:
		return ds4.ThinkNone
	case c.ThinkMax:
		return ds4.ThinkMax
	default:
		return ds4.ThinkHigh
	}
}

// EngineOptions builds ds4.EngineOptions from the parsed flags. Invalid flag
// values return an error; the command boundary decides how to report it.
func (c *CLIConfig) EngineOptions() (ds4.EngineOptions, error) {
	opts, err := c.EngineFlags.EngineOptions()
	if err != nil {
		return ds4.EngineOptions{}, err
	}
	opts.ExpertProfilePath = c.ExpertProfile
	opts.InspectOnly = c.Inspect
	return opts, nil
}

// ThinkLevelFlag is the --think-level value: a V4.1 reasoning effort from 0
// to 100, recorded as set only when the flag was given so a zero-value config
// keeps the named default.
type ThinkLevelFlag struct {
	Level int
	Given bool
}

// String implements pflag.Value.
func (f *ThinkLevelFlag) String() string {
	if !f.Given {
		return ""
	}
	return fmt.Sprint(f.Level)
}

// Set implements pflag.Value.
func (f *ThinkLevelFlag) Set(text string) error {
	mode, err := ds4.ParseThinkLevel(text)
	if err != nil {
		return errors.New("--think-level requires an integer from 0 to 100")
	}
	f.Level, f.Given = mode.Level(), true
	return nil
}

// Type implements pflag.Value.
func (f *ThinkLevelFlag) Type() string { return "int" }

// ImageParts returns the prompt text followed by the --image files as content
// parts, or nil when no images were given so callers keep the text-only path.
func (c *CLIConfig) ImageParts(prompt string) []ds4.ContentPart {
	if len(c.Images) == 0 {
		return nil
	}
	parts := []ds4.ContentPart{{Text: prompt}}
	for _, path := range c.Images {
		parts = append(parts, ds4.ContentPart{Image: &ds4.ImageInput{Path: path}})
	}
	return parts
}

// GenerateOptions builds ds4.GenerateOptions from the parsed sampling flags.
func (c *CLIConfig) GenerateOptions() ds4.GenerateOptions {
	return ds4.GenerateOptions{
		MaxTokens:   c.Tokens,
		Temperature: c.Temp,
		TopP:        c.TopP,
		MinP:        c.MinP,
		Seed:        c.ResolvedSeed(),
		StopOnEOS:   true,
		// Stop detection must match the think mode the prompt is rendered with;
		// callers that vary it per turn (the chat REPL) override this field.
		ThinkMode: c.ThinkMode(),
	}
}

// ResolvedSeed returns the sampling seed, generating a time-based one when the
// --seed flag is left at its zero default.
func (c *CLIConfig) ResolvedSeed() uint64 {
	if c.Seed != 0 {
		return c.Seed
	}
	return uint64(time.Now().UnixNano())
}

// PromptText returns the prompt text, reading --prompt-file when it is set.
func (c *CLIConfig) PromptText() (string, error) {
	if c.PromptFile != "" {
		b, err := os.ReadFile(c.PromptFile)
		if err != nil {
			return "", fmt.Errorf("read --prompt-file: %w", err)
		}
		return string(b), nil
	}
	return c.Prompt, nil
}

// ServerConfig holds the `ds4-server` option surface (ds4_server.c).
type ServerConfig struct {
	// EngineFlags is the engine and runtime option surface shared with the
	// `ds4` CLI.
	EngineFlags

	// Tokens is the default max output tokens when the client omits a limit.
	Tokens int

	// HTTP API.
	Host  string
	Port  int
	CORS  bool
	Trace string
	// Process and scheduling (ds4-server).
	Chdir          string
	BatchedSession int

	// Disk KV cache.
	KVDiskDir                      string
	KVDiskSpaceMB                  int
	KVCacheMinTokens               int
	KVCacheColdMaxTokens           int
	KVCacheContinuedIntervalTokens int
	KVCacheBoundaryTrimTokens      int
	KVCacheBoundaryAlignTokens     int
	KVCacheRejectDifferentQuant    bool
	DisableExactDSMLToolReplay     bool
	ToolMemoryMaxIDs               int
}

// RegisterServer registers the full `ds4-server` flag set on fs and returns the
// config that fs.Parse will populate.
func RegisterServer(fs *pflag.FlagSet) *ServerConfig {
	c := &ServerConfig{}

	// Model and runtime.
	registerEngineFlags(fs, &c.EngineFlags, engineFlagHelp{
		ctx:         "context size allocated at startup",
		threads:     "CPU helper threads for lightweight host-side work",
		warmWeights: "touch mapped tensor pages before serving",
	})
	fs.IntVarP(&c.Tokens, "tokens", "n", 393216, "default max output tokens when the client omits a limit")

	// HTTP API.
	fs.StringVar(&c.Host, "host", "127.0.0.1", "bind address")
	fs.IntVar(&c.Port, "port", 8000, "bind port")
	fs.StringVar(&c.Chdir, "chdir", "", "change working directory before loading runtime assets")
	fs.IntVar(&c.BatchedSession, "batched-session", 0, "keep N resident sessions and batch decode-ready requests")
	fs.BoolVar(&c.CORS, "cors", false, "add Access-Control-Allow-* headers for browser JS clients")
	fs.StringVar(&c.Trace, "trace", "", "write a human-readable session trace to FILE")

	// Disk KV cache.
	fs.StringVar(&c.KVDiskDir, "kv-disk-dir", "", "enable disk KV checkpoints in DIR (created if needed)")
	fs.IntVar(&c.KVDiskSpaceMB, "kv-disk-space-mb", 4096, "disk budget in MB for checkpoint files")
	fs.IntVar(&c.KVCacheMinTokens, "kv-cache-min-tokens", 512, "do not save or load checkpoints shorter than N tokens")
	fs.IntVar(&c.KVCacheColdMaxTokens, "kv-cache-cold-max-tokens", 30000, "cold first prompts in [min,N] are saved automatically (0 disables)")
	fs.IntVar(&c.KVCacheContinuedIntervalTokens, "kv-cache-continued-interval-tokens", 10000, "save at absolute aligned frontiers spaced about N tokens apart (0 disables)")
	fs.IntVar(&c.KVCacheBoundaryTrimTokens, "kv-cache-boundary-trim-tokens", 32, "trim this many tail tokens before cold boundary saves")
	fs.IntVar(&c.KVCacheBoundaryAlignTokens, "kv-cache-boundary-align-tokens", 2048, "align cold boundary saves down to this token multiple (0 disables)")
	fs.BoolVar(&c.KVCacheRejectDifferentQuant, "kv-cache-reject-different-quant", false, "refuse checkpoints written with a different routed-expert quantization")
	fs.BoolVar(&c.DisableExactDSMLToolReplay, "disable-exact-dsml-tool-replay", false, "disable the tool-id to exact sampled DSML map")
	fs.IntVar(&c.ToolMemoryMaxIDs, "tool-memory-max-ids", 100000, "maximum exact tool-call IDs kept in RAM for replay")

	return c
}

// EngineOptions builds ds4.EngineOptions from the parsed flags. Invalid flag
// values return an error; the command boundary decides how to report it.
func (c *ServerConfig) EngineOptions() (ds4.EngineOptions, error) {
	opts, err := c.EngineFlags.EngineOptions()
	if err != nil {
		return ds4.EngineOptions{}, err
	}
	// ds4-server plans placement for its resident session count and lets
	// batched sessions share one prefill workspace.
	opts.PlacementSessionCountHint = max(c.BatchedSession, 1)
	opts.ShareSessionPrefillWorkspace = c.BatchedSession > 0
	return opts, nil
}

// Addr returns the host:port listen address.
func (c *ServerConfig) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

// Parse parses args with fs, handling --help by printing usage and exiting 0.
// Any other parse error is printed and the process exits non-zero.
func Parse(fs *pflag.FlagSet, args []string) {
	if err := fs.Parse(args); err != nil {
		if err == pflag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
