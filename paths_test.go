package ds4

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NimbleMarkets/ds4go/internal/models"
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
	want := filepath.Join(dir, "models", models.DefaultModelSymlink)
	if got := DefaultModelPath(); got != want {
		t.Fatalf("DefaultModelPath() = %q, want %q", got, want)
	}
}

func TestDefaultMTPPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)

	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without the file present, DefaultMTPPath returns empty.
	if got := DefaultMTPPath(); got != "" {
		t.Fatalf("DefaultMTPPath() = %q, want empty", got)
	}

	// With the file present, DefaultMTPPath returns the path.
	model, _ := models.Lookup(models.MTPAlias)
	want := filepath.Join(modelsDir, model.FileName)
	if err := os.WriteFile(want, []byte("fake-mtp"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DefaultMTPPath(); got != want {
		t.Fatalf("DefaultMTPPath() = %q, want %q", got, want)
	}
}

func TestApplyMTPDefaultsSuppressesExternalMTPForGLM(t *testing.T) {
	glm, ok := models.Lookup("glm-q2")
	if !ok {
		t.Fatal("missing glm-q2 catalog entry")
	}
	opts := EngineOptions{ModelPath: glm.FileName, MTPPath: "/models/deepseek-mtp.gguf"}
	ApplyMTPDefaults(&opts)
	if opts.MTPPath != "" {
		t.Errorf("MTPPath = %q, want empty for GLM", opts.MTPPath)
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

	// Nothing is installed, so the answer is "not found": a bare library
	// name would make the OS loader search the working directory on macOS
	// and Windows, which is the same planting vector by another route.
	if got := DefaultLibraryPath(); got != "" {
		t.Fatalf("DefaultLibraryPath() = %q, want \"\" (a bare name or a working-directory library hands the CWD to the OS loader)", got)
	}
}

func TestApplyVisionDefaultsPairsInstalledEncoder(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var vision, encoder models.Model
	for _, m := range models.Curated() {
		switch m.Alias {
		case "vision-q2":
			vision = m
		case "vision-encoder":
			encoder = m
		}
	}
	modelPath := filepath.Join(modelsDir, vision.FileName)
	encoderPath := filepath.Join(modelsDir, encoder.FileName)
	for _, p := range []string{modelPath, encoderPath} {
		if err := os.WriteFile(p, []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts := EngineOptions{ModelPath: modelPath}
	ApplyVisionDefaults(&opts)
	if opts.VisionPath != encoderPath {
		t.Fatalf("VisionPath = %q, want the installed encoder %q", opts.VisionPath, encoderPath)
	}
	// Explicit path wins.
	opts = EngineOptions{ModelPath: modelPath, VisionPath: "/elsewhere/enc.gguf"}
	ApplyVisionDefaults(&opts)
	if opts.VisionPath != "/elsewhere/enc.gguf" {
		t.Errorf("explicit VisionPath was overridden: %q", opts.VisionPath)
	}
	// Encoder not installed: nothing is paired.
	if err := os.Remove(encoderPath); err != nil {
		t.Fatal(err)
	}
	opts = EngineOptions{ModelPath: modelPath}
	ApplyVisionDefaults(&opts)
	if opts.VisionPath != "" {
		t.Errorf("VisionPath = %q with no encoder installed, want empty", opts.VisionPath)
	}
	// A text-only model never pairs.
	opts = EngineOptions{ModelPath: filepath.Join(modelsDir, "DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix.gguf")}
	ApplyVisionDefaults(&opts)
	if opts.VisionPath != "" {
		t.Errorf("text-only model paired an encoder: %q", opts.VisionPath)
	}
}

func TestApplyMTPDefaultsPicksVisionExpDrafter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var vision, drafter, mtp models.Model
	for _, m := range models.Curated() {
		switch m.Alias {
		case "vision-q2":
			vision = m
		case "vision-dspark-support":
			drafter = m
		case "mtp":
			mtp = m
		}
	}
	for _, m := range []models.Model{vision, drafter, mtp} {
		if err := os.WriteFile(filepath.Join(modelsDir, m.FileName), []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	modelPath := filepath.Join(modelsDir, vision.FileName)
	opts := EngineOptions{ModelPath: modelPath}
	ApplyMTPDefaults(&opts)
	if want := filepath.Join(modelsDir, drafter.FileName); opts.MTPPath != want {
		t.Fatalf("MTPPath = %q, want the Vision-Exp drafter %q", opts.MTPPath, want)
	}

	// With the pinned drafter absent, the 0731 MTP model must not be used as a
	// fallback: libds4 rejects it against a Vision-Exp checkpoint, so the
	// engine would fail to open. An empty path stays empty.
	if err := os.Remove(filepath.Join(modelsDir, drafter.FileName)); err != nil {
		t.Fatal(err)
	}
	opts = EngineOptions{ModelPath: modelPath}
	ApplyMTPDefaults(&opts)
	if opts.MTPPath != "" {
		t.Fatalf("MTPPath = %q with the pinned drafter absent, want empty", opts.MTPPath)
	}
}

func TestDSparkSupportPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var vision, drafter, glm, text models.Model
	for _, m := range models.Curated() {
		switch m.Alias {
		case "vision-q2":
			vision = m
		case "vision-dspark-support":
			drafter = m
		case "glm53-q2":
			glm = m
		case "q2-imatrix":
			text = m
		}
	}
	for _, m := range []models.Model{vision, glm, text} {
		if err := os.WriteFile(filepath.Join(modelsDir, m.FileName), []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	visionPath := filepath.Join(modelsDir, vision.FileName)

	// Required, but not installed.
	if path, required := DSparkSupportPath(visionPath); !required || path != "" {
		t.Errorf("DSparkSupportPath() = (%q, %v), want (\"\", true)", path, required)
	}
	// Required and installed.
	drafterPath := filepath.Join(modelsDir, drafter.FileName)
	if err := os.WriteFile(drafterPath, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path, required := DSparkSupportPath(visionPath); !required || path != drafterPath {
		t.Errorf("DSparkSupportPath() = (%q, %v), want (%q, true)", path, required, drafterPath)
	}
	// GLM and text-only models pin no drafter.
	for _, m := range []models.Model{glm, text} {
		if path, required := DSparkSupportPath(filepath.Join(modelsDir, m.FileName)); required || path != "" {
			t.Errorf("DSparkSupportPath(%s) = (%q, %v), want (\"\", false)", m.Alias, path, required)
		}
	}
	if path, required := DSparkSupportPath(""); required || path != "" {
		t.Errorf("DSparkSupportPath(\"\") = (%q, %v), want (\"\", false)", path, required)
	}
}
