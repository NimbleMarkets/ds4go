package ds4api

import (
	"runtime"
	"unsafe"
)

// Tokens owns a ds4_tokens value allocated by libds4.
type Tokens struct {
	lib     *Library
	state   *tokensCleanupState
	owned   bool
	cleanup runtime.Cleanup
}

type tokensCleanupState struct {
	c cTokens
}

type tokensCleanupArg struct {
	lib   *Library
	state *tokensCleanupState
}

func cleanTokens(arg tokensCleanupArg) {
	if arg.state.c.V != nil {
		arg.lib.raw.ds4TokensFree(&arg.state.c)
	}
}

// NewTokens creates a libds4-owned token vector from ids.
func NewTokens(ids []int) (*Tokens, error) {
	lib, err := DefaultLibrary()
	if err != nil {
		return nil, err
	}
	return newTokensWithLibrary(lib, ids)
}

func newTokensWithLibrary(lib *Library, ids []int) (*Tokens, error) {
	lib, err := ensureLibrary(lib)
	if err != nil {
		return nil, err
	}
	state := &tokensCleanupState{}
	t := &Tokens{lib: lib, state: state, owned: true}
	for _, id := range ids {
		lib.raw.ds4TokensPush(&state.c, int32(id))
	}
	t.cleanup = runtime.AddCleanup(t, cleanTokens, tokensCleanupArg{
		lib:   lib,
		state: state,
	})
	return t, nil
}

func tokensFromC(lib *Library, c cTokens) *Tokens {
	state := &tokensCleanupState{c: c}
	t := &Tokens{lib: lib, state: state, owned: true}
	t.cleanup = runtime.AddCleanup(t, cleanTokens, tokensCleanupArg{
		lib:   lib,
		state: state,
	})
	return t
}

func borrowedTokens(lib *Library, c *cTokens) *Tokens {
	state := &tokensCleanupState{}
	if c != nil {
		state.c = *c
	}
	return &Tokens{lib: lib, state: state}
}

// Free releases memory owned by this token vector.
func (t *Tokens) Free() {
	if t == nil || t.state == nil || t.state.c.V == nil || !t.owned {
		return
	}
	t.cleanup.Stop()
	t.lib.raw.ds4TokensFree(&t.state.c)
	t.state.c = cTokens{}
	t.owned = false
}

// Len returns the number of tokens.
func (t *Tokens) Len() int {
	if t == nil || t.state == nil {
		return 0
	}
	return int(t.state.c.Len)
}

// Cap returns the token vector capacity.
func (t *Tokens) Cap() int {
	if t == nil || t.state == nil {
		return 0
	}
	return int(t.state.c.Cap)
}

// Slice returns a copy of the token ids.
func (t *Tokens) Slice() []int {
	if t == nil || t.state == nil || t.state.c.V == nil || t.state.c.Len <= 0 {
		return nil
	}
	src := unsafe.Slice((*int32)(t.state.c.V), int(t.state.c.Len))
	out := make([]int, len(src))
	for i, id := range src {
		out[i] = int(id)
	}
	return out
}

// Push appends one token id to the vector.
func (t *Tokens) Push(token int) {
	if t == nil || t.state == nil {
		return
	}
	t.lib.raw.ds4TokensPush(&t.state.c, int32(token))
}

// Copy returns a deep copy of this token vector.
func (t *Tokens) Copy() *Tokens {
	var dst cTokens
	if t != nil && t.state != nil {
		t.lib.raw.ds4TokensCopy(&dst, &t.state.c)
	}
	return tokensFromC(t.lib, dst)
}

// StartsWith reports whether t begins with prefix.
func (t *Tokens) StartsWith(prefix *Tokens) bool {
	if t == nil || prefix == nil || t.state == nil || prefix.state == nil {
		return false
	}
	return t.lib.raw.ds4TokensStartsWith(&t.state.c, &prefix.state.c)
}

func (t *Tokens) cptr() *cTokens {
	if t == nil || t.state == nil {
		return nil
	}
	return &t.state.c
}
