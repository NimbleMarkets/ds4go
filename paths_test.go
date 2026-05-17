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
