//go:build ds4_integration

// Real-library vision check. Run with a Vision-Exp or GLM 5.3 model and its
// encoder:
//
//	DS4_LIB=~/.ds4/lib/libds4.dylib DS4_VISION_MODEL=~/.ds4/models/GLM-5.3-Flash-Q2.gguf \
//	DS4_VISION_ENCODER=~/.ds4/models/GLM-5.3-Flash-Vision-Encoder.gguf \
//	  go test -tags ds4_integration -timeout 40m -count=1 -v . -run TestRealLibraryVision

package ds4

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

func solidPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRealLibraryVision(t *testing.T) {
	libPath, model, encoder := os.Getenv("DS4_LIB"), os.Getenv("DS4_VISION_MODEL"), os.Getenv("DS4_VISION_ENCODER")
	if libPath == "" || model == "" || encoder == "" {
		t.Skip("DS4_LIB, DS4_VISION_MODEL, and DS4_VISION_ENCODER must be set")
	}
	lib, err := ds4api.Load(libPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !lib.SupportsVision() {
		t.Fatal("library lacks the vision API")
	}
	backend := ds4api.BackendMetal
	switch os.Getenv("DS4_BACKEND") {
	case "cuda":
		backend = ds4api.BackendCUDA
	case "cpu":
		backend = ds4api.BackendCPU
	}
	eng, err := lib.NewEngine(ds4api.EngineOptions{ModelPath: model, VisionPath: encoder, Backend: backend, ContextSize: 8192})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if !eng.HasVision() {
		t.Fatal("HasVision() = false with an encoder configured")
	}
	enc := NewImageEncoder(eng)
	red := solidPNG(t, color.RGBA{255, 0, 0, 255})

	sess, err := eng.NewSession(8192)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	loop := ToolLoop{Engine: eng, Session: sess, Tools: NewToolRegistry(), Images: enc, ThinkMode: ThinkHigh, Thinking: true}
	res, err := loop.Run(ToolLoopOptions{
		System:    "Answer in one short sentence.",
		History:   []ChatMessage{{Role: "user", Parts: []ContentPart{{Text: "What single color fills this image? "}, {Image: &ImageInput{Data: red}}}}},
		Generate:  GenerateOptions{MaxTokens: 200, StopOnEOS: true},
		MaxRounds: 2,
	})
	if err != nil {
		t.Fatalf("ToolLoop.Run: %v", err)
	}
	t.Logf("answer: %q", res.Assistant.Content)
	if !strings.Contains(strings.ToLower(res.Assistant.Content), "red") {
		t.Errorf("answer %q does not mention red", res.Assistant.Content)
	}
	if !sess.HasVisionState() {
		t.Error("session reports no vision state after an image prompt")
	}
	if entries, _ := enc.Stats(); entries != 1 {
		t.Errorf("cache entries = %d, want 1", entries)
	}

	// A text-only session on the same engine has no vision state.
	plain, err := eng.NewSession(1024)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if err := plain.Sync([]int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if plain.HasVisionState() {
		t.Error("text-only session reports vision state")
	}
}
