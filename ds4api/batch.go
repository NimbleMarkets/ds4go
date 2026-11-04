package ds4api

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

// DecodeItem advances one independent session by a sampled token in EvalBatch.
type DecodeItem struct {
	Session *Session
	Token   int
}

// EvalBatch advances independent sessions by one token each. libds4 selects
// native batching where available and sequential evaluation otherwise. Older
// libraries without the batch entry point use the same sequential fallback.
// Sessions must be distinct and belong to this engine. On failure some items
// may have advanced; callers should re-sync or discard the affected sessions.
func (e *Engine) EvalBatch(items []DecodeItem) error {
	unlock, err := e.require()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { runtime.KeepAlive(e); runtime.KeepAlive(items) }()
	raw, err := e.decodeItemsLocked(items)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	buf, ptr, n := errorBuffer()
	if e.lib.raw.ds4SessionsEvalBatch == nil {
		for _, item := range raw {
			code := e.lib.raw.ds4SessionEval(item.Session, item.Token, ptr, n)
			if err := errorFromBuffer("ds4_session_eval", code, buf); err != nil {
				return err
			}
		}
		return nil
	}
	// C reads this pointer-free array synchronously and never retains it.
	code := e.lib.raw.ds4SessionsEvalBatch(unsafe.Pointer(&raw[0]), int32(len(raw)), ptr, n)
	runtime.KeepAlive(items)
	runtime.KeepAlive(raw)
	return errorFromBuffer("ds4_sessions_eval_batch", code, buf)
}
func (e *Engine) decodeItemsLocked(items []DecodeItem) ([]cDecodeItem, error) {
	if len(items) > 1<<31-1 {
		return nil, errors.New("ds4: too many decode items")
	}
	raw := make([]cDecodeItem, len(items))
	seen := make(map[*Session]bool, len(items))
	for i, item := range items {
		s := item.Session
		if s == nil || s.ptr == 0 || s.engine != e {
			return nil, fmt.Errorf("ds4: decode item %d must have a live session belonging to this engine", i)
		}
		if seen[s] {
			return nil, errors.New("ds4: duplicate session in decode batch")
		}
		seen[s] = true
		if item.Token < 0 || int64(item.Token) > 1<<31-1 {
			return nil, fmt.Errorf("ds4: invalid token in decode item %d", i)
		}
		raw[i] = cDecodeItem{Session: s.ptr, Token: int32(item.Token)}
	}
	return raw, nil
}
