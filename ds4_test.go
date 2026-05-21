package ds4

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIsPathWithinSafeDomain(t *testing.T) {
	// Setup custom DS4_DIR using TempDir to isolate tests.
	tempDir := t.TempDir()
	t.Setenv("DS4_DIR", tempDir)

	// Also mock DS4_LIB for domain tests.
	ds4LibPath := filepath.Join(tempDir, "custom_libds4.dylib")
	t.Setenv("DS4_LIB", ds4LibPath)

	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "empty path",
			path: "",
			want: false,
		},
		{
			name: "bare valid library name macos",
			path: "libds4.dylib",
			want: true,
		},
		{
			name: "bare valid library name windows",
			path: "libds4.dll",
			want: true,
		},
		{
			name: "bare valid library name linux",
			path: "libds4.so",
			want: true,
		},
		{
			name: "bare invalid library name",
			path: "invalid.dylib",
			want: false,
		},
		{
			name: "within DefaultDir",
			path: filepath.Join(tempDir, "lib", "libds4.dylib"),
			want: true,
		},
		{
			name: "exact match DefaultDir",
			path: tempDir,
			want: true,
		},
		{
			name: "exact match DS4_LIB",
			path: ds4LibPath,
			want: true,
		},
		{
			name: "path traversal escaping DefaultDir",
			path: filepath.Join(tempDir, "..", "some_other_dir"),
			want: false,
		},
		{
			name: "path traversal trying to query sensitive system files",
			path: filepath.Join(tempDir, "..", "..", "etc", "passwd"),
			want: false,
		},
		{
			name: "arbitrary absolute path",
			path: "/etc/passwd",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPathWithinSafeDomain(tt.path)
			if got != tt.want {
				t.Errorf("isPathWithinSafeDomain(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestLibraryHoldersSecurityValidation(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	t.Setenv("DS4_LIB", "")

	// 1. LibraryHolders with a path outside the safe domain must return an error.
	_, err := LibraryHolders("/etc/passwd")
	if err == nil {
		t.Fatal("expected LibraryHolders with unsafe path to return an error, but got nil")
	}
	if !strings.Contains(err.Error(), "outside the authorized security domain") {
		t.Errorf("expected error message to mention security domain, got: %v", err)
	}

	// 2. LibraryHolders with empty string should not fail security checks immediately
	// (it will try to resolve the default library path and check existence/holders).
	// Because the default library won't exist in the temp directory, we expect it to
	// return nil, nil or a not exist error, but NOT a security domain error.
	_, err = LibraryHolders("")
	if err != nil {
		if strings.Contains(err.Error(), "outside the authorized security domain") {
			t.Errorf("expected empty path LibraryHolders to not fail security validation, got: %v", err)
		}
	}
}

func TestEngineHoldersSecurityValidation(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())

	// 1. EngineHolders with a directory outside the safe domain must return an error.
	_, err := EngineHolders("/etc")
	if err == nil {
		t.Fatal("expected EngineHolders with unsafe path to return an error, but got nil")
	}
	if !strings.Contains(err.Error(), "outside the authorized security domain") {
		t.Errorf("expected error message to mention security domain, got: %v", err)
	}

	// 2. EngineHolders with empty string should not fail security checks immediately
	// because it resolves to the default models directory (which is within DefaultDir).
	// Since the directory might not exist yet, we expect it to succeed with nil/empty map,
	// or return nil, nil, but NOT a security domain error.
	holders, err := EngineHolders("")
	if err != nil {
		if strings.Contains(err.Error(), "outside the authorized security domain") {
			t.Errorf("expected empty path EngineHolders to not fail security validation, got: %v", err)
		}
	} else {
		// Verify that it runs and returns a map (or nil if directory does not exist).
		if holders != nil && len(holders) > 0 {
			t.Logf("found unexpected holders on empty default dir: %v", holders)
		}
	}
}
