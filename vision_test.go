package ds4

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/dsml"
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

func TestImageEncoderSetLimitsKeepsEntriesWithinTheNewLimit(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	one, _ := enc.Encode(ImageInput{Data: []byte("one")})
	one.Free()
	two, _ := enc.Encode(ImageInput{Data: []byte("two")})
	two.Free()
	// Resizing to exactly the current occupancy evicts nothing: the limit is
	// what the cache may hold, not one less.
	enc.SetLimits(2, DefaultImageCacheBytes)
	if entries, _ := enc.Stats(); entries != 2 {
		t.Fatalf("entries = %d after SetLimits(2, ...) on a two-entry cache, want 2", entries)
	}
	if !enc.cached([]byte("one")) || !enc.cached([]byte("two")) {
		t.Errorf("an entry was evicted by a resize to the current size: one=%v two=%v", enc.cached([]byte("one")), enc.cached([]byte("two")))
	}
}

func TestImageEncoderZeroEntryLimitCachesNothing(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	enc.SetLimits(0, DefaultImageCacheBytes)
	emb, err := enc.Encode(ImageInput{Data: []byte("one")})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if emb.TokenCount() == 0 {
		t.Fatal("Encode returned an empty embedding")
	}
	emb.Free()
	// Eviction makes room for the incoming entry, but a zero limit means the
	// cache may hold nothing at all, so the entry must not be inserted.
	if entries, bytes := enc.Stats(); entries != 0 || bytes != 0 {
		t.Fatalf("cache holds %d entries / %d bytes after SetLimits(0, ...), want none", entries, bytes)
	}
	if enc.cached([]byte("one")) {
		t.Error("image was cached under a zero entry limit")
	}
}

// TestImageEncoderSetLimitsRacesEncode runs SetLimits against concurrent
// Encodes; run with -race, the assertion is that the limit fields are not
// read outside the mutex that guards them.
func TestImageEncoderSetLimitsRacesEncode(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			emb, err := enc.Encode(ImageInput{Data: []byte{byte('a' + i%7), 'x'}})
			if err != nil {
				t.Errorf("Encode: %v", err)
				return
			}
			emb.Free()
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			enc.SetLimits(1+i%4, int64(64+i)<<10)
		}(i)
	}
	wg.Wait()
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

func TestMessagePartsSplitsTextAroundImages(t *testing.T) {
	msg := ChatMessage{Role: "user", Parts: []ContentPart{
		{Text: "first "}, {Image: &ImageInput{Data: []byte("a")}}, {Text: "middle"},
		{Image: &ImageInput{Data: []byte("b")}},
	}}
	texts, images, err := messageParts(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || len(texts) != 3 {
		t.Fatalf("texts=%v images=%d, want 3 texts and 2 images", texts, len(images))
	}
	if texts[0] != "first " || texts[1] != "middle" || texts[2] != "" {
		t.Errorf("texts = %q", texts)
	}
	// Adjacent text parts merge; a message with no images has one text.
	texts, images, _ = messageParts(ChatMessage{Role: "user", Parts: []ContentPart{{Text: "a"}, {Text: "b"}}})
	if len(images) != 0 || len(texts) != 1 || texts[0] != "ab" {
		t.Errorf("text-only parts: texts=%q images=%d", texts, len(images))
	}
	if _, _, err := messageParts(ChatMessage{Role: "assistant", Parts: []ContentPart{{Image: &ImageInput{Data: []byte("a")}}}}); err == nil {
		t.Error("assistant message with an image was accepted")
	}
	if _, _, err := messageParts(ChatMessage{Role: "user", Parts: []ContentPart{{}}}); err == nil {
		t.Error("empty part was accepted")
	}
}

func TestBuildChatPromptMultimodalProducesSpans(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	history := []ChatMessage{{Role: "user", Parts: []ContentPart{
		{Text: "What is in "}, {Image: &ImageInput{Data: []byte("first")}}, {Text: " and "}, {Image: &ImageInput{Data: []byte("second-image")}}, {Text: "?"},
	}}}
	p, err := BuildChatPromptMultimodal(eng, enc, "sys", nil, history, ThinkNone)
	if err != nil {
		t.Fatalf("BuildChatPromptMultimodal: %v", err)
	}
	defer p.Free()
	if len(p.Images) != 2 {
		t.Fatalf("got %d spans, want 2", len(p.Images))
	}
	ids := p.Tokens.Slice()
	for i, sp := range p.Images {
		if sp.TokenStart+sp.Embedding.TokenCount() > len(ids) {
			t.Fatalf("span %d exceeds the prompt", i)
		}
		if i > 0 && sp.TokenStart <= p.Images[i-1].TokenStart {
			t.Fatalf("spans not ascending: %+v", p.Images)
		}
	}
	// The same history rendered again yields the same offsets and
	// fingerprints (cache hit, stable layout), which is what lets a session
	// reuse its image prefix across tool-loop rounds.
	q, err := BuildChatPromptMultimodal(eng, enc, "sys", nil, history, ThinkNone)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Free()
	for i := range p.Images {
		if p.Images[i].TokenStart != q.Images[i].TokenStart || p.Images[i].Embedding.Fingerprint() != q.Images[i].Embedding.Fingerprint() {
			t.Errorf("span %d differs between renders", i)
		}
	}
	if entries, _ := enc.Stats(); entries != 2 {
		t.Errorf("cache entries = %d, want 2", entries)
	}
}

func TestBuildChatPromptMultimodalTextOnlyMatchesBuildChatPrompt(t *testing.T) {
	eng, _ := visionMockEngine(t)
	history := []ChatMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}, {Role: "user", Content: "again"}}
	plain, err := BuildChatPrompt(eng, "sys", nil, history, ThinkHigh)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Free()
	multi, err := BuildChatPromptMultimodal(eng, nil, "sys", nil, history, ThinkHigh)
	if err != nil {
		t.Fatal(err)
	}
	defer multi.Free()
	if a, b := plain.Slice(), multi.Tokens.Slice(); len(a) != len(b) {
		t.Fatalf("text-only multimodal prompt has %d tokens, plain has %d", len(b), len(a))
	} else {
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("token %d differs", i)
			}
		}
	}
	if len(multi.Images) != 0 {
		t.Error("text-only prompt produced spans")
	}
}

func TestBuildPromptRejectsImageParts(t *testing.T) {
	eng, _ := visionMockEngine(t)
	history := []ChatMessage{{Role: "user", Parts: []ContentPart{{Image: &ImageInput{Data: []byte("x")}}, {Text: "?"}}}}
	if _, err := BuildChatPrompt(eng, "", nil, history, ThinkNone); !errors.Is(err, ErrImagesNeedMultimodalPrompt) {
		t.Fatalf("BuildChatPrompt error = %v, want ErrImagesNeedMultimodalPrompt", err)
	}
	if _, err := NewToolRegistry().BuildPrompt(eng, "", history, ThinkNone); !errors.Is(err, ErrImagesNeedMultimodalPrompt) {
		t.Fatalf("BuildPrompt error = %v, want ErrImagesNeedMultimodalPrompt", err)
	}
	if _, err := BuildChatPromptMultimodal(eng, nil, "", nil, history, ThinkNone); err == nil {
		t.Fatal("multimodal build with a nil encoder and images succeeded")
	}
}

func TestToolMessageWithImageRendersAsUserTurn(t *testing.T) {
	eng, ctl := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	for _, glm := range []bool{false, true} {
		ctl.SetGLM(glm)
		history := []ChatMessage{
			{Role: "user", Content: "view it"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "view_image", Arguments: `{"path":"a.png"}`}}},
			{Role: "tool", ToolCallID: "c1", Parts: []ContentPart{{Text: "[tool:view_image] a.png\n"}, {Image: &ImageInput{Data: []byte("pixels")}}}},
		}
		p, err := NewToolRegistry().BuildPromptMultimodal(eng, enc, "", history, ThinkNone)
		if err != nil {
			t.Fatalf("glm=%v: %v", glm, err)
		}
		if len(p.Images) != 1 {
			t.Errorf("glm=%v: got %d spans, want 1", glm, len(p.Images))
		}
		p.Free()
	}
	// A text-only tool message is unchanged (still coalesced/wrapped as before).
	ctl.SetGLM(false)
	history := []ChatMessage{
		{Role: "user", Content: "x"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "t", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "c1", Content: "result"},
	}
	plain, _ := NewToolRegistry().BuildPrompt(eng, "", history, ThinkNone)
	defer plain.Free()
	multi, _ := NewToolRegistry().BuildPromptMultimodal(eng, enc, "", history, ThinkNone)
	defer multi.Free()
	if a, b := plain.Slice(), multi.Tokens.Slice(); len(a) != len(b) {
		t.Errorf("text-only tool turn: multimodal %d tokens vs plain %d", len(b), len(a))
	}
}

// TestRenderChatMessageImageToolTurn asserts the central role/wrapper rule
// directly: an image-bearing tool message always renders under the "user"
// role, DSML wraps its text segments in the <tool_result> markers (spanning
// the whole payload, not each segment), and GLM leaves the text unwrapped.
func TestRenderChatMessageImageToolTurn(t *testing.T) {
	const start, end = "<tool_result>", "</tool_result>"
	msg := ChatMessage{Role: "tool", ToolCallID: "c1", Parts: []ContentPart{
		{Text: "obs "}, {Image: &ImageInput{Data: []byte("pixels")}}, {Text: " tail"},
	}}

	dsmlRendered, err := renderChatMessage(msg, turnRenderInfo{syntax: dsml.SyntaxDSML})
	if err != nil {
		t.Fatal(err)
	}
	if dsmlRendered.role != "user" {
		t.Errorf("DSML role = %q, want %q", dsmlRendered.role, "user")
	}
	if len(dsmlRendered.images) != 1 {
		t.Fatalf("DSML images = %d, want 1", len(dsmlRendered.images))
	}
	if len(dsmlRendered.parts) == 0 || !strings.HasPrefix(dsmlRendered.parts[0], start) {
		t.Errorf("DSML parts[0] = %q, want prefix %q", dsmlRendered.parts, start)
	}
	if last := dsmlRendered.parts[len(dsmlRendered.parts)-1]; !strings.HasSuffix(last, end) {
		t.Errorf("DSML parts[last] = %q, want suffix %q", last, end)
	}

	glmRendered, err := renderChatMessage(msg, turnRenderInfo{syntax: dsml.SyntaxGLM})
	if err != nil {
		t.Fatal(err)
	}
	if glmRendered.role != "user" {
		t.Errorf("GLM role = %q, want %q", glmRendered.role, "user")
	}
	if len(glmRendered.images) != 1 {
		t.Fatalf("GLM images = %d, want 1", len(glmRendered.images))
	}
	want := []string{"obs ", " tail"}
	if len(glmRendered.parts) != len(want) || glmRendered.parts[0] != want[0] || glmRendered.parts[1] != want[1] {
		t.Errorf("GLM parts = %q, want %q", glmRendered.parts, want)
	}
}

// TestBuildPromptMultimodalRejectsAssistantImage guards against
// ToolRegistry.renderMessage's assistant short-circuit silently dropping an
// image on an assistant message instead of surfacing messageParts' error.
func TestBuildPromptMultimodalRejectsAssistantImage(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	history := []ChatMessage{
		{Role: "user", Content: "x"},
		{Role: "assistant", Parts: []ContentPart{{Image: &ImageInput{Data: []byte("x")}}}},
	}
	if _, err := NewToolRegistry().BuildPromptMultimodal(eng, enc, "", history, ThinkNone); err == nil {
		t.Fatal("BuildPromptMultimodal accepted an assistant message with an image")
	}
}

// TestImageEncoderConcurrentHitsAndEvictions exercises the race between a
// cache hit's Clone and a concurrent eviction freeing that same entry. With
// only one cache slot, every other Encode of the alternate image evicts the
// one just inserted, so hits and evictions interleave constantly. Run with
// -race; the assertion is only that nothing panics or races, and every
// embedding handed back is valid (TokenCount() > 0) before it is freed.
func TestImageEncoderConcurrentHitsAndEvictions(t *testing.T) {
	eng, _ := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	enc.SetLimits(1, DefaultImageCacheBytes)

	images := [][]byte{[]byte("alpha-image"), []byte("beta-image")}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			img := ImageInput{Data: images[i%len(images)]}
			emb, err := enc.Encode(img)
			if err != nil {
				t.Errorf("Encode: %v", err)
				return
			}
			defer emb.Free()
			if emb.TokenCount() == 0 {
				t.Error("Encode returned an embedding with no tokens")
			}
		}(i)
	}
	wg.Wait()
}

func TestGeneratePromptSyncsMultimodalOnlyWithSpans(t *testing.T) {
	eng, ctl := visionMockEngine(t)
	enc := NewImageEncoder(eng)
	sess, err := eng.NewSession(256)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	g := Generator{Engine: eng, Session: sess}

	text, err := BuildChatPromptMultimodal(eng, enc, "", nil, []ChatMessage{{Role: "user", Content: "hi"}}, ThinkNone)
	if err != nil {
		t.Fatal(err)
	}
	defer text.Free()
	if _, err := g.GeneratePrompt(text, GenerateOptions{MaxTokens: 3}); err != nil {
		t.Fatalf("GeneratePrompt(text): %v", err)
	}
	if ctl.MultimodalSyncCalls() != 0 || sess.HasVisionState() {
		t.Fatal("text-only prompt used the multimodal sync")
	}

	withImage, err := BuildChatPromptMultimodal(eng, enc, "", nil, []ChatMessage{{Role: "user", Parts: []ContentPart{{Text: "see"}, {Image: &ImageInput{Data: []byte("img")}}}}}, ThinkNone)
	if err != nil {
		t.Fatal(err)
	}
	defer withImage.Free()
	out, err := g.GeneratePrompt(withImage, GenerateOptions{MaxTokens: 3})
	if err != nil {
		t.Fatalf("GeneratePrompt(image): %v", err)
	}
	if len(out) != 3 {
		t.Errorf("generated %d tokens, want 3", len(out))
	}
	if ctl.MultimodalSyncCalls() != 1 || !sess.HasVisionState() {
		t.Error("image prompt did not go through the multimodal sync")
	}
	if got, want := sess.Pos(), withImage.Tokens.Len()+3; got != want {
		t.Errorf("Pos() = %d, want %d", got, want)
	}
}

func TestGeneratePromptRewindsWithSpans(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4api.EngineOptions{VisionPath: "enc", MTPPath: "mtp.gguf", MTPDraftTokens: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	enc := NewImageEncoder(eng)
	sess, _ := eng.NewSession(256)
	defer sess.Close()
	p, err := BuildChatPromptMultimodal(eng, enc, "", nil, []ChatMessage{{Role: "user", Parts: []ContentPart{{Text: "see"}, {Image: &ImageInput{Data: []byte("img")}}}}}, ThinkNone)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Free()
	// Make the third generated token a stop so the speculative block is cut
	// and the rewind path runs with the prompt's spans.
	if err := sess.SyncMultimodal(p.Tokens, p.Images); err != nil {
		t.Fatal(err)
	}
	first := sess.Argmax()
	ctl.SetStopTokens(first + 2)
	out, err := (Generator{Engine: eng, Session: sess}).GeneratePrompt(p, GenerateOptions{MaxTokens: 8, StopOnEOS: true})
	if err != nil {
		t.Fatalf("GeneratePrompt: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("generated %v, want two tokens before the stop", out)
	}
	if !sess.HasVisionState() {
		t.Error("vision state lost across the stop rewind: RewindSynced ran without the spans")
	}
	if got := ctl.MultimodalSyncCalls(); got < 2 {
		t.Errorf("multimodal sync calls = %d, want the rebuild to use the multimodal path", got)
	}
}

func TestToolLoopRunsWithImageHistory(t *testing.T) {
	eng, ctl := visionMockEngine(t)
	ctl.SetStopTokens(int(eng.TokenEOS()))
	sess, _ := eng.NewSession(512)
	defer sess.Close()
	loop := ToolLoop{Engine: eng, Session: sess, Tools: NewToolRegistry(), Images: NewImageEncoder(eng)}
	res, err := loop.Run(ToolLoopOptions{
		History:  []ChatMessage{{Role: "user", Parts: []ContentPart{{Text: "describe"}, {Image: &ImageInput{Data: []byte("photo")}}}}},
		Generate: GenerateOptions{MaxTokens: 4, StopOnEOS: true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Assistant.Role != "assistant" {
		t.Fatalf("assistant = %+v", res.Assistant)
	}
	if ctl.MultimodalSyncCalls() == 0 || !sess.HasVisionState() {
		t.Error("the loop did not sync the image prompt through the multimodal path")
	}
}

func TestToolLoopWithoutEncoderRejectsImages(t *testing.T) {
	eng, _ := visionMockEngine(t)
	sess, _ := eng.NewSession(256)
	defer sess.Close()
	loop := ToolLoop{Engine: eng, Session: sess, Tools: NewToolRegistry()}
	_, err := loop.Run(ToolLoopOptions{History: []ChatMessage{{Role: "user", Parts: []ContentPart{{Image: &ImageInput{Data: []byte("p")}}, {Text: "?"}}}}})
	if err == nil {
		t.Fatal("Run accepted image history without an ImageEncoder")
	}
}

func TestToolLoopCompleteFuncCannotCarryImages(t *testing.T) {
	eng, _ := visionMockEngine(t)
	sess, _ := eng.NewSession(256)
	defer sess.Close()
	loop := ToolLoop{Engine: eng, Session: sess, Tools: NewToolRegistry(), Images: NewImageEncoder(eng),
		CompleteFunc: func(*Tokens, GenerateOptions) (string, error) { return "ok", nil }}
	_, err := loop.Run(ToolLoopOptions{History: []ChatMessage{{Role: "user", Parts: []ContentPart{{Image: &ImageInput{Data: []byte("p")}}, {Text: "?"}}}}})
	if !errors.Is(err, ErrCompleteFuncCannotCarryImages) {
		t.Fatalf("error = %v, want ErrCompleteFuncCannotCarryImages", err)
	}
	// Text-only history still works through CompleteFunc.
	res, err := loop.Run(ToolLoopOptions{History: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil || res.Assistant.Content != "ok" {
		t.Fatalf("text-only CompleteFunc run: %+v, %v", res.Assistant, err)
	}
}
