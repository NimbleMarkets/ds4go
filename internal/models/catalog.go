// Package models manages ds4go's curated model catalog.
package models

import (
	"os"
	"path/filepath"
	"strings"
)

// ModelPart is one published piece of a split GGUF (see Model.Parts).
type ModelPart struct {
	FileName string  `json:"fileName"`
	SizeGB   float64 `json:"sizeGB"`
	// Bytes is the part's exact published size; the joiner truncates an
	// interrupted join back to the first part's boundary with it.
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Model describes a curated ds4 GGUF model.
type Model struct {
	Alias          string  `json:"alias"`
	GGUFPath       string  `json:"ggufPath"`
	FileName       string  `json:"fileName"`
	SizeGB         float64 `json:"sizeGB"`
	RecommendedRAM string  `json:"recommendedRAM"`
	SHA256         string  `json:"sha256,omitempty"`
	// Repo is the Hugging Face repo hosting this model. Empty means the default
	// DeepSeek repo, so existing entries and saved state stay valid.
	Repo string `json:"repo,omitempty"`
	// GLM marks a GLM DSA model. Its tool-calling markup, stop tokens, and
	// reasoning-effort prompt differ from DeepSeek's; libds4 selects the
	// behaviour from the GGUF, and ds4go exposes it via Engine.IsGLMDSA.
	GLM bool `json:"glm,omitempty"`
	// Vision marks a checkpoint that accepts images once its encoder is
	// loaded. Encoder is the catalog alias of that encoder GGUF; ds4go pairs
	// it automatically when installed (ds4.ApplyVisionDefaults).
	Vision  bool   `json:"vision,omitempty"`
	Encoder string `json:"encoder,omitempty"`
	// DSpark is the catalog alias of the DSpark support model this checkpoint
	// requires, when it is not the default one (Vision-Exp has its own).
	DSpark string `json:"dspark,omitempty"`
	// DeepSeek41 marks a DeepSeek V4.1 Flash checkpoint: Metal-only upstream,
	// no DSpark or external MTP, thinking as an effort level (--think-level).
	DeepSeek41 bool `json:"deepseek41,omitempty"`
	// Parts lists the pieces a GGUF is published as when Hugging Face's single
	// file limit splits it. The downloader fetches each part, joins them into
	// FileName, and verifies the joined file against SHA256.
	Parts       []ModelPart `json:"parts,omitempty"`
	Imatrix     bool        `json:"imatrix"`
	Legacy      bool        `json:"legacy"`
	Optional    bool        `json:"optional"`
	Distributed bool        `json:"distributed"`
	// DistributedRole and LayerRange describe a distributed split half. LayerRange
	// is in upstream ds4 --layers form (e.g. "0:30", "31:output"); DistributedRole
	// is "coordinator" or "worker". Both empty for non-distributed models.
	DistributedRole string `json:"distributedRole,omitempty"`
	LayerRange      string `json:"layerRange,omitempty"`
	Notes           string `json:"notes,omitempty"`
	// (fields below are populated at runtime, not part of the curated definition)
	Installed    bool  `json:"installed"`
	Partial      bool  `json:"partial"`
	PartialBytes int64 `json:"partialBytes,omitempty"`
	Default      bool  `json:"default"`
}

const (
	hfRepo = "antirez/deepseek-v4-gguf"

	// glmRepo hosts the GLM 5.2 quants ds4's download_model.sh offers as
	// glm-antirez-*. ds4 spells it "antirez/GLM-5.2-GGUF" because it fetches
	// GLM with the Hugging Face CLI, which follows redirects; ds4go reads
	// x-linked-size/x-linked-etag off the resolve response and so must use the
	// canonical lower-case name that answers directly.
	glmRepo = "antirez/glm-5.2-gguf"

	// glm53FlashRepo hosts the GLM 5.3 Flash quants (ds4's glm53-q2 / glm53-q4
	// targets); glm53FullRepo hosts the full GLM 5.3 checkpoint (glm53-full-q2).
	glm53FlashRepo = "antirez/glm-5.3-flash-gguf"
	glm53FullRepo  = "antirez/glm-5.3-gguf"

	// ds41Repo hosts DeepSeek V4.1 Flash (ds4's ds41f-* targets): a different
	// model family from V4 Flash with its own GGUFs, tokenizer, and encoder.
	ds41Repo = "antirez/deepseek-v4.1-flash-gguf"

	// DefaultModelSymlink is the name of the active-model symlink in ModelsDir.
	DefaultModelSymlink = "ds4flash.gguf"

	// ConfigFileName is the ds4go configuration file name.
	ConfigFileName = "ds4go.json"

	// MTPAlias is the curated alias for the MTP companion model.
	MTPAlias = "mtp"

	// RecommendedModelAlias is the suggested default model for first-time users.
	RecommendedModelAlias = "q2-imatrix"
)

var hfRepoBase = "https://huggingface.co/" + hfRepo + "/resolve/main"

// modelDownloadURL returns the resolve URL for a curated model. Entries without
// an explicit Repo use hfRepoBase, which tests override to point at a local
// server.
func modelDownloadURL(m Model) string {
	return repoDownloadBase(m.Repo) + "/" + m.FileName
}

// partDownloadURL returns the resolve URL of one piece of a split model.
func partDownloadURL(m Model, part ModelPart) string {
	return repoDownloadBase(m.Repo) + "/" + part.FileName
}

// repoDownloadBase derives a repo's resolve base from hfRepoBase by swapping
// the default repo name, so a test that points hfRepoBase at a local server
// captures every repo's downloads.
func repoDownloadBase(repo string) string {
	base := strings.TrimRight(hfRepoBase, "/")
	if repo != "" && repo != hfRepo {
		base = strings.Replace(base, hfRepo, repo, 1)
	}
	return base
}

var curated = []Model{
	{
		Alias:          "q2-imatrix",
		FileName:       "DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix.gguf",
		SizeGB:         80.8,
		RecommendedRAM: "96-128 GB",
		SHA256:         "efc7ed607ff27076e3e501fc3fefefa33c0ed8cf1eff483a2b7fdc0c2e616668",
		Imatrix:        true,
		Notes:          "preferred imatrix-tuned default",
	},
	{
		Alias:          "q2-q4-imatrix",
		FileName:       "DeepSeek-V4-Flash-Layers37-42Q4KExperts-OtherExpertLayersIQ2XXSGateUp-Q2KDown-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix-fixed.gguf",
		SizeGB:         90.9,
		RecommendedRAM: "128-192 GB",
		SHA256:         "edabc92af63ad8b139f00087fbfc10a4072f37b7597f4fd9ad1dfa6f83002396",
		Imatrix:        true,
		Notes:          "mixed q2/q4 imatrix: q2 routed experts with last 6 layers q4",
	},
	{
		Alias:          "q4-imatrix",
		FileName:       "DeepSeek-V4-Flash-Q4KExperts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2-imatrix.gguf",
		SizeGB:         153.3,
		RecommendedRAM: ">=256 GB",
		SHA256:         "a2a3b31eca06344b93d32b2095511c4d36f92739a68a599b22047b4b2335d859",
		Imatrix:        true,
		Notes:          "higher quality, much larger memory footprint",
	},
	{
		Alias:          "pro-q2-imatrix",
		FileName:       "DeepSeek-V4-Pro-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-Instruct-imatrix.gguf",
		SizeGB:         432.7,
		RecommendedRAM: ">=512 GB",
		SHA256:         "a0314d9c0e16122cd60071079124a2d17185d317c55a8f95ecb3ed3506278a96",
		Imatrix:        true,
		Notes:          "DeepSeek V4 Pro q2 imatrix quant; 512 GB RAM machines",
	},
	{
		Alias:           "pro-q4-layers00-30",
		FileName:        "DeepSeek-V4-Pro-Q4K-Layers00-30.gguf",
		SizeGB:          426.1,
		RecommendedRAM:  "distributed, 2 hosts",
		SHA256:          "3c4526735ce204a99174059b216db155846b729bf5014c6b86d573323daa3cfa",
		Distributed:     true,
		DistributedRole: "coordinator",
		LayerRange:      "0:30",
		Notes:           "DeepSeek V4 Pro Q4 distributed split: coordinator half; run with --role coordinator --layers 0:30",
	},
	{
		Alias:           "pro-q4-layers31-output",
		FileName:        "DeepSeek-V4-Pro-Q4K-Layers-31-output.gguf",
		SizeGB:          411.6,
		RecommendedRAM:  "distributed, 2 hosts",
		SHA256:          "41d14e4ccf9a9b777899887ac4d6115b11e5a5125f051e9fa5e727656ad5179b",
		Distributed:     true,
		DistributedRole: "worker",
		LayerRange:      "31:output",
		Notes:           "DeepSeek V4 Pro Q4 distributed split: worker half; run with --role worker --layers 31:output",
	},
	{
		Alias:          "glm-iq2xxs",
		FileName:       "GLM-5.2-UD-IQ2_XXS_RoutedIQ2XXS_blk78Q2K.gguf",
		Repo:           glmRepo,
		GLM:            true,
		SizeGB:         196.6,
		RecommendedRAM: ">=256 GB",
		SHA256:         "a49de64c5020432bdae23de36a423a9660a5621bc0db8d12b66bd8814b07fea0",
		Notes:          "GLM 5.2 routed IQ2_XXS with Q2_K block 78; reduced-memory testing",
	},
	{
		Alias:          "glm-q2",
		FileName:       "GLM-5.2-UD-Q2_K_RoutedQ2K.gguf",
		Repo:           glmRepo,
		GLM:            true,
		SizeGB:         244.0,
		RecommendedRAM: ">=320 GB",
		SHA256:         "b9fa49d010dad35b96418c45831c212a746715b0646c1121ccfc414455bd6fe5",
		Notes:          "GLM 5.2 routed Q2_K",
	},
	{
		Alias:          "glm-q4",
		FileName:       "GLM-5.2-UD-Q4_K_RoutedQ4K.gguf",
		Repo:           glmRepo,
		GLM:            true,
		SizeGB:         404.4,
		RecommendedRAM: ">=512 GB",
		SHA256:         "7160879c87756236eea16ec6bfeb19288d16fa94dcfcef3a5ed5f38b1383d3a5",
		Notes:          "GLM 5.2 routed Q4_K; highest quality, largest footprint",
	},
	{
		Alias:          "glm53-q2",
		FileName:       "GLM-5.3-Flash-Q2.gguf",
		Repo:           glm53FlashRepo,
		GLM:            true,
		SizeGB:         89.9,
		RecommendedRAM: "128 GB",
		SHA256:         "e81fd6241c6e55a64e1e14e47a3eab61a173fa8d7e4b5c1d1848827119705b32",
		Vision:         true,
		Encoder:        "glm53-vision",
		Notes:          "GLM 5.3 Flash imatrix IQ2_XXS/Q2_K experts; one 128 GB Mac or DGX Spark, embedded MTP",
	},
	{
		Alias:          "glm53-q4",
		FileName:       "GLM-5.3-Flash-Q4_K.gguf",
		Repo:           glm53FlashRepo,
		GLM:            true,
		SizeGB:         177.8,
		RecommendedRAM: ">=192 GB",
		SHA256:         "c7a0d950363238dd7804782c88340d737775aba53a15f8d4fdcc34e984f25221",
		Vision:         true,
		Encoder:        "glm53-vision",
		Notes:          "GLM 5.3 Flash Q4_K; larger Mac, two 128 GB Macs, or SSD streaming",
	},
	{
		Alias:          "glm53-full-q2",
		FileName:       "GLM-5.3-UD-IQ2_XXS_RoutedIQ2XXS_blk78Q2K.gguf",
		Repo:           glm53FullRepo,
		GLM:            true,
		SizeGB:         196.6,
		RecommendedRAM: ">=256 GB",
		SHA256:         "059b36accd4c9acf73099da9f703b574d627869d619b7c4c316aa856e33d472e",
		Notes:          "full GLM 5.3 routed IQ2_XXS with Q2_K block 78; large machine or --ssd-streaming",
	},
	{
		Alias:          "vision-q2",
		FileName:       "DeepSeek-V4-Flash-Vision-Exp-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8.gguf",
		SizeGB:         80.8,
		RecommendedRAM: "96-128 GB",
		SHA256:         "8f2d42c0071ccf8a98f391cc2b835fd123f12330690b3059dbb7707920e5ad9e",
		Vision:         true,
		Encoder:        "vision-encoder",
		DSpark:         "vision-dspark-support",
		Notes:          "DeepSeek V4 Flash Vision-Exp q2; a different checkpoint from Flash 0731, pairs with vision-encoder",
	},
	{
		Alias:          "vision-q2-q4",
		FileName:       "DeepSeek-V4-Flash-Vision-Exp-Layers37-42Q4KExperts-OtherExpertLayersIQ2XXSGateUp-Q2KDown-AProjQ8-SExpQ8-OutQ8.gguf",
		SizeGB:         90.9,
		RecommendedRAM: "128-192 GB",
		SHA256:         "cded4517bb9d033e778e8bc4ccf1e79ba96d1c2d2b9f1c071c1d4a9037c51b02",
		Vision:         true,
		Encoder:        "vision-encoder",
		DSpark:         "vision-dspark-support",
		Notes:          "Vision-Exp mixed q2/q4",
	},
	{
		Alias:          "vision-mxfp4",
		FileName:       "DeepSeek-V4-Flash-Vision-Exp-MXFP4Experts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out.gguf",
		SizeGB:         145.3,
		RecommendedRAM: ">=192 GB",
		SHA256:         "fc1efb96fa26e654b3530ce5f4b926b189a936d41d94dc1903c832f1e18eb3e7",
		Vision:         true,
		Encoder:        "vision-encoder",
		DSpark:         "vision-dspark-support",
		Notes:          "Vision-Exp MXFP4 experts",
	},
	{
		Alias:          "vision-encoder",
		FileName:       "DeepSeek-V4-Flash-Vision-Encoder.gguf",
		SizeGB:         0.9,
		RecommendedRAM: "optional",
		SHA256:         "00cd4d81a435364967400a95c42703343e11da6b6f18c5143fe76e1d94d5035f",
		Optional:       true,
		Notes:          "vision encoder for the Vision-Exp checkpoints; loaded with --vision",
	},
	{
		Alias:          "vision-dspark-support",
		FileName:       "DeepSeek-V4-Flash-Vision-Exp-DSpark-support.gguf",
		SizeGB:         5.6,
		RecommendedRAM: "optional",
		SHA256:         "0807a67fd9ce5874bfc60d8d2461f50e11657e3dd94913d3473f85aa679bc877",
		Optional:       true,
		Notes:          "DSpark drafter for the Vision-Exp checkpoints only; the 0731 drafter is rejected",
	},
	{
		Alias:          "v41-q2",
		FileName:       "DeepSeek-V4.1-Flash-Q2.gguf",
		Repo:           ds41Repo,
		SizeGB:         340.6,
		RecommendedRAM: "128 GB + SSD streaming; Metal only",
		SHA256:         "1ce6a8f8806205c13330d7ca287bd198331dc5ca35ccc5d8a9a92a188a6f6f42",
		DeepSeek41:     true,
		Vision:         true,
		Encoder:        "v41-vision",
		Imatrix:        true,
		Notes:          "DeepSeek V4.1 Flash q2 (152 GiB main weights + 189 GiB on-disk Engram tables); one 128 GB Mac with --ssd-streaming, or two over RDMA; keep it on a fast local SSD",
	},
	{
		Alias:          "v41-q4",
		FileName:       "DeepSeek-V4.1-Flash-Q4.gguf",
		Repo:           ds41Repo,
		SizeGB:         483.0,
		RecommendedRAM: ">=512 GB, or SSD streaming; Metal only",
		SHA256:         "a5e2e2c3ada4b2e98d9f9e4b50f6d9c2a12c2c96f5da165c07e13aff9264984e",
		DeepSeek41:     true,
		Vision:         true,
		Encoder:        "v41-vision",
		Imatrix:        true,
		Parts: []ModelPart{
			{FileName: "DeepSeek-V4.1-Flash-Q4.gguf.part1", SizeGB: 447.0, Bytes: 480000000000, SHA256: "6442b1f9224079662c02003c0ef9ef6be6e2aff509510f681dab9e6cc41df246"},
			{FileName: "DeepSeek-V4.1-Flash-Q4.gguf.part2", SizeGB: 35.9, Bytes: 38596067328, SHA256: "7c3e10646c918eeaffbc39305a75ec96117450262c61454ff194cef00d7617f0"},
		},
		Notes: "DeepSeek V4.1 Flash q4 (294 GiB main weights); published in two parts that are joined after download, allow 37 GiB extra while joining",
	},
	{
		Alias:          "v41-vision",
		FileName:       "DeepSeek-V4.1-Flash-Vision.gguf",
		Repo:           ds41Repo,
		SizeGB:         0.9,
		RecommendedRAM: "optional",
		SHA256:         "cc283f032b3e8b8d78aeb5fccaa14e97b859b0c53aae3cd6bffa690ddf0e9e15",
		DeepSeek41:     true,
		Optional:       true,
		Notes:          "vision encoder for DeepSeek V4.1 Flash; V4 encoders do not work with V4.1",
	},
	{
		Alias:          "glm53-fp8",
		FileName:       "GLM-5.3-Flash-FP8.gguf",
		Repo:           glm53FlashRepo,
		SizeGB:         304.7,
		RecommendedRAM: ">=384 GB",
		SHA256:         "59275e79a5246835226230616b3865fb599c661f242c38316b1cf82869bd14c9",
		GLM:            true,
		Vision:         true,
		Encoder:        "glm53-vision",
		Notes:          "GLM 5.3 Flash FP8, the unquantized reference",
	},
	{
		Alias:          "glm53-vision",
		FileName:       "GLM-5.3-Flash-Vision-Encoder.gguf",
		Repo:           glm53FlashRepo,
		SizeGB:         1.0,
		RecommendedRAM: "optional",
		SHA256:         "ae23e14c6979e889051b2e4a39351abcdafb161e18e606fae4d8c40095a4bf3a",
		Optional:       true,
		Notes:          "vision encoder for GLM 5.3 Flash; loaded with --vision",
	},
	{
		Alias:          "q2-imatrix-0731",
		FileName:       "DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix-0731.gguf",
		SizeGB:         80.8,
		RecommendedRAM: "96-128 GB",
		SHA256:         "ca22ae2f838e14077c22bc1c1417b71b45b5e5a3687bd96c2ac6e17fdb6261c0",
		Imatrix:        true,
		Notes:          "refreshed 0731 build of the preferred imatrix default",
	},
	{
		Alias:          "q2-q4-imatrix-0731",
		FileName:       "DeepSeek-V4-Flash-Layers37-42Q4KExperts-OtherExpertLayersIQ2XXSGateUp-Q2KDown-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix-fixed-0731.gguf",
		SizeGB:         90.9,
		RecommendedRAM: "128-192 GB",
		SHA256:         "659e22fbd01c9e13ea37a57c8d9c41e0a8819dffa3473d3c5286ee44b2d3398f",
		Imatrix:        true,
		Notes:          "refreshed 0731 mixed q2/q4 imatrix",
	},
	{
		Alias:          "q4-imatrix-0731",
		FileName:       "DeepSeek-V4-Flash-Q4KExperts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2-imatrix-0731.gguf",
		SizeGB:         153.3,
		RecommendedRAM: ">=256 GB",
		SHA256:         "6bb77b5ddcbc2d974c687cfb63d644ecfb295581b4a53fa4c1d810aea538254a",
		Imatrix:        true,
		Notes:          "refreshed 0731 q4 imatrix",
	},
	{
		Alias:          "mxfp4-0731",
		FileName:       "DeepSeek-V4-Flash-MXFP4Experts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2-mxfp4-0731.gguf",
		SizeGB:         145.3,
		RecommendedRAM: ">=192 GB",
		SHA256:         "0e3a161b670f686128ec5f92a601dfde616a37bf5e7e48999fa2d32471b57ec6",
		Notes:          "MXFP4 experts; Metal has exact fast paths for this format",
	},
	{
		Alias:          "pro-q2-imatrix-0813",
		FileName:       "DeepSeek-V4-Pro-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-Instruct-imatrix-0813.gguf",
		SizeGB:         432.7,
		RecommendedRAM: ">=512 GB",
		SHA256:         "c4d997ab9894b6c78b759f7869fe1726b6314b6515f6ff82607df3797c5eb193",
		Imatrix:        true,
		Notes:          "refreshed 0813 DeepSeek V4 Pro q2 imatrix",
	},
	{
		Alias:          "dspark-support",
		FileName:       "DeepSeek-V4-Flash-DSpark-support-0731.gguf",
		SizeGB:         5.6,
		RecommendedRAM: "optional",
		SHA256:         "7e319924541db3f7a163ed7e11d7532a70d48228ab59d36cb81e1d4511885360",
		Optional:       true,
		Notes:          "DSpark speculative decoding support model; pair with --mtp",
	},
	{
		Alias:          "mtp",
		FileName:       "DeepSeek-V4-Flash-MTP-Q4K-Q8_0-F32.gguf",
		SizeGB:         3.5,
		RecommendedRAM: "optional",
		SHA256:         "afd481ee689dce9037f70f39085fcdae5a5b096d521cdad43b19fa52bf8f4083",
		Optional:       true,
		Notes:          "speculative decoding companion model",
	},
}

// Curated returns a copy of the curated ds4 model catalog.
func Curated() []Model {
	out := make([]Model, len(curated))
	copy(out, curated)
	for i := range out {
		out[i].GGUFPath = "models/" + out[i].FileName
	}
	return out
}

// ModelForPath returns the curated model whose GGUF file is selected by path.
// It follows a final-model symlink such as ds4flash.gguf before matching, while
// also accepting a direct path to a curated file.
func ModelForPath(path string) (Model, bool) {
	if path == "" {
		return Model{}, false
	}
	base := filepath.Base(filepath.Clean(path))
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		base = filepath.Base(resolved)
	}
	models := Curated()
	for _, model := range models {
		if model.FileName == base {
			return model, true
		}
	}
	// The active ds4flash.gguf is normally a hard link, not a symlink. Match
	// its file identity against curated files beside it so model-family policy
	// still follows the selected catalog entry.
	selected, err := os.Stat(path)
	if err != nil {
		return Model{}, false
	}
	dir := filepath.Dir(filepath.Clean(path))
	for _, model := range models {
		candidate, err := os.Stat(filepath.Join(dir, model.FileName))
		if err == nil && os.SameFile(selected, candidate) {
			return model, true
		}
	}
	return Model{}, false
}
