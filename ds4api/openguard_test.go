package ds4api

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The binding layer runs whatever open guard the runtime layer installed,
// holding its release for the engine lifetime: released once on Close, never
// twice.
func TestEngineOpenGuardWrapsEngineLifetime(t *testing.T) {
	var acquired, released int
	prev := SetEngineOpenGuard(func(opts EngineOptions) (func(), error) {
		acquired++
		if opts.ModelPath != "mock-model" {
			t.Errorf("guard saw ModelPath %q, want mock-model", opts.ModelPath)
		}
		return func() { released++ }, nil
	})
	t.Cleanup(func() { SetEngineOpenGuard(prev) })

	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{ModelPath: "mock-model"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if acquired != 1 || released != 0 {
		t.Errorf("after open: acquired=%d released=%d, want 1/0", acquired, released)
	}
	eng.Close()
	if released != 1 {
		t.Errorf("after Close: released=%d, want 1", released)
	}
	eng.Close()
	if released != 1 {
		t.Errorf("after second Close: released=%d, want 1", released)
	}
}

// A guard refusal aborts the open and surfaces its error.
func TestEngineOpenGuardErrorAbortsOpen(t *testing.T) {
	prev := SetEngineOpenGuard(func(EngineOptions) (func(), error) {
		return nil, errors.New("model busy")
	})
	t.Cleanup(func() { SetEngineOpenGuard(prev) })

	lib := NewMockLibrary()
	if _, err := lib.NewEngine(EngineOptions{ModelPath: "m"}); err == nil || !strings.Contains(err.Error(), "model busy") {
		t.Errorf("NewEngine = %v, want the guard's error", err)
	}
}

// Without an installed guard the strict binding layer carries no model
// management policy: opening an engine writes nothing next to the model.
func TestNoGuardMeansNoFilesystemPolicy(t *testing.T) {
	model := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(model, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{ModelPath: model})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if _, err := os.Stat(model + ".run.lock"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("run lock beside the model: stat err = %v, want not-exist", err)
	}
}

// A guard that acquires and then fails must have its release called: the
// open never happens, so nothing else will free what the guard took.
func TestGuardErrorReleasesWhatItAcquired(t *testing.T) {
	released := 0
	prev := SetEngineOpenGuard(func(EngineOptions) (func(), error) {
		return func() { released++ }, errors.New("refused after acquiring")
	})
	t.Cleanup(func() { SetEngineOpenGuard(prev) })

	lib := NewMockLibrary()
	if _, err := lib.NewEngine(EngineOptions{ModelPath: "m"}); err == nil {
		t.Fatal("NewEngine = nil error, want the guard's error")
	}
	if released != 1 {
		t.Errorf("released = %d, want 1", released)
	}
}

// SetEngineOpenGuard returns the previously installed guard so a caller
// layering additional policy can chain to it instead of silently replacing
// it (the module root's run-lock policy, for one).
func TestSetEngineOpenGuardReturnsThePreviousGuard(t *testing.T) {
	firstRan := false
	orig := SetEngineOpenGuard(func(EngineOptions) (func(), error) {
		firstRan = true
		return nil, nil
	})
	t.Cleanup(func() { SetEngineOpenGuard(orig) })

	prev := SetEngineOpenGuard(nil)
	if prev == nil {
		t.Fatal("SetEngineOpenGuard returned nil, want the guard installed above")
	}
	if _, err := prev(EngineOptions{}); err != nil || !firstRan {
		t.Errorf("returned guard ran: err=%v firstRan=%v, want nil/true", err, firstRan)
	}
}
