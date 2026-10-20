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

// DeepSeek V4.1 takes its own DSpark drafter through MTPPath, but libds4
// rejects the V4 Flash drafter for it ("families differ"), so the root policy
// keeps an explicit path and never fills in the installed default, whether
// the model is named by its catalog file or through the active-model hard link.
func TestApplyMTPDefaultsKeepsExplicitMTPForDeepSeek41(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mtp, _ := models.Lookup(models.MTPAlias)
	if err := os.WriteFile(filepath.Join(modelsDir, mtp.FileName), []byte("fake-mtp"), 0o644); err != nil {
		t.Fatal(err)
	}
	if DefaultMTPPath() == "" {
		t.Fatal("DefaultMTPPath() = empty, want the installed drafter")
	}

	v41, ok := models.Lookup("v41-q2-0912")
	if !ok {
		t.Fatal("missing v41-q2-0912 catalog entry")
	}
	file := filepath.Join(modelsDir, v41.FileName)
	if err := os.WriteFile(file, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(modelsDir, models.DefaultModelSymlink)
	if err := os.Link(file, link); err != nil {
		t.Fatal(err)
	}
	const drafter = "/models/DeepSeek-V4.1-Flash-DSpark.gguf"
	for _, path := range []string{file, link} {
		opts := EngineOptions{ModelPath: path}
		ApplyMTPDefaults(&opts)
		if opts.MTPPath != "" {
			t.Errorf("ApplyMTPDefaults(%q).MTPPath = %q, want no default drafter for V4.1", path, opts.MTPPath)
		}
		opts = EngineOptions{ModelPath: path, MTPPath: drafter}
		ApplyMTPDefaults(&opts)
		if opts.MTPPath != drafter {
			t.Errorf("ApplyMTPDefaults(%q).MTPPath = %q, want the explicit %q", path, opts.MTPPath, drafter)
		}
		// NewEngine passes an explicit V4.1 drafter through unchanged.
		if externalMTPUnsupported(path) {
			t.Errorf("externalMTPUnsupported(%q) = true, want false", path)
		}
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

// With no home directory and no DS4_DIR, the data directory falls back to a
// working-directory-relative ".ds4", but the library search must never
// consider it: a libds4 under CWD/.ds4/lib is the binary-planting vector the
// loader's bare-name refusal cannot catch (the path has a directory
// component).
func TestDefaultLibraryPathSkipsRelativeFallbackWithoutHome(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("HOME", "")
	t.Setenv("DS4_DIR", "")
	t.Setenv("DS4_LIB", "")

	name := libraryFileName()
	planted := filepath.Join(cwd, ".ds4", "lib", name)
	if err := os.MkdirAll(filepath.Dir(planted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planted, []byte("malicious"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := DefaultLibraryPath(); got != "" {
		t.Fatalf("DefaultLibraryPath() = %q, want \"\" (a working-directory-relative .ds4/lib must not be searched)", got)
	}
	if got := DefaultLibraryDir(); got != "" {
		t.Fatalf("DefaultLibraryDir() = %q, want \"\" when no absolute data directory resolves", got)
	}
	for _, candidate := range defaultLibraryCandidates() {
		if !filepath.IsAbs(candidate) {
			t.Errorf("library candidate %q is relative to the working directory", candidate)
		}
	}
}

// DefaultDir keeps its documented ".ds4" fallback for data (models, config,
// scratch) when neither DS4_DIR nor a home directory is available. It must not
// return "" (filepath.Abs("") is the working directory, which would widen the
// safe domain checked by LibraryHolders/EngineHolders to the whole CWD) and
// must not move to a shared location such as os.TempDir, where another local
// user could pre-create the directory.
func TestDefaultDirFallbackWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("DS4_DIR", "")

	if got := DefaultDir(); got != ".ds4" {
		t.Fatalf("DefaultDir() = %q, want \".ds4\"", got)
	}
	if got := DefaultModelsDir(); got != filepath.Join(".ds4", "models") {
		t.Fatalf("DefaultModelsDir() = %q, want .ds4/models", got)
	}
	if dir, ok := resolveDefaultDir(); ok || dir != "" {
		t.Fatalf("resolveDefaultDir() = (%q, %v), want (\"\", false)", dir, ok)
	}
	t.Setenv("DS4_DIR", "/tmp/example-ds4")
	if dir, ok := resolveDefaultDir(); !ok || dir != "/tmp/example-ds4" {
		t.Fatalf("resolveDefaultDir() = (%q, %v), want (DS4_DIR, true)", dir, ok)
	}
}
