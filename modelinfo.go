package ds4

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

// ModelInfo describes a curated model and its local installation state.
// Capability fields describe catalog metadata, not hardware compatibility or
// capabilities probed from a loaded engine.
type ModelInfo struct {
	// Alias is the catalog moniker (e.g. "vision-q2").
	Alias string
	// Family groups catalog quantizations of the same checkpoint, for example
	// "deepseek-v4-flash". Empty for companion files. Use this for selector
	// grouping, not runtime capability checks.
	Family string
	// FileName is the real GGUF file name the entry installs as.
	FileName string
	// Path is the model's destination under DefaultModelsDir, even if absent.
	// Check Installed before offering it as a locally available model.
	Path string
	// SHA256 is the pinned (or verified-at-download) content hash.
	SHA256 string
	// SizeGB is the curated download size.
	SizeGB float64
	// RecommendedRAM is the catalog's human-readable memory guidance.
	RecommendedRAM string
	// Imatrix reports whether this is an imatrix-tuned quant.
	Imatrix bool
	// Notes is the curated one-line description.
	Notes string
	// Installed reports a nonempty model file on disk; it does not verify its hash.
	Installed bool
	// Partial reports whether unfinished download data exists on disk.
	Partial bool
	// PartialBytes is the size of unfinished download data, including split parts.
	PartialBytes int64
	// Default reports whether this installed entry is the configured default chat model.
	Default bool
	// Legacy marks an older catalog variant.
	Legacy bool
	// Optional marks a companion file, such as an encoder or speculative drafter.
	Optional bool
	// Distributed marks a model piece requiring distributed inference.
	Distributed bool
	// DistributedRole is "coordinator" or "worker" for distributed pieces.
	DistributedRole string
	// LayerRange describes a distributed piece's layers in ds4 --layers syntax.
	LayerRange string
	// Vision reports image support when the matching encoder is loaded.
	// It does not imply that the encoder is installed.
	Vision bool
	// Encoder is the matching vision encoder's catalog alias, or empty.
	// Find that alias in ListModels to inspect its Path and Installed state.
	Encoder string
	// DSpark is a checkpoint-specific DSpark companion alias, or empty when
	// there is no override. Empty does not imply DSpark support; use ApplyMTPDefaults
	// to resolve companion policy for engine options.
	DSpark string
	// GLM identifies the GLM DSA family.
	GLM bool
	// DeepSeek41 identifies the DeepSeek V4.1 family.
	DeepSeek41 bool
	// Qwen identifies the Qwen3.8 Flash Next family (checkpoint or encoder):
	// ChatML history, reasoning effort as a system-turn instruction, built-in
	// speculation, and a tool-call dialect the dsml package does not yet speak.
	Qwen bool
}

// IsChatModel reports whether this is a standalone chat checkpoint rather than
// a companion file or distributed piece. It does not require installation or
// check hardware compatibility. For a local chat selector, also check Installed.
func (m ModelInfo) IsChatModel() bool {
	return m.Alias != "" && !m.Optional && !m.Distributed
}

// ListModels returns a fresh snapshot of the curated catalog in catalog order,
// including models that are not installed. It uses DS4_DIR (default ~/.ds4),
// reads local configuration and file metadata, and does not load libds4,
// download files, or change the default model. Custom GGUFs outside the catalog
// are not included; applications can offer a separate file picker.
// A missing installation is valid; unreadable or malformed configuration is an error.
func ListModels() ([]ModelInfo, error) {
	mgr := models.NewManager()
	list, _, err := mgr.List()
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	result := make([]ModelInfo, 0, len(list))
	for _, m := range list {
		result = append(result, ModelInfo{
			Alias: m.Alias, Family: m.Family, FileName: m.FileName, Path: filepath.Join(mgr.ModelsDir, m.FileName),
			SHA256: m.SHA256, SizeGB: m.SizeGB, RecommendedRAM: m.RecommendedRAM,
			Imatrix: m.Imatrix, Notes: m.Notes,
			Installed: m.Installed, Partial: m.Partial, PartialBytes: m.PartialBytes, Default: m.Default,
			Legacy: m.Legacy, Optional: m.Optional, Distributed: m.Distributed,
			DistributedRole: m.DistributedRole, LayerRange: m.LayerRange,
			Vision: m.Vision, Encoder: m.Encoder, DSpark: m.DSpark, GLM: m.GLM, DeepSeek41: m.DeepSeek41, Qwen: m.Qwen,
		})
	}
	return result, nil
}

// ResolveModelInfo maps a model file path back to its ds4 catalog entry.
// The path is matched against installed catalog models by file identity
// (os.SameFile), so the models/ds4flash.gguf default link — maintained as a
// hard link by the installer — resolves to the entry it points at, as does
// a direct path to the GGUF. Returns false for paths that do not stat or
// are not catalog-managed, or if local model configuration cannot be read.
func ResolveModelInfo(path string) (ModelInfo, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return ModelInfo{}, false
	}
	list, err := ListModels()
	if err != nil {
		return ModelInfo{}, false
	}
	for _, m := range list {
		if !m.Installed {
			continue
		}
		mst, err := os.Stat(m.Path)
		if err == nil && os.SameFile(st, mst) {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// ResolveModelPath maps an installed catalog alias (for example "vision-q2")
// to its GGUF file under DefaultModelsDir. File paths, unknown aliases, and
// aliases that are not installed are returned unchanged so callers can
// validate them and report an appropriate error. It never downloads models.
func ResolveModelPath(value string) string {
	return models.NewManager().ResolvePath(value)
}
