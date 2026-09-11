package ds4api

import (
	"crypto/sha256"
	"errors"
	"os"
	"strings"
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
	wantFingerprint := emb.Fingerprint()
	wantRows := emb.TokenCount()
	emb.Free()
	if clone.TokenCount() != wantRows {
		t.Errorf("clone lost its token count after the original was freed: got %d, want %d", clone.TokenCount(), wantRows)
	}
	if clone.Fingerprint() != wantFingerprint {
		t.Errorf("clone fingerprint differs: got %x, want %x", clone.Fingerprint(), wantFingerprint)
	}
	clone.Free()
	clone.Free() // double free is a no-op
}

func TestVisionEncodeFileReadsTheFile(t *testing.T) {
	eng, _ := visionEngine(t)
	img := []byte("\x89PNG\r\n\x1a\nfake-image-file-bytes")
	path := t.TempDir() + "/image.png"
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	emb, err := eng.VisionEncodeFile(path)
	if err != nil {
		t.Fatalf("VisionEncodeFile: %v", err)
	}
	defer emb.Free()
	if want := 1 + len(img)%4; emb.TokenCount() != want {
		t.Errorf("TokenCount() = %d, want %d", emb.TokenCount(), want)
	}
	if got, want := emb.Fingerprint(), sha256.Sum256(img); got != want {
		t.Errorf("Fingerprint() = %x, want sha256 of the file bytes", got)
	}

	missingPath := path + ".missing"
	if _, err := eng.VisionEncodeFile(missingPath); err == nil {
		t.Fatal("VisionEncodeFile succeeded on a missing path")
	} else if !strings.Contains(err.Error(), missingPath) {
		t.Errorf("error = %q, want it to mention the missing path", err.Error())
	}
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

func TestChatAppendMultimodalMessageMovesEmbeddingsIntoSpans(t *testing.T) {
	eng, _ := visionEngine(t)
	tokens, err := eng.NewTokens(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Free()
	a, _ := eng.VisionEncodeMemory([]byte("aaaa"))  // 1 row
	b, _ := eng.VisionEncodeMemory([]byte("bbbbb")) // 2 rows
	spans, err := eng.ChatAppendMultimodalMessage(tokens, "user", []string{"look", "and", "compare"}, []*VisionEmbedding{a, b})
	if err != nil {
		t.Fatalf("ChatAppendMultimodalMessage: %v", err)
	}
	defer FreeVisionSpans(spans)
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	if a.TokenCount() != 0 || b.TokenCount() != 0 {
		t.Error("source embeddings were not emptied by the move")
	}
	if spans[0].Embedding.TokenCount() != 1 || spans[1].Embedding.TokenCount() != 2 {
		t.Errorf("span token counts = %d,%d want 1,2", spans[0].Embedding.TokenCount(), spans[1].Embedding.TokenCount())
	}
	ids := tokens.Slice()
	// Placeholders sit exactly at the span offsets.
	for _, sp := range spans {
		for i := 0; i < sp.Embedding.TokenCount(); i++ {
			if ids[sp.TokenStart+i] != int(mockImageToken) {
				t.Fatalf("token %d = %d, want image placeholder", sp.TokenStart+i, ids[sp.TokenStart+i])
			}
		}
	}
	if spans[1].TokenStart <= spans[0].TokenStart {
		t.Errorf("spans out of order: %+v", spans)
	}
}

func TestChatAppendMultimodalMessageRequiresMatchingParts(t *testing.T) {
	eng, _ := visionEngine(t)
	tokens, _ := eng.NewTokens(nil)
	defer tokens.Free()
	a, _ := eng.VisionEncodeMemory([]byte("aaaa"))
	defer a.Free()
	if _, err := eng.ChatAppendMultimodalMessage(tokens, "user", []string{"only one"}, []*VisionEmbedding{a}); err == nil {
		t.Fatal("accepted len(textParts) != len(images)+1")
	}
	if a.TokenCount() != 1 {
		t.Fatal("embedding was consumed by a rejected call")
	}
	if _, err := eng.ChatAppendMultimodalMessage(tokens, "assistant", []string{"x", "y"}, []*VisionEmbedding{a}); err == nil {
		t.Fatal("accepted an assistant role with images")
	}
}

func TestChatAppendMultimodalMessageTextOnlyDelegates(t *testing.T) {
	eng, _ := visionEngine(t)
	tokens, _ := eng.NewTokens(nil)
	defer tokens.Free()
	spans, err := eng.ChatAppendMultimodalMessage(tokens, "tool", []string{"plain result"}, nil)
	if err != nil || len(spans) != 0 {
		t.Fatalf("text-only call: spans=%v err=%v", spans, err)
	}
	plain, _ := eng.NewTokens(nil)
	defer plain.Free()
	if err := eng.ChatAppendMessage(plain, "tool", "plain result"); err != nil {
		t.Fatal(err)
	}
	if a, b := tokens.Slice(), plain.Slice(); len(a) != len(b) {
		t.Fatalf("text-only multimodal append (%d tokens) differs from ChatAppendMessage (%d)", len(a), len(b))
	}
}

func TestPromptAppendVision(t *testing.T) {
	eng, _ := visionEngine(t)
	tokens, _ := eng.NewTokens(nil)
	defer tokens.Free()
	tokens.Push(5)
	emb, _ := eng.VisionEncodeMemory([]byte("bbbbb")) // 2 rows
	span, err := eng.PromptAppendVision(tokens, emb)
	if err != nil {
		t.Fatalf("PromptAppendVision: %v", err)
	}
	defer span.Embedding.Free()
	if emb.TokenCount() != 0 {
		t.Error("embedding not moved")
	}
	// Mock layout: start marker, rows of placeholders, end marker.
	if span.TokenStart != 2 {
		t.Errorf("TokenStart = %d, want 2 (after the pushed token and the start marker)", span.TokenStart)
	}
	if got := tokens.Len(); got != 1+1+2+1 {
		t.Errorf("tokens = %d, want 5", got)
	}
}
