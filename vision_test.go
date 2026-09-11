package ds4

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

func visionMockEngine(t *testing.T) (*Engine, *ds4api.MockControls) {
	t.Helper()
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4api.EngineOptions{VisionPath: "enc.gguf"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(eng.Close)
	return eng, ctl
}

func TestImageEncoderCachesByBytes(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	img := ImageInput{Data: []byte("same-image-bytes")}
	a, err := enc.Encode(img)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	b, err := enc.Encode(img)
	if err != nil {
		t.Fatalf("Encode (cached): %v", err)
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("cached encode returned a different image")
	}
	// Each call hands out an independent clone: freeing one leaves the other.
	a.Free()
	if b.TokenCount() == 0 {
		t.Fatal("freeing one result emptied the other")
	}
	b.Free()
	if entries, _ := enc.Stats(); entries != 1 {
		t.Errorf("cache entries = %d, want 1", entries)
	}
}

func TestImageEncoderReadsPathOnce(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	path := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(path, []byte("png-bytes-here"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := enc.Encode(ImageInput{Path: path})
	if err != nil {
		t.Fatalf("Encode(path): %v", err)
	}
	defer a.Free()
	b, err := enc.Encode(ImageInput{Data: []byte("png-bytes-here")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Free()
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("path and bytes of the same image did not share a cache entry")
	}
	if entries, _ := enc.Stats(); entries != 1 {
		t.Errorf("cache entries = %d, want 1", entries)
	}
	if _, err := enc.Encode(ImageInput{}); err == nil {
		t.Error("Encode accepted an empty ImageInput")
	}
	if _, err := enc.Encode(ImageInput{Data: []byte("x"), Path: path}); err == nil {
		t.Error("Encode accepted both Data and Path")
	}
}

func TestImageEncoderEvictsLeastRecentlyUsed(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	enc.SetLimits(2, DefaultImageCacheBytes)
	one, _ := enc.Encode(ImageInput{Data: []byte("one")})
	one.Free()
	two, _ := enc.Encode(ImageInput{Data: []byte("two")})
	two.Free()
	again, _ := enc.Encode(ImageInput{Data: []byte("one")}) // touch "one"
	again.Free()
	three, _ := enc.Encode(ImageInput{Data: []byte("three")}) // evicts "two"
	three.Free()
	if entries, _ := enc.Stats(); entries != 2 {
		t.Fatalf("entries = %d, want 2", entries)
	}
	if !enc.cached([]byte("one")) || enc.cached([]byte("two")) || !enc.cached([]byte("three")) {
		t.Errorf("wrong entry evicted: one=%v two=%v three=%v", enc.cached([]byte("one")), enc.cached([]byte("two")), enc.cached([]byte("three")))
	}
}

func TestImageEncoderByteBudget(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	// One mock row is 8 floats = 32 bytes; mock rows = 1 + len(bytes)%4, so
	// "abcdef" (6 bytes, 3 rows) is 96 bytes.
	enc.SetLimits(DefaultImageCacheEntries, 100)
	a, _ := enc.Encode(ImageInput{Data: []byte("abcdef")})
	a.Free()
	b, _ := enc.Encode(ImageInput{Data: []byte("abcdefg")}) // 4 rows = 128 bytes: too big to cache
	b.Free()
	entries, bytes := enc.Stats()
	if entries != 1 || bytes != 96 {
		t.Errorf("stats = %d entries / %d bytes, want 1 / 96 (oversized result not cached)", entries, bytes)
	}
}

func TestImageEncoderWithoutVision(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(false)
	eng, _ := lib.NewEngine(ds4api.EngineOptions{})
	defer eng.Close()
	enc := NewImageEncoder(eng)
	if _, err := enc.Encode(ImageInput{Data: []byte("x")}); err == nil {
		t.Fatal("Encode succeeded with no encoder loaded")
	}
}

func TestPromptFreeReleasesSpans(t *testing.T) {
	eng, _ := visionMockEngine(t)
	tokens, _ := eng.NewTokens(nil)
	emb, _ := eng.VisionEncodeMemory([]byte("img"))
	spans, err := eng.ChatAppendMultimodalMessage(tokens, "user", []string{"a", "b"}, []*ds4api.VisionEmbedding{emb})
	if err != nil {
		t.Fatal(err)
	}
	p := &Prompt{Tokens: tokens, Images: spans}
	p.Free()
	if spans[0].Embedding.TokenCount() != 0 {
		t.Error("Prompt.Free did not free the spans")
	}
	p.Free() // idempotent
}
