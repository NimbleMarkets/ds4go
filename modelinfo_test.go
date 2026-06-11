package ds4

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureCatalogDir builds a DS4_DIR with one installed curated model
// (q2-q4-imatrix), the ds4flash.gguf default hardlink the installer
// maintains, and a ds4go.json naming it the default. Returns the real
// gguf path and the link path.
func fixtureCatalogDir(t *testing.T) (real, link string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	real = filepath.Join(modelsDir, "DeepSeek-V4-Flash-Layers37-42Q4KExperts-OtherExpertLayersIQ2XXSGateUp-Q2KDown-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix-fixed.gguf")
	if err := os.WriteFile(real, []byte("fake gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(modelsDir, "ds4flash.gguf")
	if err := os.Link(real, link); err != nil {
		t.Fatal(err)
	}
	cfg := `{"defaultModel":"q2-q4-imatrix"}`
	if err := os.WriteFile(filepath.Join(dir, "ds4go.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return real, link
}

func TestResolveModelInfoDefaultLink(t *testing.T) {
	real, link := fixtureCatalogDir(t)

	info, ok := ResolveModelInfo(link)
	if !ok {
		t.Fatal("ResolveModelInfo did not resolve the default link")
	}
	if info.Alias != "q2-q4-imatrix" {
		t.Errorf("Alias = %q, want q2-q4-imatrix", info.Alias)
	}
	if info.FileName != filepath.Base(real) {
		t.Errorf("FileName = %q, want %q", info.FileName, filepath.Base(real))
	}
	if !info.Default {
		t.Error("Default = false, want true (config names this alias)")
	}
	if info.SHA256 == "" {
		t.Error("SHA256 empty, want the catalog's pinned hash")
	}
	if !info.Imatrix {
		t.Error("Imatrix = false, want true")
	}
	if info.Notes == "" {
		t.Error("Notes empty, want the catalog notes")
	}
}

func TestResolveModelInfoDirectPath(t *testing.T) {
	real, _ := fixtureCatalogDir(t)

	info, ok := ResolveModelInfo(real)
	if !ok {
		t.Fatal("ResolveModelInfo did not resolve the direct gguf path")
	}
	if info.Alias != "q2-q4-imatrix" {
		t.Errorf("Alias = %q, want q2-q4-imatrix", info.Alias)
	}
}

func TestResolveModelInfoUnknownPath(t *testing.T) {
	_, _ = fixtureCatalogDir(t)
	dir := os.Getenv("DS4_DIR")

	stray := filepath.Join(dir, "models", "homebrew-quant.gguf")
	if err := os.WriteFile(stray, []byte("not in catalog"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ResolveModelInfo(stray); ok {
		t.Error("resolved a gguf that is not in the catalog")
	}
	if _, ok := ResolveModelInfo(filepath.Join(dir, "missing.gguf")); ok {
		t.Error("resolved a nonexistent path")
	}
}
