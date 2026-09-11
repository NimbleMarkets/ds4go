package ds4api

import (
	"crypto/sha256"
	"errors"
	"testing"
)

func visionEngine(t *testing.T) (*Engine, *MockControls) {
	t.Helper()
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(EngineOptions{VisionPath: "encoder.gguf"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(eng.Close)
	return eng, ctl
}

func TestVisionEncodeMemoryProducesEmbedding(t *testing.T) {
	eng, _ := visionEngine(t)
	if !eng.HasVision() {
		t.Fatal("HasVision() = false with an encoder configured")
	}
	if got := eng.EmbdDim(); got != mockEmbdDim {
		t.Fatalf("EmbdDim() = %d, want %d", got, mockEmbdDim)
	}
	img := []byte("\x89PNG\r\n\x1a\nfake-image-bytes")
	emb, err := eng.VisionEncodeMemory(img)
	if err != nil {
		t.Fatalf("VisionEncodeMemory: %v", err)
	}
	defer emb.Free()
	if want := 1 + len(img)%4; emb.TokenCount() != want {
		t.Errorf("TokenCount() = %d, want %d", emb.TokenCount(), want)
	}
	if got, want := emb.Fingerprint(), sha256.Sum256(img); got != want {
		t.Errorf("Fingerprint() = %x, want sha256 of the bytes", got)
	}
}

func TestVisionEncodeWithoutEncoderErrors(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetVision(false)
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if eng.HasVision() {
		t.Fatal("HasVision() = true without an encoder")
	}
	if _, err := eng.VisionEncodeMemory([]byte("x")); err == nil {
		t.Fatal("VisionEncodeMemory succeeded without an encoder")
	}
}

func TestVisionCloneIsIndependent(t *testing.T) {
	eng, _ := visionEngine(t)
	emb, err := eng.VisionEncodeMemory([]byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	clone, err := emb.Clone()
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	emb.Free()
	if clone.TokenCount() != 1+len("abcdef")%4 {
		t.Errorf("clone lost its token count after the original was freed")
	}
	if clone.Fingerprint() != emb.Fingerprint() && emb.TokenCount() != 0 {
		t.Errorf("clone fingerprint differs")
	}
	clone.Free()
	clone.Free() // double free is a no-op
}

func TestVisionUnsupportedLibrary(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetVision(true)
	lib.raw.ds4EngineVisionEncodeMemory = nil
	lib.raw.ds4EngineVisionEncodeFile = nil
	if lib.SupportsVision() {
		t.Fatal("SupportsVision() = true without the symbols")
	}
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if _, err := eng.VisionEncodeMemory([]byte("x")); !errors.Is(err, ErrVisionNotSupported) {
		t.Fatalf("error = %v, want ErrVisionNotSupported", err)
	}
	if _, err := eng.VisionEncodeFile("x.png"); !errors.Is(err, ErrVisionNotSupported) {
		t.Fatalf("error = %v, want ErrVisionNotSupported", err)
	}
}
