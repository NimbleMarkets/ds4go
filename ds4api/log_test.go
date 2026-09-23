package ds4api

import (
	"os"
	"testing"
)

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

// LogString must hand ds4_log a message that survives the trip through C's
// printf machinery unchanged. purego cannot pass C variadic arguments, so a
// "%s" format with the message as a trailing argument makes va_arg read
// garbage (the real library prints "(null)" on darwin/arm64). The message
// therefore has to travel as the format string itself, with every literal
// '%' escaped so printf reproduces it and never consults va_arg.
func TestLogStringRoundTripsThroughFormat(t *testing.T) {
	defaultMu.Lock()
	prev := defaultLib
	defaultMu.Unlock()
	lib := NewMockLibrary()
	SetDefaultLibrary(lib)
	t.Cleanup(func() { SetDefaultLibrary(prev) })

	f, err := os.CreateTemp(t.TempDir(), "ds4-log-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := lib.SetStderrFd(int(f.Fd())); err != nil {
		t.Fatalf("SetStderrFd: %v", err)
	}
	t.Cleanup(func() { _ = lib.SetStderrFd(-1) })

	for _, msg := range []string{
		"hello-from-go\n",
		"prefill 50% done\n",
		"format %s must be literal: %d %5.2f %%\n",
		"100%",
	} {
		if err := f.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		LogString(0, LogDefault, msg)
		got, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != msg {
			t.Errorf("LogString(%q) wrote %q", msg, got)
		}
	}
}
