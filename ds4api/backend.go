package ds4api

import (
	"fmt"
	"log"
	"strconv"
)

// String returns a fixed name for b: "metal", "cuda", "rocm", "cpu", or
// "backend(N)". Unlike [BackendName] it never consults a library, so a ROCm
// libds4 does not rename BackendCUDA here.
func (b Backend) String() string {
	switch b {
	case BackendMetal:
		return "metal"
	case BackendCUDA:
		return "cuda"
	case BackendROCm:
		return "rocm"
	case BackendCPU:
		return "cpu"
	}
	return "backend(" + strconv.Itoa(int(b)) + ")"
}

// abi returns the C ds4_backend value for b. libds4 has no ROCm entry: a
// ROCm build serves DS4_BACKEND_CUDA, so the Go-only BackendROCm crosses the
// ABI as BackendCUDA. Every raw call taking a backend must go through this.
func (b Backend) abi() int32 {
	if b == BackendROCm {
		return int32(BackendCUDA)
	}
	return int32(b)
}

// GPUFlavor names the GPU runtime a libds4 build compiled behind the shared
// DS4_BACKEND_CUDA slot.
type GPUFlavor string

const (
	// GPUFlavorUnknown means the library's name for the slot was not
	// recognized (for example a mock).
	GPUFlavorUnknown GPUFlavor = ""
	// GPUFlavorCUDA means the slot is not ROCm. libds4 names it "cuda" in
	// CUDA builds and also in Metal and CPU-only builds, which have no GPU
	// behind it, so this does not by itself prove CUDA support.
	GPUFlavorCUDA GPUFlavor = "cuda"
	// GPUFlavorROCm means the library is a ROCm build.
	GPUFlavorROCm GPUFlavor = "rocm"
)

// Backend returns the Go backend value that selects this flavor, and false
// for GPUFlavorUnknown.
func (f GPUFlavor) Backend() (Backend, bool) {
	switch f {
	case GPUFlavorCUDA:
		return BackendCUDA, true
	case GPUFlavorROCm:
		return BackendROCm, true
	}
	return 0, false
}

// parseGPUFlavor maps ds4_backend_name(DS4_BACKEND_CUDA) to a flavor.
func parseGPUFlavor(name string) GPUFlavor {
	switch name {
	case "cuda":
		return GPUFlavorCUDA
	case "rocm":
		return GPUFlavorROCm
	}
	return GPUFlavorUnknown
}

// GPUFlavor reports what the DS4_BACKEND_CUDA slot really is in this
// library, from ds4_backend_name(DS4_BACKEND_CUDA). It is read once at load.
func (l *Library) GPUFlavor() GPUFlavor {
	if l == nil {
		return GPUFlavorUnknown
	}
	return l.gpuFlavor
}

// cacheGPUFlavor records the library's flavor; call it once raw symbols are
// registered.
func (l *Library) cacheGPUFlavor() {
	if l.raw.ds4BackendName == nil {
		return
	}
	l.gpuFlavor = parseGPUFlavor(l.raw.ds4BackendName(int32(BackendCUDA)))
}

// BackendMismatchError reports a GPU backend request that the loaded libds4
// build cannot serve: BackendROCm on a non-ROCm library, or (after the
// transition release) BackendCUDA on a ROCm library.
type BackendMismatchError struct {
	Library   string    // path of the loaded libds4
	Requested Backend   // BackendCUDA or BackendROCm
	Flavor    GPUFlavor // what the library serves
}

func (e *BackendMismatchError) Error() string {
	if e.Requested == BackendROCm {
		return fmt.Sprintf("ds4: backend rocm requested, but %s is not a ROCm build of libds4 "+
			"(it names its GPU slot %q); load a ROCm libds4 with DS4_LIB or 'ds4go install --backend rocm'",
			e.Library, string(e.Flavor))
	}
	return fmt.Sprintf("ds4: backend %s requested, but %s is a %s build of libds4; "+
		"request backend %s, or load a %s libds4 with DS4_LIB or 'ds4go install --backend %s'",
		e.Requested, e.Library, e.Flavor, e.Flavor, e.Requested, e.Requested)
}

// deprecationLogf receives the one-time BackendCUDA-on-ROCm warning; tests
// replace it.
var deprecationLogf = log.Printf

// CheckBackend validates a backend request against the library's GPUFlavor.
// [Library.NewEngine] calls it before opening anything; call it directly to
// fail early, before loading a model.
//
// BackendROCm on a library that is not ROCm is an error. BackendCUDA on a
// ROCm library is still accepted for one transition release, because callers
// written before BackendROCm existed had to pass BackendCUDA for ROCm; it logs
// a deprecation warning once per library and will become an error. An
// unknown flavor is never rejected.
func (l *Library) CheckBackend(b Backend) error {
	flavor := l.GPUFlavor()
	switch {
	case b == BackendROCm && flavor == GPUFlavorCUDA:
		return &BackendMismatchError{Library: l.path, Requested: b, Flavor: flavor}
	case b == BackendCUDA && flavor == GPUFlavorROCm:
		l.cudaOnROCmWarn.Do(func() {
			deprecationLogf("ds4go: BackendCUDA selected %s, a ROCm build of libds4; this is deprecated "+
				"and a future release will reject it: use BackendROCm (--backend rocm)", l.path)
		})
	}
	return nil
}
