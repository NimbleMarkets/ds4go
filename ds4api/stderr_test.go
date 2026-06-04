package ds4api

import (
	"os"
	"testing"
)

// SetStderrFd must forward the descriptor to the library so that subsequent
// libds4 diagnostics land in the redirected stream, and -1 must restore the
// default so later writes no longer reach it.
func TestLibrarySetStderrFdRedirectsLogOutput(t *testing.T) {
	lib := NewMockLibrary()
	f, err := os.CreateTemp(t.TempDir(), "ds4-stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := lib.SetStderrFd(int(f.Fd())); err != nil {
		t.Fatalf("SetStderrFd: %v", err)
	}
	lib.raw.ds4LogString(0, LogWarning, "%s", "ds4: redirected\n")

	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ds4: redirected\n" {
		t.Fatalf("redirected log = %q, want %q", got, "ds4: redirected\n")
	}

	// Restore: later writes must not land in the redirected file.
	if err := lib.SetStderrFd(-1); err != nil {
		t.Fatalf("SetStderrFd(-1): %v", err)
	}
	lib.raw.ds4LogString(0, LogError, "%s", "ds4: native\n")
	if got2, _ := os.ReadFile(f.Name()); string(got2) != "ds4: redirected\n" {
		t.Fatalf("file changed after restore = %q", got2)
	}
}
