package ds4api

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

// ErrVisionNotSupported is returned by the vision entry points when the loaded
// libds4 predates the vision API.
var ErrVisionNotSupported = errors.New("ds4: vision is not supported by the loaded library (missing symbols)")

// VisionEmbedding owns the encoder output for one image: token_count rows of
// ds4_engine_embd_dim floats malloc'd by libds4. Appending it to a prompt
// moves ownership into a VisionSpan and empties this handle.
type VisionEmbedding struct {
	lib     *Library
	state   *visionEmbeddingState
	dim     int
	cleanup runtime.Cleanup
}

type visionEmbeddingState struct{ c cVisionEmbedding }

type visionCleanupArg struct {
	lib   *Library
	state *visionEmbeddingState
}

func cleanVisionEmbedding(arg visionCleanupArg) {
	if arg.state.c.Data != nil && arg.lib.raw.ds4VisionEmbeddingFree != nil {
		arg.lib.raw.ds4VisionEmbeddingFree(&arg.state.c)
	}
}

func visionEmbeddingFromC(lib *Library, c cVisionEmbedding, dim int) *VisionEmbedding {
	state := &visionEmbeddingState{c: c}
	e := &VisionEmbedding{lib: lib, state: state, dim: dim}
	e.cleanup = runtime.AddCleanup(e, cleanVisionEmbedding, visionCleanupArg{lib: lib, state: state})
	return e
}

// TokenCount is the number of image tokens the embedding occupies.
func (e *VisionEmbedding) TokenCount() int {
	if e == nil || e.state == nil {
		return 0
	}
	return int(e.state.c.TokenCount)
}

// Fingerprint identifies the image content; libds4 compares it when deciding
// whether a session's image state can be reused.
func (e *VisionEmbedding) Fingerprint() [32]byte {
	if e == nil || e.state == nil {
		return [32]byte{}
	}
	return e.state.c.Fingerprint
}

// Free releases the embedding. It is a no-op after a move or a prior Free.
func (e *VisionEmbedding) Free() {
	if e == nil || e.state == nil || e.state.c.Data == nil {
		return
	}
	libCallMu.Lock()
	defer libCallMu.Unlock()
	e.lib.raw.ds4VisionEmbeddingFree(&e.state.c)
	e.state.c = cVisionEmbedding{}
}

// take moves the C value out of the handle for a transfer to libds4.
func (e *VisionEmbedding) take() cVisionEmbedding {
	c := e.state.c
	e.state.c = cVisionEmbedding{}
	return c
}

// Clone copies the embedding. The copy is independent: freeing either side
// leaves the other valid.
func (e *VisionEmbedding) Clone() (*VisionEmbedding, error) {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.state == nil || e.state.c.Data == nil {
		return nil, errors.New("ds4: clone of an empty vision embedding")
	}
	if e.state.c.TokenCount == 0 || e.dim <= 0 {
		return nil, errors.New("ds4: vision embedding has no rows")
	}
	bytes := uintptr(e.state.c.TokenCount) * uintptr(e.dim) * 4
	if err := loadCRuntime(); err != nil {
		return nil, err
	}
	dst := cMalloc(bytes)
	if dst == nil {
		return nil, errors.New("ds4: out of memory cloning a vision embedding")
	}
	copy(unsafe.Slice((*byte)(dst), bytes), unsafe.Slice((*byte)(e.state.c.Data), bytes))
	c := e.state.c
	c.Data = dst
	return visionEmbeddingFromC(e.lib, c, e.dim), nil
}

// VisionSpan is an embedding placed at a token offset within a prompt.
type VisionSpan struct {
	TokenStart int
	Embedding  *VisionEmbedding
}

// FreeVisionSpans frees every embedding in spans.
func FreeVisionSpans(spans []VisionSpan) {
	for i := range spans {
		spans[i].Embedding.Free()
	}
}

// HasVision calls ds4_engine_has_vision: whether an encoder is loaded.
func (e *Engine) HasVision() bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 || e.lib.raw.ds4EngineHasVision == nil {
		return false
	}
	return e.lib.raw.ds4EngineHasVision(e.ptr)
}

// EmbdDim calls ds4_engine_embd_dim: the width of one embedding row.
func (e *Engine) EmbdDim() int {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if e == nil || e.ptr == 0 || e.lib.raw.ds4EngineEmbdDim == nil {
		return 0
	}
	return int(e.lib.raw.ds4EngineEmbdDim(e.ptr))
}

// VisionEncodeFile calls ds4_engine_vision_encode_file on a PNG or JPEG path.
func (e *Engine) VisionEncodeFile(path string) (*VisionEmbedding, error) {
	unlock, err := e.require()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if e.lib.raw.ds4EngineVisionEncodeFile == nil {
		return nil, ErrVisionNotSupported
	}
	dim := 0
	if e.lib.raw.ds4EngineEmbdDim != nil {
		dim = int(e.lib.raw.ds4EngineEmbdDim(e.ptr))
	}
	var out cVisionEmbedding
	buf, errPtr, n := errorBuffer()
	ok := e.lib.raw.ds4EngineVisionEncodeFile(e.ptr, path, &out, errPtr, n)
	if ok == 0 {
		return nil, errorFromBuffer("ds4_engine_vision_encode_file", 1, buf)
	}
	return visionEmbeddingFromC(e.lib, out, dim), nil
}

// VisionEncodeMemory calls ds4_engine_vision_encode_memory on encoded PNG or
// JPEG bytes.
func (e *Engine) VisionEncodeMemory(encoded []byte) (*VisionEmbedding, error) {
	unlock, err := e.require()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if e.lib.raw.ds4EngineVisionEncodeMemory == nil {
		return nil, ErrVisionNotSupported
	}
	if len(encoded) == 0 {
		return nil, errors.New("ds4: empty image")
	}
	dim := 0
	if e.lib.raw.ds4EngineEmbdDim != nil {
		dim = int(e.lib.raw.ds4EngineEmbdDim(e.ptr))
	}
	var out cVisionEmbedding
	buf, errPtr, n := errorBuffer()
	ok := e.lib.raw.ds4EngineVisionEncodeMemory(e.ptr, unsafe.Pointer(&encoded[0]), uintptr(len(encoded)), &out, errPtr, n)
	runtime.KeepAlive(encoded)
	if ok == 0 {
		return nil, errorFromBuffer("ds4_engine_vision_encode_memory", 1, buf)
	}
	return visionEmbeddingFromC(e.lib, out, dim), nil
}

// cStringArray builds a NULL-free C array of C strings for a call. Free
// releases everything; libds4 copies what it needs during the call.
type cStringArray struct {
	ptrs unsafe.Pointer
	strs []unsafe.Pointer
}

func newCStringArray(values []string) (*cStringArray, error) {
	if err := loadCRuntime(); err != nil {
		return nil, err
	}
	arr := &cStringArray{ptrs: cMalloc(uintptr(len(values)) * unsafe.Sizeof(uintptr(0)))}
	slots := unsafe.Slice((**byte)(arr.ptrs), len(values))
	for i, v := range values {
		p := cMalloc(uintptr(len(v)) + 1)
		copy(unsafe.Slice((*byte)(p), len(v)+1), append([]byte(v), 0))
		slots[i] = (*byte)(p)
		arr.strs = append(arr.strs, p)
	}
	return arr, nil
}

func (a *cStringArray) free() {
	for _, p := range a.strs {
		cFree(p)
	}
	cFree(a.ptrs)
}

// PromptAppendVision calls ds4_prompt_append_vision: it appends the image
// placeholder block for emb to tokens and moves emb into the returned span.
func (e *Engine) PromptAppendVision(tokens *Tokens, emb *VisionEmbedding) (VisionSpan, error) {
	unlock, err := e.require()
	if err != nil {
		return VisionSpan{}, err
	}
	defer unlock()
	if e.lib.raw.ds4PromptAppendVision == nil {
		return VisionSpan{}, ErrVisionNotSupported
	}
	if emb == nil || emb.state == nil || emb.state.c.Data == nil {
		return VisionSpan{}, errors.New("ds4: empty vision embedding")
	}
	dim := 0
	if e.lib.raw.ds4EngineEmbdDim != nil {
		dim = int(e.lib.raw.ds4EngineEmbdDim(e.ptr))
	}
	var span cVisionSpan
	buf, errPtr, n := errorBuffer()
	ok := e.lib.raw.ds4PromptAppendVision(e.ptr, tokens.cptr(), &span, &emb.state.c, errPtr, n)
	if ok == 0 {
		return VisionSpan{}, errorFromBuffer("ds4_prompt_append_vision", 1, buf)
	}
	emb.take() // libds4 zeroed it; mirror that in Go
	return VisionSpan{TokenStart: int(span.TokenStart), Embedding: visionEmbeddingFromC(e.lib, span.Embedding, dim)}, nil
}

// ChatAppendMultimodalMessage calls ds4_chat_append_multimodal_message for a
// user or tool message whose text parts alternate with images:
// len(textParts) must equal len(images)+1. On success every embedding is
// moved into the returned spans (in order). On failure the embeddings are
// left untouched. With no images it delegates to ChatAppendMessage.
func (e *Engine) ChatAppendMultimodalMessage(tokens *Tokens, role string, textParts []string, images []*VisionEmbedding) ([]VisionSpan, error) {
	if len(textParts) != len(images)+1 {
		return nil, errors.New("ds4: multimodal message needs len(textParts) == len(images)+1")
	}
	if len(images) == 0 {
		return nil, e.ChatAppendMessage(tokens, role, textParts[0])
	}
	if role != "user" && role != "tool" && role != "function" {
		return nil, errors.New("ds4: multimodal messages require a user or tool role")
	}
	unlock, err := e.require()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if e.lib.raw.ds4ChatAppendMultimodalMessage == nil {
		return nil, ErrVisionNotSupported
	}
	dim := 0
	if e.lib.raw.ds4EngineEmbdDim != nil {
		dim = int(e.lib.raw.ds4EngineEmbdDim(e.ptr))
	}
	for i, img := range images {
		if img == nil || img.state == nil || img.state.c.Data == nil {
			return nil, fmt.Errorf("ds4: image %d is an empty vision embedding", i)
		}
	}
	parts, err := newCStringArray(textParts)
	if err != nil {
		return nil, err
	}
	defer parts.free()
	embs := make([]cVisionEmbedding, len(images))
	for i, img := range images {
		embs[i] = img.state.c
	}
	spans := make([]cVisionSpan, len(images))
	buf, errPtr, n := errorBuffer()
	ok := e.lib.raw.ds4ChatAppendMultimodalMessage(e.ptr, tokens.cptr(), role,
		parts.ptrs, unsafe.Pointer(&embs[0]), uintptr(len(images)), unsafe.Pointer(&spans[0]), errPtr, n)
	runtime.KeepAlive(embs)
	if ok == 0 {
		// libds4 unwinds a partial append by handing the already-moved
		// images back through the embeddings array. For DeepSeek those are
		// replacement buffers (the originals were freed while appending), so
		// the handles must adopt whatever came back or a later Free
		// double-frees the original and leaks the replacement.
		for i := range images {
			images[i].state.c = embs[i]
		}
		return nil, errorFromBuffer("ds4_chat_append_multimodal_message", 1, buf)
	}
	out := make([]VisionSpan, len(spans))
	for i := range spans {
		images[i].take() // moved
		out[i] = VisionSpan{TokenStart: int(spans[i].TokenStart), Embedding: visionEmbeddingFromC(e.lib, spans[i].Embedding, dim)}
	}
	return out, nil
}

// spansToC marshals spans for one call. The returned slice must be kept alive
// (runtime.KeepAlive) until the call returns; libds4 copies the identities it
// keeps and never retains the array or the embedding buffers.
func spansToC(spans []VisionSpan) ([]cVisionSpan, error) {
	out := make([]cVisionSpan, len(spans))
	for i, sp := range spans {
		if sp.Embedding == nil || sp.Embedding.state == nil || sp.Embedding.state.c.Data == nil {
			return nil, fmt.Errorf("ds4: image span %d has an empty embedding", i)
		}
		if sp.TokenStart < 0 {
			return nil, fmt.Errorf("ds4: image span %d has a negative token start", i)
		}
		out[i] = cVisionSpan{TokenStart: uint32(sp.TokenStart), Embedding: sp.Embedding.state.c}
	}
	return out, nil
}

func spansPtr(c []cVisionSpan) unsafe.Pointer {
	if len(c) == 0 {
		return nil
	}
	return unsafe.Pointer(&c[0])
}

// SyncMultimodal is Session.SyncTokens for a prompt containing image spans
// (ds4_session_sync_multimodal). With no spans it is the plain sync.
func (s *Session) SyncMultimodal(prompt *Tokens, spans []VisionSpan) error {
	return s.SyncMultimodalWithCancel(prompt, spans, nil)
}

// SyncMultimodalWithCancel is SyncMultimodal polling fn for cancellation, as
// SyncTokensWithCancel does.
func (s *Session) SyncMultimodalWithCancel(prompt *Tokens, spans []VisionSpan, fn CancelFunc) error {
	if len(spans) == 0 {
		if fn == nil {
			return s.SyncTokens(prompt)
		}
		return s.SyncTokensWithCancel(prompt, fn)
	}
	unlock, err := s.require()
	if err != nil {
		return err
	}
	defer unlock()
	if s.lib.raw.ds4SessionSyncMultimodal == nil {
		return ErrVisionNotSupported
	}
	c, err := spansToC(spans)
	if err != nil {
		return err
	}
	if fn != nil {
		if s.lib.raw.ds4SessionSetCancel == nil {
			return ErrCancelNotSupported
		}
		tempID := registerCancelCallback(fn)
		s.lib.raw.ds4SessionSetCancel(s.ptr, cancelCallback, tempID)
		defer func() {
			if s.state.cancelID != 0 {
				s.lib.raw.ds4SessionSetCancel(s.ptr, cancelCallback, s.state.cancelID)
			} else {
				s.lib.raw.ds4SessionSetCancel(s.ptr, 0, 0)
			}
			unregisterCancelCallback(tempID)
		}()
	}
	buf, errPtr, n := errorBuffer()
	code := s.lib.raw.ds4SessionSyncMultimodal(s.ptr, prompt.cptr(), spansPtr(c), uintptr(len(c)), errPtr, n)
	runtime.KeepAlive(c)
	// c holds copies of the C embedding structs; the handles in spans own the
	// buffers those point at, and their cleanups free them.
	runtime.KeepAlive(spans)
	if code == sessionSyncInterruptedCode {
		return ErrSessionSyncInterrupted
	}
	return errorFromBuffer("ds4_session_sync_multimodal", code, buf)
}

// visionPredicate nil-checks s and s.ptr, then resolves the raw symbol under
// libCallMu via pick (rather than in the caller's argument list, which would
// dereference s before this check runs and read the raw symbol table outside
// the lock).
func (s *Session) visionPredicate(spans []VisionSpan, pick func(r *rawSymbols) func(s uintptr, images unsafe.Pointer, n uintptr) bool) bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if s == nil || s.ptr == 0 {
		return false
	}
	fn := pick(&s.lib.raw)
	if fn == nil {
		return false
	}
	c, err := spansToC(spans)
	if err != nil {
		return false
	}
	ok := fn(s.ptr, spansPtr(c), uintptr(len(c)))
	runtime.KeepAlive(c)
	// The handles in spans own the buffers the C call reads through c.
	runtime.KeepAlive(spans)
	return ok
}

// VisionPrefixMatches calls ds4_session_vision_prefix_matches: every image
// the session already holds is unchanged in spans (same offset and
// fingerprint) and any new images start at or after the live frontier. The
// caller must still check the token prefix.
func (s *Session) VisionPrefixMatches(spans []VisionSpan) bool {
	return s.visionPredicate(spans, func(r *rawSymbols) func(s uintptr, images unsafe.Pointer, n uintptr) bool {
		return r.ds4SessionVisionPrefixMatches
	})
}

// VisionStateMatches is VisionPrefixMatches plus an identical image count.
func (s *Session) VisionStateMatches(spans []VisionSpan) bool {
	return s.visionPredicate(spans, func(r *rawSymbols) func(s uintptr, images unsafe.Pointer, n uintptr) bool {
		return r.ds4SessionVisionStateMatches
	})
}

// RebaseVisionState calls ds4_session_rebase_vision_state: for a continuation
// authenticated by other means, restore the session's image offsets into
// spans (TokenStart is updated in place). Fingerprints and row counts must
// match; on failure spans are unchanged.
func (s *Session) RebaseVisionState(spans []VisionSpan) bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if s == nil || s.ptr == 0 || s.lib.raw.ds4SessionRebaseVisionState == nil {
		return false
	}
	c, err := spansToC(spans)
	if err != nil {
		return false
	}
	ok := s.lib.raw.ds4SessionRebaseVisionState(s.ptr, spansPtr(c), uintptr(len(c)))
	if ok {
		for i := range spans {
			spans[i].TokenStart = int(c[i].TokenStart)
		}
	}
	runtime.KeepAlive(c)
	// The handles in spans own the buffers the C call reads through c.
	runtime.KeepAlive(spans)
	return ok
}

// HasVisionState calls ds4_session_has_vision_state: true while the session
// contains, or is syncing, image-conditioned state. Such state must not be
// written to a text-keyed disk cache.
func (s *Session) HasVisionState() bool {
	libCallMu.Lock()
	defer libCallMu.Unlock()
	if s == nil || s.ptr == 0 || s.lib.raw.ds4SessionHasVisionState == nil {
		return false
	}
	return s.lib.raw.ds4SessionHasVisionState(s.ptr)
}
