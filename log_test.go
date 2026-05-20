package ds4

import (
	"bytes"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

func TestSetLogOutputWritesMessages(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	SetDefaultLibrary(lib)
	t.Cleanup(func() {
		_ = SetLogFunc(nil)
		SetDefaultLibrary(nil)
	})

	var buf bytes.Buffer
	if err := SetLogOutput(&buf); err != nil {
		t.Fatalf("SetLogOutput: %v", err)
	}

	ds4api.LogString(0, ds4api.LogWarning, "ds4: routed\n")
	if got, want := buf.String(), "ds4: routed\n"; got != want {
		t.Fatalf("log output = %q, want %q", got, want)
	}
}

func TestDiscardLogsAndRestoreNativeLogger(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	SetDefaultLibrary(lib)
	t.Cleanup(func() {
		_ = SetLogFunc(nil)
		SetDefaultLibrary(nil)
	})

	var buf bytes.Buffer
	if err := SetLogOutput(&buf); err != nil {
		t.Fatalf("SetLogOutput: %v", err)
	}
	ds4api.LogString(0, ds4api.LogWarning, "ds4: visible\n")

	if err := DiscardLogs(); err != nil {
		t.Fatalf("DiscardLogs: %v", err)
	}
	ds4api.LogString(0, ds4api.LogError, "ds4: discarded\n")

	if err := SetLogOutput(nil); err != nil {
		t.Fatalf("SetLogOutput(nil): %v", err)
	}
	ds4api.LogString(0, ds4api.LogError, "ds4: native\n")

	if got, want := buf.String(), "ds4: visible\n"; got != want {
		t.Fatalf("log output after discard/restore = %q, want %q", got, want)
	}
}
