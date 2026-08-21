// Package models manages ds4go's curated model catalog.
package models

import (
	"os"
	"path/filepath"
	"strings"
)

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
	GLM         bool `json:"glm,omitempty"`
	Imatrix     bool `json:"imatrix"`
	Legacy      bool `json:"legacy"`
	Optional    bool `json:"optional"`
	Distributed bool `json:"distributed"`
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
	base := hfRepoBase
	if m.Repo != "" && m.Repo != hfRepo {
		base = "https://huggingface.co/" + m.Repo + "/resolve/main"
	}
	return strings.TrimRight(base, "/") + "/" + m.FileName
}

var curated = []Model{
	{
		Alias:          "q2-imatrix",
		FileName:       "DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix.gguf",
		SizeGB:         81.2,
		RecommendedRAM: "96-128 GB",
		SHA256:         "efc7ed607ff27076e3e501fc3fefefa33c0ed8cf1eff483a2b7fdc0c2e616668",
		Imatrix:        true,
		Notes:          "preferred imatrix-tuned default",
	},
	{
		Alias:          "q2-q4-imatrix",
		FileName:       "DeepSeek-V4-Flash-Layers37-42Q4KExperts-OtherExpertLayersIQ2XXSGateUp-Q2KDown-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix-fixed.gguf",
		SizeGB:         98,
		RecommendedRAM: "128-192 GB",
		SHA256:         "edabc92af63ad8b139f00087fbfc10a4072f37b7597f4fd9ad1dfa6f83002396",
		Imatrix:        true,
		Notes:          "mixed q2/q4 imatrix: q2 routed experts with last 6 layers q4",
	},
	{
		Alias:          "q4-imatrix",
		FileName:       "DeepSeek-V4-Flash-Q4KExperts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2-imatrix.gguf",
		SizeGB:         153,
		RecommendedRAM: ">=256 GB",
		SHA256:         "a2a3b31eca06344b93d32b2095511c4d36f92739a68a599b22047b4b2335d859",
		Imatrix:        true,
		Notes:          "higher quality, much larger memory footprint",
	},
	{
		Alias:          "pro-q2-imatrix",
		FileName:       "DeepSeek-V4-Pro-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-Instruct-imatrix.gguf",
		SizeGB:         430,
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
		Alias:          "mtp",
		FileName:       "DeepSeek-V4-Flash-MTP-Q4K-Q8_0-F32.gguf",
		SizeGB:         3.6,
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
