package ds4

import (
	"runtime"
	"unsafe"
)

// Tokens owns a ds4_tokens value allocated by libds4.
type Tokens struct {
	lib   *Library
	c     cTokens
	owned bool
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
	t := &Tokens{lib: lib, owned: true}
	for _, id := range ids {
		lib.raw.ds4TokensPush(&t.c, int32(id))
	}
	runtime.SetFinalizer(t, (*Tokens).Free)
	return t, nil
}

func tokensFromC(lib *Library, c cTokens) *Tokens {
	t := &Tokens{lib: lib, c: c, owned: true}
	runtime.SetFinalizer(t, (*Tokens).Free)
	return t
}

func borrowedTokens(lib *Library, c *cTokens) *Tokens {
	if c == nil {
		return &Tokens{lib: lib}
	}
	return &Tokens{lib: lib, c: *c}
}

// Free releases memory owned by this token vector.
func (t *Tokens) Free() {
	if t == nil || t.c.V == nil || !t.owned {
		return
	}
	runtime.SetFinalizer(t, nil)
	t.lib.raw.ds4TokensFree(&t.c)
	t.c = cTokens{}
	t.owned = false
}

// Len returns the number of tokens.
func (t *Tokens) Len() int {
	if t == nil {
		return 0
	}
	return int(t.c.Len)
}

// Cap returns the token vector capacity.
func (t *Tokens) Cap() int {
	if t == nil {
		return 0
	}
	return int(t.c.Cap)
}

// Slice returns a copy of the token ids.
func (t *Tokens) Slice() []int {
	if t == nil || t.c.V == nil || t.c.Len <= 0 {
		return nil
	}
	src := unsafe.Slice((*int32)(t.c.V), int(t.c.Len))
	out := make([]int, len(src))
	for i, id := range src {
		out[i] = int(id)
	}
	return out
}

// Push appends one token id to the vector.
func (t *Tokens) Push(token int) {
	t.lib.raw.ds4TokensPush(&t.c, int32(token))
}

// Copy returns a deep copy of this token vector.
func (t *Tokens) Copy() *Tokens {
	var dst cTokens
	t.lib.raw.ds4TokensCopy(&dst, &t.c)
	return tokensFromC(t.lib, dst)
}

// StartsWith reports whether t begins with prefix.
func (t *Tokens) StartsWith(prefix *Tokens) bool {
	if t == nil || prefix == nil {
		return false
	}
	return t.lib.raw.ds4TokensStartsWith(&t.c, &prefix.c)
}

func (t *Tokens) cptr() *cTokens {
	if t == nil {
		return nil
	}
	return &t.c
}
