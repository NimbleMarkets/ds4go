package ds4api

import "testing"

func TestLibrarySetAbortFuncRoutesFatalMessage(t *testing.T) {
	lib := NewMockLibrary()
	var installedFn uintptr
	var installedID uintptr
	lib.raw.ds4AbortSet = func(fn uintptr, ud uintptr) {
		installedFn = fn
		installedID = ud
	}

	var got string
	if err := lib.SetAbortFunc(func(msg string) {
		got = msg
	}); err != nil {
		t.Fatalf("SetAbortFunc: %v", err)
	}
	if installedFn == 0 || installedID == 0 {
		t.Fatalf("abort callback not installed: fn=%d id=%d", installedFn, installedID)
	}

	invokeAbortCallback(installedID, "fatal invariant")
	if got != "fatal invariant" {
		t.Fatalf("abort callback got %q, want fatal invariant", got)
	}
	oldID := installedID

	if err := lib.SetAbortFunc(nil); err != nil {
		t.Fatalf("SetAbortFunc(nil): %v", err)
	}
	if installedFn != 0 || installedID != 0 {
		t.Fatalf("abort callback not reset: fn=%d id=%d", installedFn, installedID)
	}

	got = ""
	invokeAbortCallback(oldID, "hidden")
	if got != "" {
		t.Fatalf("abort callback invoked after reset: %q", got)
	}
}
