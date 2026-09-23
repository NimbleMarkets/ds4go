package ds4

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

func TestCaptureStderrPumpsToWriter(t *testing.T) {
	SetDefaultLibrary(ds4api.NewMockLibrary())
	t.Cleanup(func() { SetDefaultLibrary(nil) })

	var buf bytes.Buffer
	cap, err := CaptureStderr(&buf)
	if err != nil {
		t.Fatalf("CaptureStderr: %v", err)
	}

	ds4api.LogString(0, ds4api.LogWarning, "ds4: captured\n")

	if err := cap.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := buf.String(); got != "ds4: captured\n" {
		t.Fatalf("captured = %q, want %q", got, "ds4: captured\n")
	}
}

func TestSetStderrRedirectsToFile(t *testing.T) {
	SetDefaultLibrary(ds4api.NewMockLibrary())
	t.Cleanup(func() {
		_ = SetStderr(nil)
		SetDefaultLibrary(nil)
	})

	f, err := os.CreateTemp(t.TempDir(), "ds4-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := SetStderr(f); err != nil {
		t.Fatalf("SetStderr: %v", err)
	}
	ds4api.LogString(0, ds4api.LogWarning, "ds4: visible\n")

	if got, _ := os.ReadFile(f.Name()); string(got) != "ds4: visible\n" {
		t.Fatalf("redirected output = %q, want %q", got, "ds4: visible\n")
	}
}

func TestDiscardLogsSilencesOutput(t *testing.T) {
	SetDefaultLibrary(ds4api.NewMockLibrary())
	t.Cleanup(func() {
		_ = SetStderr(nil)
		SetDefaultLibrary(nil)
	})

	f, err := os.CreateTemp(t.TempDir(), "ds4-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := SetStderr(f); err != nil {
		t.Fatalf("SetStderr: %v", err)
	}
	ds4api.LogString(0, ds4api.LogWarning, "ds4: before\n")

	if err := DiscardLogs(); err != nil {
		t.Fatalf("DiscardLogs: %v", err)
	}
	ds4api.LogString(0, ds4api.LogError, "ds4: discarded\n")

	if got, _ := os.ReadFile(f.Name()); string(got) != "ds4: before\n" {
		t.Fatalf("file after discard = %q, want only %q", got, "ds4: before\n")
	}
}

func TestSetAbortFuncUsesDefaultLibrary(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	SetDefaultLibrary(lib)
	t.Cleanup(func() {
		_ = SetAbortFunc(nil)
		SetDefaultLibrary(nil)
	})

	if err := SetAbortFunc(func(string) {}); err != nil {
		t.Fatalf("SetAbortFunc: %v", err)
	}
	if err := SetAbortFunc(nil); err != nil {
		t.Fatalf("SetAbortFunc(nil): %v", err)
	}
}

// Close must restore the captured library and drain its output even when the
// default has changed and a fresh lookup would fail or use different symbols.
func TestStderrCaptureCloseUsesOriginalLibrary(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement *ds4api.Library
	}{
		{name: "default cleared"},
		// This sentinel has no bound symbols: attempting to restore through
		// the replacement instead of the original library would panic.
		{name: "default replaced", replacement: &ds4api.Library{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := ds4api.NewMockLibrary()
			SetDefaultLibrary(lib)
			t.Cleanup(func() {
				_ = lib.SetStderrFd(-1)
				SetDefaultLibrary(nil)
			})
			t.Setenv("DS4_LIB", filepath.Join(t.TempDir(), "missing", libraryFileName()))
			t.Setenv("DS4_DIR", t.TempDir())

			var buf bytes.Buffer
			cap, err := CaptureStderr(&buf)
			if err != nil {
				t.Fatalf("CaptureStderr: %v", err)
			}
			const message = "ds4: before restore\n"
			ds4api.LogString(0, ds4api.LogWarning, message)
			SetDefaultLibrary(tc.replacement)

			closed := make(chan error, 1)
			go func() { closed <- cap.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatalf("Close: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Close hung after changing the default library")
			}
			if got := buf.String(); got != message {
				t.Fatalf("captured = %q, want %q", got, message)
			}
			if tc.replacement != nil {
				got, err := ds4api.DefaultLibrary()
				if err != nil || got != tc.replacement {
					t.Fatalf("Close changed the default library: got %p, error %v", got, err)
				}
			} else if _, err := ds4api.DefaultLibrary(); err == nil {
				t.Fatal("Close repopulated the cleared default library")
			}
		})
	}
}
