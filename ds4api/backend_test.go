package ds4api

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBackendString(t *testing.T) {
	cases := map[Backend]string{
		BackendMetal: "metal",
		BackendCUDA:  "cuda",
		BackendROCm:  "rocm",
		BackendCPU:   "cpu",
		Backend(9):   "backend(9)",
	}
	for b, want := range cases {
		if got := b.String(); got != want {
			t.Errorf("Backend(%d).String() = %q, want %q", int(b), got, want)
		}
	}
	if BackendROCm == BackendCUDA {
		t.Fatal("BackendROCm shares BackendCUDA's value")
	}
}

func TestBackendABITranslation(t *testing.T) {
	cases := map[Backend]int32{BackendMetal: 0, BackendCUDA: 1, BackendCPU: 2, BackendROCm: 1}
	for b, want := range cases {
		if got := b.abi(); got != want {
			t.Errorf("%v.abi() = %d, want %d", b, got, want)
		}
	}
}

func TestBackendNameROCmNeedsNoLibrary(t *testing.T) {
	prev := CurrentDefaultLibrary()
	SetDefaultLibrary(nil)
	t.Cleanup(func() { SetDefaultLibrary(prev) })
	t.Setenv("DS4_LIB", "/nonexistent/libds4.so")
	if got := BackendName(BackendROCm); got != "rocm" {
		t.Fatalf("BackendName(BackendROCm) = %q, want rocm", got)
	}
}

func TestBackendNameAsksLibraryForCUDASlot(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetGPUFlavor(GPUFlavorROCm)
	prev := CurrentDefaultLibrary()
	SetDefaultLibrary(lib)
	t.Cleanup(func() { SetDefaultLibrary(prev) })
	if got := BackendName(BackendCUDA); got != "rocm" {
		t.Errorf("BackendName(BackendCUDA) on a ROCm library = %q, want rocm", got)
	}
	if got := BackendCUDA.String(); got != "cuda" {
		t.Errorf("BackendCUDA.String() = %q, want the fixed name cuda", got)
	}
}

func TestGPUFlavor(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	if got := lib.GPUFlavor(); got != GPUFlavorUnknown {
		t.Fatalf("default mock flavor = %q, want unknown", got)
	}
	for _, f := range []GPUFlavor{GPUFlavorCUDA, GPUFlavorROCm, GPUFlavorUnknown} {
		ctl.SetGPUFlavor(f)
		if got := lib.GPUFlavor(); got != f {
			t.Errorf("after SetGPUFlavor(%q) GPUFlavor() = %q", f, got)
		}
	}
	if b, ok := GPUFlavorROCm.Backend(); !ok || b != BackendROCm {
		t.Errorf("GPUFlavorROCm.Backend() = %v, %v", b, ok)
	}
	if b, ok := GPUFlavorCUDA.Backend(); !ok || b != BackendCUDA {
		t.Errorf("GPUFlavorCUDA.Backend() = %v, %v", b, ok)
	}
	if _, ok := GPUFlavorUnknown.Backend(); ok {
		t.Error("GPUFlavorUnknown.Backend() reported ok")
	}
	var nilLib *Library
	if nilLib.GPUFlavor() != GPUFlavorUnknown {
		t.Error("nil Library has a flavor")
	}
}

// captureDeprecations swaps the deprecation logger for the test.
func captureDeprecations(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := deprecationLogf
	deprecationLogf = func(format string, args ...any) { got = append(got, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { deprecationLogf = prev })
	return &got
}

func TestNewEngineROCmTranslatesAtABI(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetGPUFlavor(GPUFlavorROCm)
	warnings := captureDeprecations(t)

	engine, err := lib.NewEngine(EngineOptions{ModelPath: "mock", Backend: BackendROCm})
	if err != nil {
		t.Fatalf("BackendROCm on a ROCm library: %v", err)
	}
	defer engine.Close()
	if got := lastMockEngineOptions().Backend; got != int32(BackendCUDA) {
		t.Errorf("C engine options backend = %d, want DS4_BACKEND_CUDA (%d)", got, BackendCUDA)
	}
	if len(*warnings) != 0 {
		t.Errorf("BackendROCm logged a deprecation: %v", *warnings)
	}

	engine.ContextMemoryEstimate(BackendROCm, 4096)
	if got := ctl.LastEstimateBackend(); got != int32(BackendCUDA) {
		t.Errorf("engine estimate sent backend %d, want %d", got, BackendCUDA)
	}
	engine.ContextMemoryEstimateWithPrefill(BackendROCm, 4096, 512)
	if got := ctl.LastEstimateBackend(); got != int32(BackendCUDA) {
		t.Errorf("engine prefill estimate sent backend %d, want %d", got, BackendCUDA)
	}

	prev := CurrentDefaultLibrary()
	SetDefaultLibrary(lib)
	t.Cleanup(func() { SetDefaultLibrary(prev) })
	ctl.recordEstimateBackend(-1)
	ContextMemoryEstimate(BackendROCm, 4096)
	if got := ctl.LastEstimateBackend(); got != int32(BackendCUDA) {
		t.Errorf("package estimate sent backend %d, want %d", got, BackendCUDA)
	}
	ctl.recordEstimateBackend(-1)
	ContextMemoryEstimateWithPrefill(BackendROCm, 4096, 512)
	if got := ctl.LastEstimateBackend(); got != int32(BackendCUDA) {
		t.Errorf("package prefill estimate sent backend %d, want %d", got, BackendCUDA)
	}
	// Non-GPU backends pass through unchanged.
	ContextMemoryEstimate(BackendCPU, 4096)
	if got := ctl.LastEstimateBackend(); got != int32(BackendCPU) {
		t.Errorf("CPU estimate sent backend %d, want %d", got, BackendCPU)
	}
}

func TestNewEngineROCmOnCUDALibraryFails(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetGPUFlavor(GPUFlavorCUDA)
	engine, err := lib.NewEngine(EngineOptions{ModelPath: "mock", Backend: BackendROCm})
	if err == nil {
		engine.Close()
		t.Fatal("BackendROCm on a CUDA library opened an engine")
	}
	var mm *BackendMismatchError
	if !errors.As(err, &mm) || mm.Requested != BackendROCm || mm.Flavor != GPUFlavorCUDA || mm.Library != "mock" {
		t.Fatalf("error = %#v, want *BackendMismatchError{mock, rocm, cuda}", err)
	}
	for _, want := range []string{"rocm", "mock", "not a ROCm build", "--backend rocm"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestNewEngineCUDAOnROCmLibraryWarnsOnce(t *testing.T) {
	lib, ctl := NewMockLibraryWithControls()
	ctl.SetGPUFlavor(GPUFlavorROCm)
	warnings := captureDeprecations(t)
	for i := 0; i < 2; i++ {
		engine, err := lib.NewEngine(EngineOptions{ModelPath: "mock", Backend: BackendCUDA})
		if err != nil {
			t.Fatalf("BackendCUDA on a ROCm library must still open during the transition: %v", err)
		}
		engine.Close()
	}
	if len(*warnings) != 1 {
		t.Fatalf("deprecation warnings = %d (%v), want exactly 1", len(*warnings), *warnings)
	}
	if !strings.Contains((*warnings)[0], "BackendROCm") || !strings.Contains((*warnings)[0], "mock") {
		t.Errorf("warning %q should name BackendROCm and the library", (*warnings)[0])
	}
}

func TestCheckBackendPassThrough(t *testing.T) {
	warnings := captureDeprecations(t)
	lib, ctl := NewMockLibraryWithControls()
	for _, f := range []GPUFlavor{GPUFlavorUnknown, GPUFlavorCUDA, GPUFlavorROCm} {
		ctl.SetGPUFlavor(f)
		for _, b := range []Backend{BackendMetal, BackendCPU} {
			if err := lib.CheckBackend(b); err != nil {
				t.Errorf("flavor %q, %v: %v", f, b, err)
			}
		}
	}
	ctl.SetGPUFlavor(GPUFlavorUnknown)
	for _, b := range []Backend{BackendCUDA, BackendROCm} {
		if err := lib.CheckBackend(b); err != nil {
			t.Errorf("unknown flavor must not block %v: %v", b, err)
		}
	}
	ctl.SetGPUFlavor(GPUFlavorCUDA)
	if err := lib.CheckBackend(BackendCUDA); err != nil {
		t.Errorf("CUDA on CUDA: %v", err)
	}
	if len(*warnings) != 0 {
		t.Errorf("unexpected warnings: %v", *warnings)
	}
}
