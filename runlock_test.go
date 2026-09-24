package ds4

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// Importing the root package installs the single-runner policy: an engine
// open takes the model's run lock, a second open on the same model fails,
// and Close releases and removes the lock. This held when ds4api owned the
// lock itself and must keep holding with the policy at the root.
func TestRootInstallsEngineRunLockPolicy(t *testing.T) {
	model := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(model, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := ds4api.NewMockLibrary()

	e1, err := lib.NewEngine(ds4api.EngineOptions{ModelPath: model})
	if err != nil {
		t.Fatalf("first NewEngine: %v", err)
	}
	if _, err := os.Stat(model + ".run.lock"); err != nil {
		t.Errorf("run lock missing while engine is open: %v", err)
	}
	if _, err := lib.NewEngine(ds4api.EngineOptions{ModelPath: model}); err == nil {
		t.Error("second engine on the same model = nil error, want busy")
	}

	e1.Close()
	if _, err := os.Stat(model + ".run.lock"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("run lock not removed after Close: %v", err)
	}
	e2, err := lib.NewEngine(ds4api.EngineOptions{ModelPath: model})
	if err != nil {
		t.Fatalf("reopen after Close: %v", err)
	}
	e2.Close()
}

// Inspect-only opens and empty model paths take no lock, as before.
func TestRunLockSkipsInspectAndEmptyModel(t *testing.T) {
	model := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(model, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := ds4api.NewMockLibrary()

	inspect, err := lib.NewEngine(ds4api.EngineOptions{ModelPath: model, InspectOnly: true})
	if err != nil {
		t.Fatalf("inspect NewEngine: %v", err)
	}
	defer inspect.Close()
	if _, err := os.Stat(model + ".run.lock"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("inspect-only open took the run lock: %v", err)
	}

	bare, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatalf("empty-model NewEngine: %v", err)
	}
	bare.Close()
}
