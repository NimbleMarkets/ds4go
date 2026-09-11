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

func TestChatAppendMultimodalMessageFailureLeavesHandlesFreeable(t *testing.T) {
	eng, ctl := visionEngine(t)
	ctl.SetMultimodalAppendFailAt(1)
	tokens, _ := eng.NewTokens(nil)
	defer tokens.Free()
	a, _ := eng.VisionEncodeMemory([]byte("aaaa"))  // 1 row
	b, _ := eng.VisionEncodeMemory([]byte("bbbbb")) // 2 rows
	origA := a.state.c.Data
	before := tokens.Len()
	spans, err := eng.ChatAppendMultimodalMessage(tokens, "user", []string{"x", "y", "z"}, []*VisionEmbedding{a, b})
	if err == nil {
		FreeVisionSpans(spans)
		t.Fatal("append succeeded with the second image set to fail")
	}
	if got := tokens.Len(); got != before {
		t.Errorf("tokens = %d after a failed append, want %d (rewound)", got, before)
	}
	// libds4 replaced a's buffer while appending it and freed the original,
	// then handed the replacement back through the embeddings array. The Go
	// handle must adopt it: pointing at the freed original means Free (or the
	// cleanup) double-frees, and the replacement leaks.
	if a.state.c.Data == nil || a.state.c.Data == origA {
		t.Fatalf("first handle data = %p after the failed append, want the replacement (orig %p)", a.state.c.Data, origA)
	}
	if a.TokenCount() != 1 || b.TokenCount() != 2 {
		t.Errorf("token counts = %d,%d after the failed append, want 1,2", a.TokenCount(), b.TokenCount())
	}
	a.Free()
	b.Free()
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

func multimodalPrompt(t *testing.T, eng *Engine, text string, image []byte) (*Tokens, []VisionSpan) {
	t.Helper()
	tokens, err := eng.NewTokens(nil)
	if err != nil {
		t.Fatal(err)
	}
	emb, err := eng.VisionEncodeMemory(image)
	if err != nil {
		t.Fatal(err)
	}
	spans, err := eng.ChatAppendMultimodalMessage(tokens, "user", []string{text, ""}, []*VisionEmbedding{emb})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { FreeVisionSpans(spans); tokens.Free() })
	return tokens, spans
}

func TestSessionSyncMultimodalRecordsVisionState(t *testing.T) {
	eng, _ := visionEngine(t)
	sess, err := eng.NewSession(256)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if sess.HasVisionState() {
		t.Fatal("fresh session reports vision state")
	}
	tokens, spans := multimodalPrompt(t, eng, "describe", []byte("image-one"))
	if err := sess.SyncMultimodal(tokens, spans); err != nil {
		t.Fatalf("SyncMultimodal: %v", err)
	}
	if !sess.HasVisionState() {
		t.Error("HasVisionState() = false after a multimodal sync")
	}
	if sess.Pos() != tokens.Len() {
		t.Errorf("Pos() = %d, want %d", sess.Pos(), tokens.Len())
	}
	if !sess.VisionPrefixMatches(spans) || !sess.VisionStateMatches(spans) {
		t.Error("the synced spans do not match their own session")
	}
	// A different image at the same offset is not a prefix match.
	_, other := multimodalPrompt(t, eng, "describe", []byte("image-two"))
	if sess.VisionPrefixMatches(other) {
		t.Error("a different image matched the session's prefix")
	}
	// The same image plus a later new one is a prefix match but not a state match.
	longer, more := multimodalPrompt(t, eng, "describe", []byte("image-one"))
	extra, _ := eng.VisionEncodeMemory([]byte("image-three"))
	span2, err := eng.PromptAppendVision(longer, extra)
	if err != nil {
		t.Fatal(err)
	}
	more = append(more, span2)
	if !sess.VisionPrefixMatches(more) {
		t.Error("prefix with a trailing new image did not match")
	}
	if sess.VisionStateMatches(more) {
		t.Error("state matched despite an extra image")
	}
	// A plain sync clears image state.
	if err := sess.Sync([]int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if sess.HasVisionState() {
		t.Error("HasVisionState() = true after a text-only sync")
	}
}

func TestSessionSyncMultimodalRejectsBadSpans(t *testing.T) {
	eng, _ := visionEngine(t)
	sess, _ := eng.NewSession(256)
	defer sess.Close()
	tokens, spans := multimodalPrompt(t, eng, "x", []byte("img"))
	bad := []VisionSpan{{TokenStart: tokens.Len() + 10, Embedding: spans[0].Embedding}}
	if err := sess.SyncMultimodal(tokens, bad); err == nil {
		t.Fatal("accepted a span past the end of the prompt")
	}
}

func TestSessionRebaseVisionState(t *testing.T) {
	eng, _ := visionEngine(t)
	sess, _ := eng.NewSession(256)
	defer sess.Close()
	tokens, spans := multimodalPrompt(t, eng, "look", []byte("same"))
	if err := sess.SyncMultimodal(tokens, spans); err != nil {
		t.Fatal(err)
	}
	_, moved := multimodalPrompt(t, eng, "look at this longer text", []byte("same"))
	if moved[0].TokenStart == spans[0].TokenStart {
		t.Fatal("test setup: expected a different offset")
	}
	if !sess.RebaseVisionState(moved) {
		t.Fatal("RebaseVisionState refused a same-fingerprint span")
	}
	if moved[0].TokenStart != spans[0].TokenStart {
		t.Errorf("TokenStart after rebase = %d, want %d", moved[0].TokenStart, spans[0].TokenStart)
	}
}

func TestSessionRewindSyncedWithSpansUsesMultimodalRebuild(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(EngineOptions{VisionPath: "enc"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, _ := eng.NewSession(256)
	defer sess.Close()
	tokens, spans := multimodalPrompt(t, eng, "look", []byte("img"))
	if err := sess.SyncMultimodal(tokens, spans); err != nil {
		t.Fatal(err)
	}
	if err := sess.Eval(99); err != nil {
		t.Fatal(err)
	}
	if err := sess.RewindSynced(tokens.Len(), spans...); err != nil {
		t.Fatalf("RewindSynced with spans: %v", err)
	}
	if !sess.HasVisionState() {
		t.Error("vision state lost across a span-aware rewind")
	}
	if got := ctl.MultimodalSyncCalls(); got != 2 {
		t.Errorf("multimodal sync calls = %d, want 2 (initial + rebuild)", got)
	}
}

func TestSessionVisionUnsupported(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetVision(true)
	lib.raw.ds4SessionSyncMultimodal = nil
	lib.raw.ds4SessionHasVisionState = nil
	eng, _ := lib.NewEngine(EngineOptions{})
	defer eng.Close()
	sess, _ := eng.NewSession(64)
	defer sess.Close()
	tokens, _ := eng.NewTokens([]int{1})
	defer tokens.Free()
	if err := sess.SyncMultimodal(tokens, []VisionSpan{{}}); !errors.Is(err, ErrVisionNotSupported) {
		t.Fatalf("SyncMultimodal error = %v, want ErrVisionNotSupported", err)
	}
	if sess.HasVisionState() {
		t.Fatal("HasVisionState() = true without the symbol")
	}
	// With no spans, SyncMultimodal is the plain sync and needs no vision symbols.
	if err := sess.SyncMultimodal(tokens, nil); err != nil {
		t.Fatalf("SyncMultimodal(nil spans) = %v, want plain sync", err)
	}
}

// TestSessionVisionMethodsNilSessionSafe guards against a regression where
// VisionPrefixMatches/VisionStateMatches evaluated s.lib.raw.<symbol> in the
// caller's argument list, dereferencing a nil *Session before visionPredicate
// ever reached its nil check.
func TestSessionVisionMethodsNilSessionSafe(t *testing.T) {
	var s *Session
	if s.VisionPrefixMatches(nil) {
		t.Error("VisionPrefixMatches on a nil session = true")
	}
	if s.VisionStateMatches(nil) {
		t.Error("VisionStateMatches on a nil session = true")
	}
	if s.RebaseVisionState(nil) {
		t.Error("RebaseVisionState on a nil session = true")
	}
	if s.HasVisionState() {
		t.Error("HasVisionState on a nil session = true")
	}
}

// TestSessionRewindSyncedKeepsSpanInsideRetainedPrefix covers a rewind whose
// cut point lands after a synced image: the span is entirely inside the
// retained prefix, so it is kept and re-synced with the rebuild.
func TestSessionRewindSyncedKeepsSpanInsideRetainedPrefix(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(EngineOptions{VisionPath: "enc"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, _ := eng.NewSession(256)
	defer sess.Close()
	tokens, spans := multimodalPrompt(t, eng, "look", []byte("img"))
	if err := sess.SyncMultimodal(tokens, spans); err != nil {
		t.Fatal(err)
	}
	imageEnd := spans[0].TokenStart + spans[0].Embedding.TokenCount()
	// Evaluate a few more tokens after the image so there is room to rewind
	// to a position strictly after the image but before the live position.
	for i := 0; i < 3; i++ {
		if err := sess.Eval(int(99 + i)); err != nil {
			t.Fatal(err)
		}
	}
	rewindPos := imageEnd + 1
	if rewindPos >= sess.Pos() {
		t.Fatalf("test setup: rewind position %d not before the live position %d", rewindPos, sess.Pos())
	}
	before := ctl.MultimodalSyncCalls()
	if err := sess.RewindSynced(rewindPos, spans...); err != nil {
		t.Fatalf("RewindSynced: %v", err)
	}
	if !sess.HasVisionState() {
		t.Error("HasVisionState() = false after rewinding past the image")
	}
	if got := ctl.MultimodalSyncCalls(); got != before+1 {
		t.Errorf("multimodal sync calls = %d, want %d (rebuild)", got, before+1)
	}
}

// TestSessionRewindSyncedSplitSpanErrors covers a rewind whose cut point
// lands inside a synced image: RewindSynced must reject it rather than sync a
// prompt with a partial image.
func TestSessionRewindSyncedSplitSpanErrors(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(EngineOptions{VisionPath: "enc"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, _ := eng.NewSession(256)
	defer sess.Close()
	tokens, spans := multimodalPrompt(t, eng, "look", []byte("img"))
	if err := sess.SyncMultimodal(tokens, spans); err != nil {
		t.Fatal(err)
	}
	imageStart := spans[0].TokenStart
	imageEnd := imageStart + spans[0].Embedding.TokenCount()
	splitPos := imageStart + 1
	if splitPos <= imageStart || splitPos >= imageEnd {
		t.Fatalf("test setup: split position %d not inside image span %d..%d", splitPos, imageStart, imageEnd)
	}
	before := ctl.MultimodalSyncCalls()
	err = sess.RewindSynced(splitPos, spans...)
	if err == nil || !strings.Contains(err.Error(), "splits image span") {
		t.Fatalf("RewindSynced at a split position = %v, want an error mentioning \"splits image span\"", err)
	}
	if got := ctl.MultimodalSyncCalls(); got != before {
		t.Errorf("multimodal sync calls = %d, want %d (no rebuild on error)", got, before)
	}
}

// TestSessionRewindSyncedDropsSpanAfterRewind covers a rewind whose cut point
// lands before a synced image: the span is entirely after the retained
// prefix, so it is dropped and the rebuild degrades to a plain sync.
func TestSessionRewindSyncedDropsSpanAfterRewind(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(EngineOptions{VisionPath: "enc"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	sess, _ := eng.NewSession(256)
	defer sess.Close()

	tokens, err := eng.NewTokens([]int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	defer tokens.Free()
	textLen := tokens.Len()
	emb, err := eng.VisionEncodeMemory([]byte("img"))
	if err != nil {
		t.Fatal(err)
	}
	span, err := eng.PromptAppendVision(tokens, emb)
	if err != nil {
		t.Fatal(err)
	}
	defer span.Embedding.Free()

	if err := sess.SyncMultimodal(tokens, []VisionSpan{span}); err != nil {
		t.Fatal(err)
	}
	if !sess.HasVisionState() {
		t.Fatal("HasVisionState() = false after a multimodal sync")
	}
	if span.TokenStart <= textLen {
		t.Fatalf("test setup: image span at %d does not start after the text prefix (%d)", span.TokenStart, textLen)
	}

	before := ctl.MultimodalSyncCalls()
	if err := sess.RewindSynced(textLen, span); err != nil {
		t.Fatalf("RewindSynced: %v", err)
	}
	if got := ctl.MultimodalSyncCalls(); got != before {
		t.Errorf("multimodal sync calls = %d, want %d (plain sync, span dropped)", got, before)
	}
	if sess.HasVisionState() {
		t.Error("HasVisionState() = true after rewinding before the image, with the span dropped")
	}
}
