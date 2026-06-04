//go:build ds4_integration

// Run with: DS4_LIB=/path/to/libds4.dylib go test -tags ds4_integration ./ds4api/ -run TestRealLibrarySetStderrFd
// Requires a libds4 built from the nm-shared-stderr API (exports ds4_set_stderr_fd).

package ds4api

import (
	"os"
	"testing"
)

// Loading the real library exercises the migrated symbol table: ds4_set_stderr_fd
// must resolve and ds4_log_set must no longer be required. SetStderrFd must then
// round-trip a descriptor and restore (-1) without error.
func TestRealLibrarySetStderrFd(t *testing.T) {
	libPath := os.Getenv("DS4_LIB")
	if libPath == "" {
		t.Skip("DS4_LIB must be set")
	}
	lib, err := Load(libPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", libPath, err)
	}

	f, err := os.CreateTemp(t.TempDir(), "ds4-stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := lib.SetStderrFd(int(f.Fd())); err != nil {
		t.Fatalf("SetStderrFd: %v", err)
	}
	if err := lib.SetStderrFd(-1); err != nil {
		t.Fatalf("SetStderrFd(-1): %v", err)
	}
}
