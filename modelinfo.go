package ds4

import (
	"os"
	"path/filepath"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

// ModelInfo identifies the ds4 catalog entry behind a model file path.
type ModelInfo struct {
	// Alias is the catalog moniker (e.g. "q2-q4-imatrix").
	Alias string
	// FileName is the real GGUF file name the entry installs as.
	FileName string
	// SHA256 is the pinned (or verified-at-download) content hash.
	SHA256 string
	// SizeGB is the curated download size.
	SizeGB float64
	// Imatrix reports whether this is an imatrix-tuned quant.
	Imatrix bool
	// Notes is the curated one-line description.
	Notes string
	// Default reports whether this entry is the configured default chat model.
	Default bool
}

// ResolveModelInfo maps a model file path back to its ds4 catalog entry.
// The path is matched against installed catalog models by file identity
// (os.SameFile), so the models/ds4flash.gguf default link — maintained as a
// hard link by the installer — resolves to the entry it points at, as does
// a direct path to the GGUF. Returns false for paths that do not stat or
// are not catalog-managed.
func ResolveModelInfo(path string) (ModelInfo, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return ModelInfo{}, false
	}
	mgr := models.NewManager()
	list, _, err := mgr.List()
	if err != nil {
		return ModelInfo{}, false
	}
	for _, m := range list {
		if !m.Installed {
			continue
		}
		mst, err := os.Stat(filepath.Join(mgr.ModelsDir, m.FileName))
		if err != nil {
			continue
		}
		if os.SameFile(st, mst) {
			return ModelInfo{
				Alias:    m.Alias,
				FileName: m.FileName,
				SHA256:   m.SHA256,
				SizeGB:   m.SizeGB,
				Imatrix:  m.Imatrix,
				Notes:    m.Notes,
				Default:  m.Default,
			}, true
		}
	}
	return ModelInfo{}, false
}
