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
package cliopts

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/spf13/pflag"
)

// CLIConfig holds the `ds4` CLI option surface (ds4_cli.c).
type CLIConfig struct {
	// Lib is the libds4 shared library path. This flag has no ds4 equivalent;
	// it is required by the pure-Go wrapper. Empty uses DS4_LIB or DS4_DIR/lib.
	Lib string

	// Model and runtime.
	Model                      string
	MTP                        string
	MTPDraft                   int
	MTPMargin                  float32
	Ctx                        int
	Metal                      bool
	CUDA                       bool
	ROCm                       bool
	CPU                        bool
	Backend                    string
	Threads                    int
	Quality                    bool
	DirSteeringFile            string
	DirSteeringFFN             float32
	DirSteeringAttn            float32
	WarmWeights                bool
	SSDStreaming               bool
	SSDStreamingCold           bool
	SSDStreamingCacheExperts   string
	SSDStreamingPreloadExperts uint32
	SimulateUsedMemory         string
	PrefillChunk               uint32
	ExpertProfile              string

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

	fs.StringVar(&c.Lib, "lib", "", "libds4 shared library path (ds4go addition; empty uses DS4_LIB or DS4_DIR/lib)")

	// Model and runtime.
	fs.StringVarP(&c.Model, "model", "m", models.DefaultModelPath(), "GGUF model path")
	fs.StringVar(&c.MTP, "mtp", models.DefaultMTPPath(), "optional MTP support GGUF used for draft-token probes")
	fs.IntVar(&c.MTPDraft, "mtp-draft", 1, "maximum autoregressive MTP draft tokens per speculative step")
	fs.Float32Var(&c.MTPMargin, "mtp-margin", 3, "minimum recursive-draft confidence for the fast N=2 verifier")
	fs.IntVarP(&c.Ctx, "ctx", "c", 32768, "context size allocated for the session")
	fs.BoolVar(&c.Metal, "metal", false, "use the Metal graph backend")
	fs.BoolVar(&c.CUDA, "cuda", false, "use the CUDA graph backend")
	fs.BoolVar(&c.ROCm, "rocm", false, "use the ROCm graph backend")
	fs.BoolVar(&c.CPU, "cpu", false, "use the CPU reference/debug backend")
	fs.StringVar(&c.Backend, "backend", "", "select backend explicitly: metal, cuda, rocm, or cpu")
	fs.IntVarP(&c.Threads, "threads", "t", 0, "CPU helper threads for host-side or reference work")
	fs.BoolVar(&c.Quality, "quality", false, "prefer exact kernels where faster approximate paths exist")
	fs.StringVar(&c.DirSteeringFile, "dir-steering-file", "", "load one f32 direction vector per layer for directional steering")
	fs.Float32Var(&c.DirSteeringFFN, "dir-steering-ffn", 0, "apply steering after FFN outputs (default 1 with file)")
	fs.Float32Var(&c.DirSteeringAttn, "dir-steering-attn", 0, "apply steering after attention outputs")
	fs.BoolVar(&c.WarmWeights, "warm-weights", false, "touch mapped tensor pages before generation")
	fs.BoolVar(&c.SSDStreaming, "ssd-streaming", false, "enable SSD streaming of experts")
	fs.BoolVar(&c.SSDStreamingCold, "ssd-streaming-cold", false, "enable SSD streaming of experts with cold cache")
	fs.StringVar(&c.SSDStreamingCacheExperts, "ssd-streaming-cache-experts", "", "routed experts to keep in VRAM (count or <N>GB)")
	fs.Uint32Var(&c.SSDStreamingPreloadExperts, "ssd-streaming-preload-experts", 0, "experts to preload during startup")
	fs.StringVar(&c.SimulateUsedMemory, "simulate-used-memory", "", "simulate a specific amount of used GPU memory (e.g. 64GB)")
	fs.Uint32Var(&c.PrefillChunk, "prefill-chunk", 0, "prefill chunk size")
	fs.StringVar(&c.ExpertProfile, "expert-profile", "", "load one f32 expert profile from FILE")

	// Prompt and generation.
	fs.StringVarP(&c.Prompt, "prompt", "p", "", "prompt to generate from")
	fs.StringVar(&c.PromptFile, "prompt-file", "", "read the prompt text from FILE")
	fs.StringVar(&c.System, "system", "You are a helpful assistant", "system prompt; empty string disables the default")
	fs.IntVarP(&c.Tokens, "tokens", "n", 50000, "maximum tokens to generate")
	fs.Float32Var(&c.Temp, "temp", ds4.DefaultTemperature, "sampling temperature; 0 is greedy/deterministic")
	fs.Float32Var(&c.TopP, "top-p", ds4.DefaultTopP, "nucleus sampling probability")
	fs.Float32Var(&c.MinP, "min-p", ds4.DefaultMinP, "keep tokens scoring at least F times the top token")
	fs.Uint64Var(&c.Seed, "seed", 0, "sampling seed for reproducible non-greedy runs (0 = time-based)")
	fs.BoolVar(&c.Think, "think", false, "use normal thinking mode (the default)")
	fs.BoolVar(&c.ThinkMax, "think-max", false, "use Think Max when --ctx is large enough; otherwise normal thinking")
	fs.BoolVar(&c.NoThink, "nothink", false, "start assistant turns with </think> for direct non-thinking replies")

	// Diagnostics.
	fs.BoolVar(&c.Inspect, "inspect", false, "load the model and print a summary only")
	fs.BoolVar(&c.DumpTokens, "dump-tokens", false, "tokenize the prompt exactly as written, then exit")
	fs.StringVar(&c.DumpLogprobs, "dump-logprobs", "", "write greedy continuation top-logprobs as JSON to FILE")
	fs.IntVar(&c.LogprobsTopK, "logprobs-top-k", 20, "number of local alternatives stored by --dump-logprobs")
	fs.StringVar(&c.IMatrixDataset, "imatrix-dataset", "", "rendered DS4 prompt dataset for imatrix collection")
	fs.StringVar(&c.IMatrixOut, "imatrix-out", "", "collect a routed-MoE activation imatrix and write llama-compatible .dat")
	fs.IntVar(&c.IMatrixMaxPrompts, "imatrix-max-prompts", 0, "stop imatrix collection after N prompts (0 = no limit)")
	fs.IntVar(&c.IMatrixMaxTokens, "imatrix-max-tokens", 0, "stop imatrix collection after N prompt tokens (0 = no limit)")
	fs.BoolVar(&c.HeadTest, "head-test", false, "run the output HC/logits head after the native slice")
	fs.BoolVar(&c.FirstTokenTest, "first-token-test", false, "run an exact CPU whole-model pass for the first prompt token")
	fs.BoolVar(&c.MetalGraphTest, "metal-graph-test", false, "compare first GPU-resident graph stages with CPU")
	fs.BoolVar(&c.MetalGraphFullTest, "metal-graph-full-test", false, "run the GPU-resident self-token graph across all layers")
	fs.BoolVar(&c.MetalGraphPromptTest, "metal-graph-prompt-test", false, "compare CPU and GPU graph logits for the full prompt")

	return c
}

// SelectBackend resolves the backend from --metal/--cuda/--rocm/--cpu/--backend,
// falling back to metadata detection or host capabilities.
func (c *CLIConfig) SelectBackend() ds4.Backend {
	return selectBackend(c.Metal, c.CUDA, c.ROCm, c.CPU, c.Backend, c.Lib)
}

// ThinkMode resolves the thinking mode from --think/--think-max/--nothink.
func (c *CLIConfig) ThinkMode() ds4.ThinkMode {
	switch {
	case c.NoThink:
		return ds4.ThinkNone
	case c.ThinkMax:
		return ds4.ThinkMax
	default:
		return ds4.ThinkHigh
	}
}

// EngineOptions builds ds4.EngineOptions from the parsed flags.
func (c *CLIConfig) EngineOptions() ds4.EngineOptions {
	var ssdExperts uint32
	var ssdBytes uint64
	if c.SSDStreamingCacheExperts != "" {
		exp, b, err := parseStreamingCacheExpertsArg(c.SSDStreamingCacheExperts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ds4: --ssd-streaming-cache-experts must be a positive count or <number>GB\n")
			os.Exit(2)
		}
		ssdExperts = exp
		ssdBytes = b
	}

	var simUsedBytes uint64
	if c.SimulateUsedMemory != "" {
		b, err := parseGibArg(c.SimulateUsedMemory)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ds4: --simulate-used-memory must be a positive GiB value, e.g. 64GB\n")
			os.Exit(2)
		}
		simUsedBytes = b
	}

	return ds4.EngineOptions{
		ModelPath:                  c.Model,
		MTPPath:                    c.MTP,
		Backend:                    c.SelectBackend(),
		NThreads:                   c.Threads,
		PrefillChunk:               c.PrefillChunk,
		MTPDraftTokens:             c.MTPDraft,
		MTPMargin:                  c.MTPMargin,
		DirectionalSteeringFile:    c.DirSteeringFile,
		ExpertProfilePath:          c.ExpertProfile,
		DirectionalSteeringAttn:    c.DirSteeringAttn,
		DirectionalSteeringFFN:     c.DirSteeringFFN,
		SSDStreamingCacheExperts:   ssdExperts,
		SSDStreamingCacheBytes:     ssdBytes,
		SSDStreamingPreloadExperts: c.SSDStreamingPreloadExperts,
		SimulateUsedMemoryBytes:    simUsedBytes,
		WarmWeights:                c.WarmWeights,
		Quality:                    c.Quality,
		SSDStreaming:               c.SSDStreaming,
		SSDStreamingCold:           c.SSDStreamingCold,
		InspectOnly:                c.Inspect,
	}
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
	// Lib is the libds4 shared library path (ds4go addition).
	Lib string

	// Model and runtime.
	Model                      string
	MTP                        string
	MTPDraft                   int
	MTPMargin                  float32
	Ctx                        int
	Tokens                     int
	Threads                    int
	Quality                    bool
	DirSteeringFile            string
	DirSteeringFFN             float32
	DirSteeringAttn            float32
	WarmWeights                bool
	Metal                      bool
	CUDA                       bool
	ROCm                       bool
	CPU                        bool
	Backend                    string
	SSDStreaming               bool
	SSDStreamingCold           bool
	SSDStreamingCacheExperts   string
	SSDStreamingPreloadExperts uint32
	SimulateUsedMemory         string
	PrefillChunk               uint32

	// HTTP API.
	Host  string
	Port  int
	CORS  bool
	Trace string

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

	fs.StringVar(&c.Lib, "lib", "", "libds4 shared library path (ds4go addition; empty uses DS4_LIB or DS4_DIR/lib)")

	// Model and runtime.
	fs.StringVarP(&c.Model, "model", "m", models.DefaultModelPath(), "GGUF model path")
	fs.StringVar(&c.MTP, "mtp", models.DefaultMTPPath(), "optional MTP support GGUF used for draft-token probes")
	fs.IntVar(&c.MTPDraft, "mtp-draft", 1, "maximum autoregressive MTP draft tokens per speculative step")
	fs.Float32Var(&c.MTPMargin, "mtp-margin", 3, "minimum recursive-draft confidence for the fast N=2 verifier")
	fs.IntVarP(&c.Ctx, "ctx", "c", 32768, "context size allocated at startup")
	fs.IntVarP(&c.Tokens, "tokens", "n", 393216, "default max output tokens when the client omits a limit")
	fs.IntVarP(&c.Threads, "threads", "t", 0, "CPU helper threads for lightweight host-side work")
	fs.BoolVar(&c.Quality, "quality", false, "prefer exact kernels where faster approximate paths exist")
	fs.StringVar(&c.DirSteeringFile, "dir-steering-file", "", "load one f32 direction vector per layer for directional steering")
	fs.Float32Var(&c.DirSteeringFFN, "dir-steering-ffn", 0, "apply steering after FFN outputs (default 1 with file)")
	fs.Float32Var(&c.DirSteeringAttn, "dir-steering-attn", 0, "apply steering after attention outputs")
	fs.BoolVar(&c.WarmWeights, "warm-weights", false, "touch mapped tensor pages before serving")
	fs.BoolVar(&c.Metal, "metal", false, "use the Metal graph backend")
	fs.BoolVar(&c.CUDA, "cuda", false, "use the CUDA graph backend")
	fs.BoolVar(&c.ROCm, "rocm", false, "use the ROCm graph backend")
	fs.BoolVar(&c.CPU, "cpu", false, "use the CPU reference/debug backend")
	fs.StringVar(&c.Backend, "backend", "", "select backend explicitly: metal, cuda, rocm, or cpu")
	fs.BoolVar(&c.SSDStreaming, "ssd-streaming", false, "enable SSD streaming of experts")
	fs.BoolVar(&c.SSDStreamingCold, "ssd-streaming-cold", false, "enable SSD streaming of experts with cold cache")
	fs.StringVar(&c.SSDStreamingCacheExperts, "ssd-streaming-cache-experts", "", "routed experts to keep in VRAM (count or <N>GB)")
	fs.Uint32Var(&c.SSDStreamingPreloadExperts, "ssd-streaming-preload-experts", 0, "experts to preload during startup")
	fs.StringVar(&c.SimulateUsedMemory, "simulate-used-memory", "", "simulate a specific amount of used GPU memory (e.g. 64GB)")
	fs.Uint32Var(&c.PrefillChunk, "prefill-chunk", 0, "prefill chunk size")

	// HTTP API.
	fs.StringVar(&c.Host, "host", "127.0.0.1", "bind address")
	fs.IntVar(&c.Port, "port", 8000, "bind port")
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

// SelectBackend resolves the backend from --metal/--cuda/--rocm/--cpu/--backend,
// falling back to metadata detection or host capabilities.
func (c *ServerConfig) SelectBackend() ds4.Backend {
	return selectBackend(c.Metal, c.CUDA, c.ROCm, c.CPU, c.Backend, c.Lib)
}

// EngineOptions builds ds4.EngineOptions from the parsed flags.
func (c *ServerConfig) EngineOptions() ds4.EngineOptions {
	var ssdExperts uint32
	var ssdBytes uint64
	if c.SSDStreamingCacheExperts != "" {
		exp, b, err := parseStreamingCacheExpertsArg(c.SSDStreamingCacheExperts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ds4-server: --ssd-streaming-cache-experts must be a positive count or <number>GB\n")
			os.Exit(2)
		}
		ssdExperts = exp
		ssdBytes = b
	}

	var simUsedBytes uint64
	if c.SimulateUsedMemory != "" {
		b, err := parseGibArg(c.SimulateUsedMemory)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ds4-server: --simulate-used-memory must be a positive GiB value, e.g. 64GB\n")
			os.Exit(2)
		}
		simUsedBytes = b
	}

	return ds4.EngineOptions{
		ModelPath:                  c.Model,
		MTPPath:                    c.MTP,
		Backend:                    c.SelectBackend(),
		NThreads:                   c.Threads,
		PrefillChunk:               c.PrefillChunk,
		MTPDraftTokens:             c.MTPDraft,
		MTPMargin:                  c.MTPMargin,
		DirectionalSteeringFile:    c.DirSteeringFile,
		DirectionalSteeringAttn:    c.DirSteeringAttn,
		DirectionalSteeringFFN:     c.DirSteeringFFN,
		SSDStreamingCacheExperts:   ssdExperts,
		SSDStreamingCacheBytes:     ssdBytes,
		SSDStreamingPreloadExperts: c.SSDStreamingPreloadExperts,
		SimulateUsedMemoryBytes:    simUsedBytes,
		WarmWeights:                c.WarmWeights,
		Quality:                    c.Quality,
		SSDStreaming:               c.SSDStreaming,
		SSDStreamingCold:           c.SSDStreamingCold,
	}
}

func parseGibArg(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty argument")
	}
	if len(s) > 2 && strings.HasSuffix(strings.ToLower(s), "gb") {
		s = s[:len(s)-2]
	}
	if s == "" {
		return 0, fmt.Errorf("invalid GB argument")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("invalid characters: %q", s)
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	const gib = 1024 * 1024 * 1024
	if v > math.MaxUint64/gib {
		return 0, fmt.Errorf("value too large")
	}
	return v * gib, nil
}

func parseStreamingCacheExpertsArg(s string) (uint32, uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty argument")
	}
	if len(s) > 2 && strings.HasSuffix(strings.ToLower(s), "gb") {
		bytes, err := parseGibArg(s)
		if err != nil {
			return 0, 0, err
		}
		return 0, bytes, nil
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, 0, fmt.Errorf("invalid characters: %q", s)
		}
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, 0, err
	}
	return uint32(v), 0, nil
}

// Addr returns the host:port listen address.
func (c *ServerConfig) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

func selectBackend(metal, cuda, rocm, cpu bool, name string, libPath string) ds4.Backend {
	switch {
	case cpu:
		return ds4.BackendCPU
	case cuda, rocm:
		return ds4.BackendCUDA
	case metal:
		return ds4.BackendMetal
	}
	switch strings.ToLower(name) {
	case "cpu":
		return ds4.BackendCPU
	case "cuda", "rocm":
		return ds4.BackendCUDA
	case "metal":
		return ds4.BackendMetal
	}
	return ds4.DetectDefaultBackend(libPath)
}

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
