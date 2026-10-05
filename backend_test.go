package ds4

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// writeBackendMeta creates a fake libds4.so with ds4go-install.json beside it
// declaring backend, and returns the library path.
func writeBackendMeta(t *testing.T, backend string) string {
	t.Helper()
	dir := t.TempDir()
	lib := filepath.Join(dir, "libds4.so")
	if err := os.WriteFile(lib, []byte("not a real library"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"backend": backend})
	if err := os.WriteFile(filepath.Join(dir, "ds4go-install.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return lib
}

func withDefaultLibrary(t *testing.T, lib *ds4api.Library) {
	t.Helper()
	prev := ds4api.CurrentDefaultLibrary()
	ds4api.SetDefaultLibrary(lib)
	t.Cleanup(func() { ds4api.SetDefaultLibrary(prev) })
}

func TestDetectDefaultBackendMetadata(t *testing.T) {
	withDefaultLibrary(t, nil)
	cases := map[string]Backend{"rocm": BackendROCm, "cuda": BackendCUDA, "cpu": BackendCPU, "metal": BackendMetal}
	for meta, want := range cases {
		if got := DetectDefaultBackend(writeBackendMeta(t, meta)); got != want {
			t.Errorf("metadata %q: DetectDefaultBackend = %v, want %v", meta, got, want)
		}
	}
}

func TestDetectBackendUsesLoadedLibraryFlavor(t *testing.T) {
	// A library pinned with the wrong backend label: the loaded library's
	// own flavor wins over metadata for the CUDA/ROCm choice.
	path := writeBackendMeta(t, "cuda")
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetPath(path)
	ctl.SetGPUFlavor(ds4api.GPUFlavorROCm)

	if got := DetectLibraryBackend(lib); got != BackendROCm {
		t.Errorf("DetectLibraryBackend(ROCm lib, cuda metadata) = %v, want rocm", got)
	}
	withDefaultLibrary(t, lib)
	if got := DetectDefaultBackend(path); got != BackendROCm {
		t.Errorf("DetectDefaultBackend with that library loaded = %v, want rocm", got)
	}
	// A different, unloaded library path still uses its own metadata.
	if got := DetectDefaultBackend(writeBackendMeta(t, "cuda")); got != BackendCUDA {
		t.Errorf("unloaded path = %v, want cuda", got)
	}

	// Metal and CPU guesses are never replaced by the GPU flavor.
	cpuPath := writeBackendMeta(t, "cpu")
	ctl.SetPath(cpuPath)
	if got := DetectLibraryBackend(lib); got != BackendCPU {
		t.Errorf("CPU metadata with a ROCm-flavored lib = %v, want cpu", got)
	}

	// An unknown flavor keeps the guess.
	ctl.SetPath(writeBackendMeta(t, "rocm"))
	ctl.SetGPUFlavor(ds4api.GPUFlavorUnknown)
	if got := DetectLibraryBackend(lib); got != BackendROCm {
		t.Errorf("unknown flavor = %v, want the rocm guess", got)
	}
}

func TestCheckBackendCombinesFlavorAndArch(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetGPUFlavor(ds4api.GPUFlavorCUDA)
	var mm *BackendMismatchError
	if err := CheckBackend(lib, BackendROCm); !errors.As(err, &mm) {
		t.Fatalf("ROCm on a CUDA library: %v, want *BackendMismatchError", err)
	}
	if err := CheckBackend(lib, BackendCUDA); err != nil {
		t.Fatalf("CUDA on a CUDA library: %v", err)
	}

	// On a ROCm library the arch check runs too; the mock's path is not a
	// readable ELF, so CheckGPUArch reports unknown and does not block.
	ctl.SetGPUFlavor(ds4api.GPUFlavorROCm)
	ctl.SetPath(filepath.Join(t.TempDir(), "missing.so"))
	if err := CheckBackend(lib, BackendROCm); err != nil {
		t.Fatalf("ROCm on a ROCm library: %v", err)
	}
	if BackendROCm.String() != "rocm" || ds4api.BackendName(BackendROCm) != "rocm" {
		t.Errorf("ROCm names: String=%q BackendName=%q", BackendROCm.String(), ds4api.BackendName(BackendROCm))
	}
}
