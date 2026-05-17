package ds4

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDirUsesDS4Dir(t *testing.T) {
	t.Setenv("DS4_DIR", "/tmp/example-ds4")
	if got := DefaultDir(); got != "/tmp/example-ds4" {
		t.Fatalf("DefaultDir() = %q, want DS4_DIR", got)
	}
}

func TestDefaultLibraryPathSearchesDS4DirLib(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	t.Setenv("DS4_LIB", "")

	libDir := filepath.Join(dir, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(libDir, libraryFileName())
	if err := os.WriteFile(want, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DefaultLibraryPath(); got != want {
		t.Fatalf("DefaultLibraryPath() = %q, want %q", got, want)
	}
}

func TestDefaultModelPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	want := filepath.Join(dir, "models", "ds4flash.gguf")
	if got := DefaultModelPath(); got != want {
		t.Fatalf("DefaultModelPath() = %q, want %q", got, want)
	}
}

func TestDefaultLibraryPathIgnoresCWD(t *testing.T) {
	// A libds4 planted in the working directory must never be selected:
	// loading a shared library from the CWD is a binary-planting vector.
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("DS4_DIR", t.TempDir()) // empty: no DS4_DIR/lib candidate exists
	t.Setenv("DS4_LIB", "")

	name := libraryFileName()
	planted := filepath.Join(cwd, name)
	plantedLib := filepath.Join(cwd, "lib", name)
	if err := os.WriteFile(planted, []byte("malicious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plantedLib, []byte("malicious"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DefaultLibraryPath(); got == planted || got == plantedLib {
		t.Fatalf("DefaultLibraryPath() = %q, must not resolve to a working-directory library", got)
	}
}
