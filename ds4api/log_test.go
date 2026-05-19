package ds4api

import "testing"

func TestLibrarySetLogFuncRoutesLogString(t *testing.T) {
	lib := NewMockLibrary()
	var gotType LogType
	var gotMsg string

	if err := lib.SetLogFunc(func(typ LogType, msg string) {
		gotType = typ
		gotMsg = msg
	}); err != nil {
		t.Fatalf("SetLogFunc: %v", err)
	}

	lib.raw.ds4LogString(0, LogWarning, "%s", "ds4: warning\n")
	if gotType != LogWarning || gotMsg != "ds4: warning\n" {
		t.Fatalf("log callback got (%v, %q), want (%v, %q)", gotType, gotMsg, LogWarning, "ds4: warning\n")
	}

	if err := lib.SetLogFunc(nil); err != nil {
		t.Fatalf("SetLogFunc(nil): %v", err)
	}
	gotType = LogDefault
	gotMsg = ""
	lib.raw.ds4LogString(0, LogError, "%s", "ds4: hidden\n")
	if gotMsg != "" {
		t.Fatalf("log callback invoked after reset: (%v, %q)", gotType, gotMsg)
	}
}
