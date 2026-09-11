package cliopts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/spf13/pflag"
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

// Generation stop detection depends on the think mode the prompt was rendered
// with: with thinking off, a thinking marker ends the turn rather than being
// emitted as content. The sampling options must therefore carry it.
func TestGenerateOptionsCarriesThinkMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  CLIConfig
		want ds4.ThinkMode
	}{
		{"default is high", CLIConfig{}, ds4.ThinkHigh},
		{"nothink", CLIConfig{NoThink: true}, ds4.ThinkNone},
		{"think-max", CLIConfig{ThinkMax: true}, ds4.ThinkMax},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := c.cfg
			if got := cfg.GenerateOptions().ThinkMode; got != c.want {
				t.Errorf("GenerateOptions().ThinkMode = %v, want %v", got, c.want)
			}
			if got := cfg.GenerateOptions().ThinkMode; got != cfg.ThinkMode() {
				t.Errorf("GenerateOptions().ThinkMode = %v, want ThinkMode() = %v", got, cfg.ThinkMode())
			}
		})
	}
}

func TestEngineOptionsCarryContextSizing(t *testing.T) {
	cli := CLIConfig{Ctx: 32768}
	if got := cli.EngineOptions(); got.ContextSize != cli.Ctx || got.PlacementCtxHint != cli.Ctx {
		t.Errorf("CLI EngineOptions context = (%d, %d), want (%d, %d)",
			got.ContextSize, got.PlacementCtxHint, cli.Ctx, cli.Ctx)
	}
	server := ServerConfig{Ctx: 65536}
	if got := server.EngineOptions(); got.ContextSize != server.Ctx || got.PlacementCtxHint != server.Ctx {
		t.Errorf("server EngineOptions context = (%d, %d), want (%d, %d)",
			got.ContextSize, got.PlacementCtxHint, server.Ctx, server.Ctx)
	}
}

func TestEngineOptionsSuppressExternalMTPForGLM(t *testing.T) {
	glm, ok := models.Lookup("glm-q2")
	if !ok {
		t.Fatal("missing glm-q2 catalog entry")
	}
	deepseek, ok := models.Lookup("q2-imatrix")
	if !ok {
		t.Fatal("missing q2-imatrix catalog entry")
	}

	const mtp = "/models/deepseek-mtp.gguf"
	cliGLM := CLIConfig{Model: glm.FileName, MTP: mtp}
	serverGLM := ServerConfig{Model: glm.FileName, MTP: mtp}
	cliDeepSeek := CLIConfig{Model: deepseek.FileName, MTP: mtp}
	serverDeepSeek := ServerConfig{Model: deepseek.FileName, MTP: mtp}
	for _, test := range []struct {
		name string
		got  ds4.EngineOptions
		want string
	}{
		{"CLI GLM", cliGLM.EngineOptions(), ""},
		{"server GLM", serverGLM.EngineOptions(), ""},
		{"CLI DeepSeek", cliDeepSeek.EngineOptions(), mtp},
		{"server DeepSeek", serverDeepSeek.EngineOptions(), mtp},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.got.MTPPath != test.want {
				t.Errorf("MTPPath = %q, want %q", test.got.MTPPath, test.want)
			}
		})
	}
}

func TestVisionFlagMapsToEngineOptions(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	cfg := RegisterCLI(fs)
	if err := fs.Parse([]string{"--vision", "/enc.gguf", "--image", "a.png", "--image", "b.jpg", "-p", "what?"}); err != nil {
		t.Fatal(err)
	}
	opts := cfg.EngineOptions()
	if opts.VisionPath != "/enc.gguf" {
		t.Errorf("VisionPath = %q, want /enc.gguf", opts.VisionPath)
	}
	if got := cfg.Images; len(got) != 2 || got[0] != "a.png" || got[1] != "b.jpg" {
		t.Errorf("Images = %v", got)
	}
	parts := cfg.ImageParts("what?")
	if len(parts) != 3 || parts[0].Text != "what?" || parts[1].Image == nil || parts[1].Image.Path != "a.png" || parts[2].Image.Path != "b.jpg" {
		t.Errorf("ImageParts = %+v", parts)
	}
	if parts := (&CLIConfig{}).ImageParts("plain"); parts != nil {
		t.Errorf("ImageParts without --image = %+v, want nil", parts)
	}
}

func TestVisionFlagPairsFromCatalogWhenUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var vision, encoder models.Model
	for _, m := range models.Curated() {
		switch m.Alias {
		case "glm53-q2":
			vision = m
		case "glm53-vision":
			encoder = m
		}
	}
	for _, m := range []models.Model{vision, encoder} {
		if err := os.WriteFile(filepath.Join(modelsDir, m.FileName), []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	cfg := RegisterCLI(fs)
	if err := fs.Parse([]string{"-m", filepath.Join(modelsDir, vision.FileName)}); err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.EngineOptions().VisionPath, filepath.Join(modelsDir, encoder.FileName); got != want {
		t.Errorf("VisionPath = %q, want paired %q", got, want)
	}
}
