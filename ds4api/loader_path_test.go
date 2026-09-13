package ds4api

import (
	"os"
	"path/filepath"
	"testing"
)

// With DS4_LIB unset and no executable-local library, the default path is
// "not found", never the bare platform name: a bare name sends the OS
// loader through the working directory on macOS and Windows, which is the
// binary-planting vector the search order exists to avoid.
func TestDefaultLibraryPathIsEmptyWhenNothingIsInstalled(t *testing.T) {
	t.Setenv("DS4_LIB", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	if err := os.WriteFile(filepath.Join(cwd, libraryFileName()), []byte("planted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if exe, err := os.Executable(); err == nil {
		for _, p := range []string{filepath.Join(filepath.Dir(exe), libraryFileName()), filepath.Join(filepath.Dir(exe), "lib", libraryFileName())} {
			if _, err := os.Stat(p); err == nil {
				t.Skipf("%s exists next to the test binary", p)
			}
		}
	}
	if got := defaultLibraryPath(); got != "" {
		t.Fatalf("defaultLibraryPath() = %q, want \"\"", got)
	}
	if _, err := Load(""); err == nil {
		t.Fatal("Load(\"\") succeeded with nothing installed and a library planted in the CWD")
	}
}
