package ds4api

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeLib writes a fake libds4 with an exact permission mode (bypassing umask).
func writeLib(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake libds4"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyLibraryAcceptsSafeFile(t *testing.T) {
	path := writeLib(t, t.TempDir(), "libds4.so", 0o644)
	if err := verifyLibrary(path); err != nil {
		t.Fatalf("verifyLibrary(safe file) = %v, want nil", err)
	}
}

func TestVerifyLibraryBareNameSkipped(t *testing.T) {
	if err := verifyLibrary("libds4.so"); err != nil {
		t.Fatalf("verifyLibrary(bare name) = %v, want nil", err)
	}
}

func TestVerifyLibraryRejectsWritableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not meaningful on Windows")
	}
	path := writeLib(t, t.TempDir(), "libds4.so", 0o666)
	if err := verifyLibrary(path); err == nil {
		t.Fatal("verifyLibrary(group/other-writable file) = nil, want error")
	}
}

func TestVerifyLibraryRejectsWritableDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	path := writeLib(t, dir, "libds4.so", 0o644)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := verifyLibrary(path); err == nil {
		t.Fatal("verifyLibrary(file in world-writable dir) = nil, want error")
	}
}

func TestVerifyLibraryChecksumSidecar(t *testing.T) {
	path := writeLib(t, t.TempDir(), "libds4.so", 0o644)
	sum, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}

	// A matching sidecar passes.
	if err := os.WriteFile(path+".sha256", []byte(sum+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyLibrary(path); err != nil {
		t.Fatalf("verifyLibrary(matching sidecar) = %v, want nil", err)
	}

	// A mismatched sidecar (tampered library, or corruption) fails.
	if err := os.WriteFile(path+".sha256", []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyLibrary(path); err == nil {
		t.Fatal("verifyLibrary(mismatched sidecar) = nil, want error")
	}
}
