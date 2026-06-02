// Package models manages ds4go's curated model catalog.
package models

// Model describes a curated ds4 GGUF model.
type Model struct {
	Alias          string  `json:"alias"`
	GGUFPath       string  `json:"ggufPath"`
	FileName       string  `json:"fileName"`
	SizeGB         float64 `json:"sizeGB"`
	RecommendedRAM string  `json:"recommendedRAM"`
	SHA256         string  `json:"sha256,omitempty"`
	Imatrix        bool    `json:"imatrix"`
	Legacy         bool    `json:"legacy"`
	Optional       bool    `json:"optional"`
	Distributed    bool    `json:"distributed"`
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
