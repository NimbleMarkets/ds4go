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
	Notes          string  `json:"notes,omitempty"`
	Installed      bool    `json:"installed"`
	Partial        bool    `json:"partial"`
	PartialBytes   int64   `json:"partialBytes,omitempty"`
	Default        bool    `json:"default"`
}

const hfRepo = "antirez/deepseek-v4-gguf"

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
		Alias:          "q4-imatrix",
		FileName:       "DeepSeek-V4-Flash-Q4KExperts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2-imatrix.gguf",
		SizeGB:         153,
		RecommendedRAM: ">=256 GB",
		SHA256:         "a2a3b31eca06344b93d32b2095511c4d36f92739a68a599b22047b4b2335d859",
		Imatrix:        true,
		Notes:          "higher quality, much larger memory footprint",
	},
	{
		Alias:          "q2",
		FileName:       "DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2.gguf",
		SizeGB:         87,
		RecommendedRAM: "96-128 GB",
		SHA256:         "31598c67c8b8744d3bcebcd19aa62253c6dc43cef3b8adf9f593656c9e86fd8c",
		Legacy:         true,
		Notes:          "legacy non-imatrix q2",
	},
	{
		Alias:          "q4",
		FileName:       "DeepSeek-V4-Flash-Q4KExperts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2.gguf",
		SizeGB:         165,
		RecommendedRAM: ">=256 GB",
		SHA256:         "39e5de72ac544fdd5ffaf83ec28e36aaf3341b145235488e67d59400bbb3af55",
		Legacy:         true,
		Notes:          "legacy non-imatrix q4",
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
