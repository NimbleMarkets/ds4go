package cliopts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NimbleMarkets/ds4go"
)

func TestSelectBackend_ExplicitFlags(t *testing.T) {
	tests := []struct {
		name string
		cfg  CLIConfig
		want ds4.Backend
	}{
		{
			name: "cuda flag",
			cfg:  CLIConfig{CUDA: true},
			want: ds4.BackendCUDA,
		},
		{
			name: "rocm flag",
			cfg:  CLIConfig{ROCm: true},
			want: ds4.BackendCUDA,
		},
		{
			name: "cpu flag",
			cfg:  CLIConfig{CPU: true},
			want: ds4.BackendCPU,
		},
		{
			name: "metal flag",
			cfg:  CLIConfig{Metal: true},
			want: ds4.BackendMetal,
		},
		{
			name: "backend cuda",
			cfg:  CLIConfig{Backend: "cuda"},
			want: ds4.BackendCUDA,
		},
		{
			name: "backend rocm",
			cfg:  CLIConfig{Backend: "rocm"},
			want: ds4.BackendCUDA,
		},
		{
			name: "backend cpu case insensitive",
			cfg:  CLIConfig{Backend: "CpU"},
			want: ds4.BackendCPU,
		},
		{
			name: "backend metal",
			cfg:  CLIConfig{Backend: "metal"},
			want: ds4.BackendMetal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.SelectBackend()
			if got != tt.want {
				t.Errorf("SelectBackend() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectBackend_MetadataFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ds4go-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	libPath := filepath.Join(tempDir, "libds4.so")
	if err := os.WriteFile(libPath, []byte("mock binary"), 0644); err != nil {
		t.Fatalf("failed to write mock lib: %v", err)
	}

	meta := struct {
		Backend string `json:"backend"`
	}{
		Backend: "rocm",
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "ds4go-install.json"), metaBytes, 0644); err != nil {
		t.Fatalf("failed to write meta file: %v", err)
	}

	cfg := CLIConfig{
		Lib: libPath,
	}

	got := cfg.SelectBackend()
	if got != ds4.BackendCUDA {
		t.Errorf("expected backend CUDA/ROCm from metadata, got %v", got)
	}
}

func TestSelectBackend_FallbackDefaults(t *testing.T) {
	// With empty config and no metadata, it should resolve to platform defaults.
	cfg := CLIConfig{
		Lib: "nonexistent-lib-path-so-no-metadata-can-be-found",
	}

	got := cfg.SelectBackend()
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		if got != ds4.BackendMetal {
			t.Errorf("expected Metal backend on macOS arm64, got %v", got)
		}
	} else if runtime.GOOS != "linux" {
		// on non-linux non-darwin, it should be CPU reference backend.
		if got != ds4.BackendCPU {
			t.Errorf("expected CPU backend on non-darwin non-linux platform, got %v", got)
		}
	} else {
		// On Linux it could be CUDA or CPU depending on the host '/dev/nvidiactl' etc.
		// So we just verify it returns a valid backend.
		if got != ds4.BackendCUDA && got != ds4.BackendCPU {
			t.Errorf("expected CUDA or CPU on Linux, got %v", got)
		}
	}
}
