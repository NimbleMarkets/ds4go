package cliopts

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/spf13/pflag"
)

// EngineFlags holds the engine and runtime option surface shared by the
// `ds4` CLI and `ds4-server` programs (ds4_cli.c, ds4_server.c). CLIConfig
// and ServerConfig embed it; flags that exist on only one program stay on
// those structs.
type EngineFlags struct {
	// Lib is the libds4 shared library path. This flag has no ds4 equivalent;
	// it is required by the pure-Go wrapper. Empty uses DS4_LIB or DS4_DIR/lib.
	Lib string

	Model                      string
	MTP                        string
	MTPDraft                   int
	MTPMargin                  float32
	Dspark                     bool
	DsparkConfidence           float32
	DsparkStrict               bool
	MTPExactSampling           bool
	Vision                     string
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
	SSDStreamingFullLayers     int // -1 = unset (auto); >= 0 mirrors --ssd-streaming-full-layers N
	SimulateUsedMemory         string
	PrefillChunk               uint32
	Power                      int
	MTPTiming                  bool
	CUDATensorParallel         bool
}

// engineFlagHelp carries the help strings that differ between ds4_cli.c and
// ds4_server.c for otherwise identical flags; each program keeps its
// upstream wording exactly.
type engineFlagHelp struct {
	ctx         string
	threads     string
	warmWeights string
}

// registerEngineFlags registers the shared engine flag surface on fs.
func registerEngineFlags(fs *pflag.FlagSet, c *EngineFlags, help engineFlagHelp) {
	fs.StringVar(&c.Lib, "lib", "", "libds4 shared library path (ds4go addition; empty uses DS4_LIB or DS4_DIR/lib)")
	fs.StringVarP(&c.Model, "model", "m", models.DefaultModelPath(), "GGUF model path or installed catalog alias (see: ds4go model list)")
	fs.StringVar(&c.MTP, "mtp", models.DefaultMTPPath(), "optional MTP support GGUF used for draft-token probes")
	fs.StringVar(&c.MTP, "mtp-model", models.DefaultMTPPath(), "external MTP or DSpark support GGUF (same as --mtp)")
	fs.IntVar(&c.MTPDraft, "mtp-draft", 1, "maximum autoregressive MTP draft tokens per speculative step")
	fs.Float32Var(&c.MTPMargin, "mtp-margin", 3, "minimum recursive-draft confidence for the fast N=2 verifier")
	fs.BoolVar(&c.Dspark, "dspark", false, "enable experimental DSpark runtime speculative decoding (needs the DSpark support GGUF via --mtp)")
	fs.Float32Var(&c.DsparkConfidence, "dspark-confidence", 0, "DSpark draft confidence threshold in (0,1]; implies --dspark")
	fs.BoolVar(&c.DsparkStrict, "dspark-strict", false, "DSpark strict verification (target-only acceptance); implies --dspark")
	fs.BoolVar(&c.MTPExactSampling, "mtp-exact-sampling", false, "exact p/q acceptance for DSpark and GLM MTP drafts instead of opportunistic sampling")
	fs.StringVar(&c.Vision, "vision", "", "vision encoder GGUF for the selected model (defaults to the catalog encoder when installed)")
	fs.IntVarP(&c.Ctx, "ctx", "c", 32768, help.ctx)
	fs.BoolVar(&c.Metal, "metal", false, "use the Metal graph backend")
	fs.BoolVar(&c.CUDA, "cuda", false, "use the CUDA graph backend")
	fs.BoolVar(&c.ROCm, "rocm", false, "use the ROCm graph backend")
	fs.BoolVar(&c.CPU, "cpu", false, "use the CPU reference/debug backend")
	fs.StringVar(&c.Backend, "backend", "", "select backend explicitly: metal, cuda, rocm, or cpu")
	fs.IntVarP(&c.Threads, "threads", "t", 0, help.threads)
	fs.BoolVar(&c.Quality, "quality", false, "prefer exact kernels where faster approximate paths exist")
	fs.StringVar(&c.DirSteeringFile, "dir-steering-file", "", "load one f32 direction vector per layer for directional steering")
	fs.Float32Var(&c.DirSteeringFFN, "dir-steering-ffn", 0, "apply steering after FFN outputs (default 1 with file)")
	fs.Float32Var(&c.DirSteeringAttn, "dir-steering-attn", 0, "apply steering after attention outputs")
	fs.BoolVar(&c.WarmWeights, "warm-weights", false, help.warmWeights)
	fs.BoolVar(&c.SSDStreaming, "ssd-streaming", false, "enable SSD streaming of experts")
	fs.BoolVar(&c.SSDStreamingCold, "ssd-streaming-cold", false, "enable SSD streaming of experts with cold cache")
	fs.StringVar(&c.SSDStreamingCacheExperts, "ssd-streaming-cache-experts", "", "routed experts to keep in VRAM (count or <N>GB)")
	fs.Uint32Var(&c.SSDStreamingPreloadExperts, "ssd-streaming-preload-experts", 0, "experts to preload during startup")
	fs.IntVar(&c.SSDStreamingFullLayers, "ssd-streaming-full-layers", -1, "GLM Metal streaming: keep the first N routed layers fully resident (default: auto from the expert budget; 0 disables)")
	fs.IntVar(&c.Power, "power", 0, "GPU duty-cycle target, 1..100 (default 100)")
	fs.BoolVar(&c.MTPTiming, "mtp-timing", false, "enable embedded MTP and print acceptance/timing counters")
	fs.BoolVar(&c.CUDATensorParallel, "cuda-tensor-parallel", false, "enable the paired DeepSeek tensor/expert path on an even multi-GPU CUDA placement")
	fs.StringVar(&c.SimulateUsedMemory, "simulate-used-memory", "", "simulate a specific amount of used GPU memory (e.g. 64GB)")
	fs.Uint32Var(&c.PrefillChunk, "prefill-chunk", 0, "prefill chunk size")
}

// EngineOptions builds the shared ds4.EngineOptions from the parsed flags.
// Invalid flag values return an error instead of terminating the process;
// each command decides how to report it and exit. CLIConfig and ServerConfig
// wrap this with their command-specific fields.
func (c *EngineFlags) EngineOptions() (ds4.EngineOptions, error) {
	var ssdExperts uint32
	var ssdBytes uint64
	if c.SSDStreamingCacheExperts != "" {
		exp, b, err := parseStreamingCacheExpertsArg(c.SSDStreamingCacheExperts)
		if err != nil {
			return ds4.EngineOptions{}, fmt.Errorf("--ssd-streaming-cache-experts must be a positive count or <number>GB")
		}
		ssdExperts, ssdBytes = exp, b
	}

	var simUsedBytes uint64
	if c.SimulateUsedMemory != "" {
		b, err := parseGibArg(c.SimulateUsedMemory)
		if err != nil {
			return ds4.EngineOptions{}, fmt.Errorf("--simulate-used-memory must be a positive GiB value, e.g. 64GB")
		}
		simUsedBytes = b
	}

	if err := checkPower(c.Power); err != nil {
		return ds4.EngineOptions{}, err
	}
	if err := checkDsparkConfidence(c.DsparkConfidence); err != nil {
		return ds4.EngineOptions{}, err
	}

	model, ok := models.ModelForPath(c.Model)
	mtpPath := c.MTP
	switch {
	case ok && (model.GLM || model.DeepSeek41 || model.Qwen):
		// libds4 rejects an external --mtp support model for GLM and Qwen3.8:
		// their next-token predictors are embedded in the base GGUF. DSpark
		// and external MTP are not implemented for V4.1 either.
		mtpPath = ""
	case ok && model.DSpark != "":
		// This checkpoint pins its own DSpark drafter; libds4 rejects every
		// other one, including the installed 0731 model --mtp defaults to.
		// An explicit --mtp is overridden for the same reason: any other
		// drafter fails the engine open. Absent, the path stays empty.
		mtpPath, _ = ds4.DSparkSupportPath(c.Model)
	}

	opts := ds4.EngineOptions{
		ModelPath:                  c.Model,
		MTPPath:                    mtpPath,
		VisionPath:                 c.Vision,
		Backend:                    c.SelectBackend(),
		NThreads:                   c.Threads,
		ContextSize:                c.Ctx,
		PlacementCtxHint:           c.Ctx,
		PrefillChunk:               c.PrefillChunk,
		MTPDraftTokens:             c.MTPDraft,
		MTPMargin:                  c.MTPMargin,
		Dspark:                     c.dsparkEnabled(),
		DsparkStrict:               c.DsparkStrict,
		DsparkExactSampling:        c.MTPExactSampling,
		DsparkConfidenceThreshold:  c.DsparkConfidence,
		DirectionalSteeringFile:    c.DirSteeringFile,
		DirectionalSteeringAttn:    c.DirSteeringAttn,
		DirectionalSteeringFFN:     c.DirSteeringFFN,
		SSDStreamingCacheExperts:   ssdExperts,
		SSDStreamingCacheBytes:     ssdBytes,
		SSDStreamingPreloadExperts: c.SSDStreamingPreloadExperts,
		SSDStreamingFullLayers:     uint32(max(c.SSDStreamingFullLayers, 0)),
		SSDStreamingFullLayersSet:  c.SSDStreamingFullLayers >= 0,
		CUDATensorParallel:         c.CUDATensorParallel,
		PowerPercent:               c.Power,
		GLMMTP:                     c.MTPTiming,
		GLMMTPTiming:               c.MTPTiming,
		SimulateUsedMemoryBytes:    simUsedBytes,
		WarmWeights:                c.WarmWeights,
		Quality:                    c.Quality,
		SSDStreaming:               c.SSDStreaming,
		SSDStreamingCold:           c.SSDStreamingCold,
	}
	ds4.ApplyVisionDefaults(&opts)
	return opts, nil
}

// SelectBackend resolves the backend from --metal/--cuda/--rocm/--cpu/--backend,
// falling back to metadata detection or host capabilities.
func (c *EngineFlags) SelectBackend() ds4.Backend {
	switch {
	case c.CPU:
		return ds4.BackendCPU
	case c.CUDA, c.ROCm:
		return ds4.BackendCUDA
	case c.Metal:
		return ds4.BackendMetal
	}
	switch strings.ToLower(c.Backend) {
	case "cpu":
		return ds4.BackendCPU
	case "cuda", "rocm":
		return ds4.BackendCUDA
	case "metal":
		return ds4.BackendMetal
	}
	return ds4.DetectDefaultBackend(c.Lib)
}

// dsparkEnabled mirrors upstream ds4_cli.c: --dspark-confidence and
// --dspark-strict each imply --dspark.
func (c *EngineFlags) dsparkEnabled() bool {
	return c.Dspark || c.DsparkStrict || c.DsparkConfidence != 0
}

// checkPower enforces upstream's 1..100 range for --power; 0 means unset.
func checkPower(v int) error {
	if v != 0 && (v < 1 || v > 100) {
		return fmt.Errorf("--power must be between 1 and 100")
	}
	return nil
}

// checkDsparkConfidence enforces upstream's parse_float_range(0, 1) for
// --dspark-confidence. Zero means unset: libds4 only reads the threshold
// when it is non-zero (see ds4api's DsparkConfidenceThresholdSet).
func checkDsparkConfidence(v float32) error {
	if v < 0 || v > 1 {
		return fmt.Errorf("--dspark-confidence must be within [0, 1]")
	}
	return nil
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
