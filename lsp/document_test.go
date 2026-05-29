package lsp

import "testing"

func TestDocStore_OpenThenUpdateBumpsVersion(t *testing.T) {
	s := newDocStore()
	v1 := s.open("file:///a.lua")
	if v1 != 1 {
		t.Fatalf("open: v=%d, want 1", v1)
	}
	v2 := s.update("file:///a.lua")
	if v2 != 2 {
		t.Fatalf("update: v=%d, want 2", v2)
	}
	v, ok := s.version("file:///a.lua")
	if !ok || v != 2 {
		t.Fatalf("version: %d ok=%v, want 2 true", v, ok)
	}
}

func TestDocStore_UpdateUnknownReturnsZero(t *testing.T) {
	s := newDocStore()
	if v := s.update("file:///missing.lua"); v != 0 {
		t.Fatalf("update unknown: v=%d, want 0", v)
	}
}

func TestDocStore_CloseRemoves(t *testing.T) {
	s := newDocStore()
	s.open("file:///a.lua")
	s.close("file:///a.lua")
	if _, ok := s.version("file:///a.lua"); ok {
		t.Fatal("expected document removed after close")
	}
}
