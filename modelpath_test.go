package ds4

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveModelPath(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	if err := os.MkdirAll(DefaultModelsDir(), 0755); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(DefaultModelsDir(), "DeepSeek-V4-Flash-Vision-Exp-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8.gguf")
	if err := os.WriteFile(model, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveModelPath("vision-q2"); got != model {
		t.Fatalf("installed alias = %q, want %q", got, model)
	}
	for _, value := range []string{"", "unknown-alias", "vision-encoder", "./custom.gguf", model} {
		if got := ResolveModelPath(value); got != value {
			t.Errorf("ResolveModelPath(%q) = %q, want unchanged", value, got)
		}
	}
	if err := os.WriteFile(model, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveModelPath("vision-q2"); got != "vision-q2" {
		t.Fatalf("empty model was treated as installed: %q", got)
	}
}
