package ds4

import (
	"runtime"
	"sync"
	"unsafe"
)

// Session wraps a ds4_session.
type Session struct {
	lib        *Library
	engine     *Engine
	ptr        uintptr
	once       sync.Once
	progressID uintptr
}

// NewSession creates a ds4 session for this engine and context size.
func (e *Engine) NewSession(ctxSize int) (*Session, error) {
	if err := e.require(); err != nil {
		return nil, err
	}
	var out uintptr
	code := e.lib.raw.ds4SessionCreate(&out, e.ptr, int32(ctxSize))
	if err := ds4Error("ds4_session_create", code); err != nil {
		return nil, err
	}
	s := &Session{lib: e.lib, engine: e, ptr: out}
	runtime.SetFinalizer(s, (*Session).Close)
	return s, nil
}

// Close releases the underlying ds4_session.
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		runtime.SetFinalizer(s, nil)
		if s.ptr != 0 {
			s.lib.raw.ds4SessionSetProgress(s.ptr, 0, 0)
			unregisterProgressCallback(s.progressID)
			s.lib.raw.ds4SessionFree(s.ptr)
			s.ptr = 0
		}
	})
}

func (s *Session) require() error {
	if s == nil || s.ptr == 0 {
		return errClosed
	}
	return nil
}

// SetProgress sets a persistent progress callback for ds4_session_set_progress.
func (s *Session) SetProgress(fn ProgressFunc) error {
	if err := s.require(); err != nil {
		return err
	}
	if s.progressID != 0 {
		s.lib.raw.ds4SessionSetProgress(s.ptr, 0, 0)
		unregisterProgressCallback(s.progressID)
		s.progressID = 0
	}
	if fn == nil {
		return nil
	}
	id := registerProgressCallback(fn)
	s.progressID = id
	s.lib.raw.ds4SessionSetProgress(s.ptr, progressCallback, id)
	return nil
}

// Sync synchronizes the live session to a full prompt token prefix.
func (s *Session) Sync(prompt []int) error {
	tokens, err := newTokensWithLibrary(s.lib, prompt)
	if err != nil {
		return err
	}
	defer tokens.Free()
	return s.SyncTokens(tokens)
}

// SyncTokens synchronizes the live session to a full prompt token prefix.
func (s *Session) SyncTokens(prompt *Tokens) error {
	if err := s.require(); err != nil {
		return err
	}
	buf, ptr, n := errorBuffer()
	code := s.lib.raw.ds4SessionSync(s.ptr, prompt.cptr(), ptr, n)
	return errorFromBuffer("ds4_session_sync", code, buf)
}

// RewriteRequiresRebuild calls ds4_session_rewrite_requires_rebuild.
func RewriteRequiresRebuild(liveLen, canonicalLen, common int) bool {
	lib, err := DefaultLibrary()
	if err != nil {
		return false
	}
	return lib.raw.ds4SessionRewriteRequiresRebuild(int32(liveLen), int32(canonicalLen), int32(common))
}

// RewriteFromCommon rewrites a session from a known common prefix length.
func (s *Session) RewriteFromCommon(prompt *Tokens, common int) (SessionRewriteResult, error) {
	if err := s.require(); err != nil {
		return SessionRewriteError, err
	}
	buf, ptr, n := errorBuffer()
	result := s.lib.raw.ds4SessionRewriteFromCommon(s.ptr, prompt.cptr(), int32(common), ptr, n)
	if result == SessionRewriteError {
		return result, errorFromBuffer("ds4_session_rewrite_from_common", -1, buf)
	}
	return result, nil
}

// CommonPrefix returns the common prefix length between the live session and prompt.
func (s *Session) CommonPrefix(prompt *Tokens) int {
	if s == nil || s.ptr == 0 {
		return 0
	}
	return int(s.lib.raw.ds4SessionCommonPrefix(s.ptr, prompt.cptr()))
}

// Argmax returns the argmax token id for the current logits.
func (s *Session) Argmax() int {
	if s == nil || s.ptr == 0 {
		return 0
	}
	return int(s.lib.raw.ds4SessionArgmax(s.ptr))
}

// ArgmaxExcluding returns the argmax token id excluding one token.
func (s *Session) ArgmaxExcluding(excludedID int) int {
	if s == nil || s.ptr == 0 {
		return 0
	}
	return int(s.lib.raw.ds4SessionArgmaxExcluding(s.ptr, int32(excludedID)))
}

// Sample samples the next token from current logits.
func (s *Session) Sample(temperature float32, topK int, topP, minP float32, rng *uint64) int {
	if s == nil || s.ptr == 0 {
		return 0
	}
	var seed uint64
	if rng == nil {
		rng = &seed
	}
	return int(s.lib.raw.ds4SessionSample(s.ptr, temperature, int32(topK), topP, minP, rng))
}

// TopLogprobs returns the top k token scores for the current logits.
func (s *Session) TopLogprobs(k int) ([]TokenScore, error) {
	if err := s.require(); err != nil {
		return nil, err
	}
	if k <= 0 {
		return nil, nil
	}
	raw := make([]cTokenScore, k)
	n := s.lib.raw.ds4SessionTopLogprobs(s.ptr, &raw[0], int32(k))
	if n < 0 {
		return nil, ds4Error("ds4_session_top_logprobs", n)
	}
	out := make([]TokenScore, int(n))
	for i := range out {
		out[i] = TokenScore{ID: int(raw[i].ID), Logit: raw[i].Logit, Logprob: raw[i].Logprob}
	}
	return out, nil
}

// TokenLogprob returns the score for a specific token.
func (s *Session) TokenLogprob(token int) (TokenScore, error) {
	if err := s.require(); err != nil {
		return TokenScore{}, err
	}
	var raw cTokenScore
	code := s.lib.raw.ds4SessionTokenLogprob(s.ptr, int32(token), &raw)
	if err := ds4Error("ds4_session_token_logprob", code); err != nil {
		return TokenScore{}, err
	}
	return TokenScore{ID: int(raw.ID), Logit: raw.Logit, Logprob: raw.Logprob}, nil
}

// Eval evaluates one token and advances the session.
func (s *Session) Eval(token int) error {
	if err := s.require(); err != nil {
		return err
	}
	buf, ptr, n := errorBuffer()
	code := s.lib.raw.ds4SessionEval(s.ptr, int32(token), ptr, n)
	return errorFromBuffer("ds4_session_eval", code, buf)
}

// EvalSpeculativeArgmax calls ds4_session_eval_speculative_argmax.
func (s *Session) EvalSpeculativeArgmax(firstToken, maxTokens, eosToken int) ([]int, error) {
	if err := s.require(); err != nil {
		return nil, err
	}
	if maxTokens <= 0 {
		return nil, nil
	}
	accepted := make([]int32, maxTokens)
	buf, ptr, n := errorBuffer()
	code := s.lib.raw.ds4SessionEvalSpeculativeArgmax(s.ptr, int32(firstToken), int32(maxTokens), int32(eosToken), unsafe.Pointer(&accepted[0]), int32(len(accepted)), ptr, n)
	if code < 0 {
		return nil, errorFromBuffer("ds4_session_eval_speculative_argmax", code, buf)
	}
	count := int(code)
	if count > len(accepted) {
		count = len(accepted)
	}
	out := make([]int, count)
	for i := range out {
		out[i] = int(accepted[i])
	}
	return out, nil
}

// Invalidate invalidates the live session state.
func (s *Session) Invalidate() {
	if s != nil && s.ptr != 0 {
		s.lib.raw.ds4SessionInvalidate(s.ptr)
	}
}

// Rewind rewinds the session to token position pos.
func (s *Session) Rewind(pos int) {
	if s != nil && s.ptr != 0 {
		s.lib.raw.ds4SessionRewind(s.ptr, int32(pos))
	}
}

// Pos returns the current session token position.
func (s *Session) Pos() int {
	if s == nil || s.ptr == 0 {
		return 0
	}
	return int(s.lib.raw.ds4SessionPos(s.ptr))
}

// Ctx returns the session context size.
func (s *Session) Ctx() int {
	if s == nil || s.ptr == 0 {
		return 0
	}
	return int(s.lib.raw.ds4SessionCtx(s.ptr))
}

// Tokens returns a borrowed snapshot of ds4_session_tokens.
func (s *Session) Tokens() *Tokens {
	if s == nil || s.ptr == 0 {
		return nil
	}
	return borrowedTokens(s.lib, s.lib.raw.ds4SessionTokens(s.ptr))
}

// Generate synchronizes to prompt and generates tokens from the session.
func (s *Session) Generate(prompt []int, opts GenerateOptions) ([]int, error) {
	if err := s.Sync(prompt); err != nil {
		return nil, err
	}
	return s.ContinueGenerate(opts)
}

// GenerateTokens synchronizes to prompt and generates tokens from the session.
func (s *Session) GenerateTokens(prompt *Tokens, opts GenerateOptions) ([]int, error) {
	if err := s.SyncTokens(prompt); err != nil {
		return nil, err
	}
	return s.ContinueGenerate(opts)
}

// ContinueGenerate generates tokens from the current session logits.
func (s *Session) ContinueGenerate(opts GenerateOptions) ([]int, error) {
	if err := s.require(); err != nil {
		return nil, err
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 128
	}
	eos := -1
	if opts.StopOnEOS && s.engine != nil {
		eos = s.engine.TokenEOS()
	}
	var rng = opts.Seed
	out := make([]int, 0, maxTokens)
	for i := 0; i < maxTokens; i++ {
		var token int
		if opts.Temperature > 0 {
			token = s.Sample(opts.Temperature, opts.TopK, opts.TopP, opts.MinP, &rng)
		} else if opts.ExcludeToken != 0 {
			token = s.ArgmaxExcluding(opts.ExcludeToken)
		} else {
			token = s.Argmax()
		}
		if opts.StopOnEOS && token == eos {
			break
		}
		out = append(out, token)
		if opts.OnToken != nil {
			opts.OnToken(token)
		}
		if err := s.Eval(token); err != nil {
			return out, err
		}
	}
	return out, nil
}

// GenerateString tokenizes prompt, generates, and decodes the generated text.
func (s *Session) GenerateString(prompt string, opts GenerateOptions) (string, error) {
	if s == nil || s.engine == nil {
		return "", errClosed
	}
	tokens, err := s.engine.TokenizeText(prompt)
	if err != nil {
		return "", err
	}
	defer tokens.Free()
	generated, err := s.GenerateTokens(tokens, opts)
	if err != nil {
		return "", err
	}
	text := ""
	for _, token := range generated {
		part, err := s.engine.TokenText(token)
		if err != nil {
			return text, err
		}
		text += part
	}
	return text, nil
}

// PayloadBytes returns ds4_session_payload_bytes.
func (s *Session) PayloadBytes() uint64 {
	if s == nil || s.ptr == 0 {
		return 0
	}
	return s.lib.raw.ds4SessionPayloadBytes(s.ptr)
}

// SavePayload writes the DS4-specific session payload to fp.
func (s *Session) SavePayload(fp File) error {
	if err := s.require(); err != nil {
		return err
	}
	buf, ptr, n := errorBuffer()
	code := s.lib.raw.ds4SessionSavePayload(s.ptr, uintptr(fp), ptr, n)
	return errorFromBuffer("ds4_session_save_payload", code, buf)
}

// SavePayloadFile writes the DS4-specific session payload to path.
func (s *Session) SavePayloadFile(path string) error {
	fp, err := OpenFile(path, "wb")
	if err != nil {
		return err
	}
	defer fp.Close()
	return s.SavePayload(fp)
}

// LoadPayload reads a DS4-specific session payload from fp.
func (s *Session) LoadPayload(fp File, payloadBytes uint64) error {
	if err := s.require(); err != nil {
		return err
	}
	buf, ptr, n := errorBuffer()
	code := s.lib.raw.ds4SessionLoadPayload(s.ptr, uintptr(fp), payloadBytes, ptr, n)
	return errorFromBuffer("ds4_session_load_payload", code, buf)
}

// LoadPayloadFile reads a DS4-specific session payload from path.
func (s *Session) LoadPayloadFile(path string, payloadBytes uint64) error {
	fp, err := OpenFile(path, "rb")
	if err != nil {
		return err
	}
	defer fp.Close()
	return s.LoadPayload(fp, payloadBytes)
}

// SaveSnapshot serializes a session snapshot to a Go byte slice.
func (s *Session) SaveSnapshot() ([]byte, error) {
	if err := s.require(); err != nil {
		return nil, err
	}
	buf, ptr, n := errorBuffer()
	var snap cSessionSnapshot
	code := s.lib.raw.ds4SessionSaveSnapshot(s.ptr, &snap, ptr, n)
	if err := errorFromBuffer("ds4_session_save_snapshot", code, buf); err != nil {
		return nil, err
	}
	defer s.lib.raw.ds4SessionSnapshotFree(&snap)
	if snap.Ptr == nil || snap.Len == 0 {
		return nil, nil
	}
	return append([]byte(nil), unsafe.Slice((*byte)(snap.Ptr), int(snap.Len))...), nil
}

// LoadSnapshot restores a session snapshot previously returned by SaveSnapshot.
func (s *Session) LoadSnapshot(data []byte) error {
	if err := s.require(); err != nil {
		return err
	}
	var ptr unsafe.Pointer
	if len(data) > 0 {
		ptr = unsafe.Pointer(&data[0])
	}
	snap := cSessionSnapshot{Ptr: ptr, Len: uint64(len(data)), Cap: uint64(len(data))}
	buf, errPtr, n := errorBuffer()
	code := s.lib.raw.ds4SessionLoadSnapshot(s.ptr, &snap, errPtr, n)
	runtime.KeepAlive(data)
	return errorFromBuffer("ds4_session_load_snapshot", code, buf)
}
