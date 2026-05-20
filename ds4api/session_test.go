package ds4api

import (
	"strings"
	"testing"
	"unsafe"
)

func TestSaveSnapshotRejectsOversizedNativeLength(t *testing.T) {
	lib := NewMockLibrary()
	var b byte
	lib.raw.ds4SessionSaveSnapshot = func(s uintptr, snap *cSessionSnapshot, err unsafe.Pointer, errLen uintptr) int32 {
		snap.Ptr = unsafe.Pointer(&b)
		snap.Len = uint64(maxInt) + 1
		return 0
	}
	var freed bool
	lib.raw.ds4SessionSnapshotFree = func(snap *cSessionSnapshot) {
		freed = true
	}

	engine, err := lib.NewEngine(EngineOptions{ModelPath: "mock"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()
	session, err := engine.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	_, err = session.SaveSnapshot()
	if err == nil {
		t.Fatal("SaveSnapshot succeeded, want oversized snapshot error")
	}
	if !strings.Contains(err.Error(), "exceeds Go's maximum slice length") {
		t.Fatalf("SaveSnapshot error = %v, want oversized snapshot error", err)
	}
	if !freed {
		t.Fatal("oversized snapshot was not freed")
	}
}
