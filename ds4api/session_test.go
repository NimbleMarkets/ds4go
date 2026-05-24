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

func TestSessionSetDisplayProgressInstallsAndClears(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(4096)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	// Install a callback. We only verify it can be set, cleared, and
	// replaced without leaking registered callback IDs.
	cb := func(event string, current, total int) {}
	if err := sess.SetDisplayProgress(cb); err != nil {
		t.Fatalf("SetDisplayProgress(cb): %v", err)
	}
	if err := sess.SetDisplayProgress(cb); err != nil {
		t.Fatalf("SetDisplayProgress(cb) replace: %v", err)
	}
	if err := sess.SetDisplayProgress(nil); err != nil {
		t.Fatalf("SetDisplayProgress(nil): %v", err)
	}

	// SetProgress and SetDisplayProgress are independent slots: setting
	// one must not clear the other.
	if err := sess.SetProgress(cb); err != nil {
		t.Fatalf("SetProgress(cb): %v", err)
	}
	if err := sess.SetDisplayProgress(cb); err != nil {
		t.Fatalf("SetDisplayProgress(cb) with SetProgress active: %v", err)
	}
}

func TestSessionCopyLogits(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(4096)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	logits, err := sess.CopyLogits()
	if err != nil {
		t.Fatalf("CopyLogits: %v", err)
	}
	if got := len(logits); got != eng.VocabSize() {
		t.Fatalf("len(CopyLogits()) = %d, want %d", got, eng.VocabSize())
	}
}

func TestSessionPowerRoundTrips(t *testing.T) {
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{PowerPercent: 80})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	sess, err := eng.NewSession(8192)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	if got := sess.Power(); got != 80 {
		t.Fatalf("Session.Power() = %d, want 80 (inherited from engine)", got)
	}
	if err := sess.SetPower(30); err != nil {
		t.Fatalf("SetPower(30): %v", err)
	}
	// Session.SetPower writes through to the engine (matches ds4.c:17861).
	if got := eng.Power(); got != 30 {
		t.Fatalf("Engine.Power() after Session.SetPower(30) = %d, want 30", got)
	}
	for _, bad := range []int{0, -1, 101, 200} {
		if err := sess.SetPower(bad); err == nil {
			t.Fatalf("Session.SetPower(%d) = nil, want error", bad)
		}
	}
	// Boundary values 1 and 100 must be accepted.
	for _, good := range []int{1, 100} {
		if err := sess.SetPower(good); err != nil {
			t.Fatalf("Session.SetPower(%d): %v", good, err)
		}
		if got := sess.Power(); got != good {
			t.Fatalf("Session.Power() after SetPower(%d) = %d, want %d", good, got, good)
		}
	}
}
