package ds4

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

// DefaultDir returns the ds4go data directory.
//
// DS4_DIR overrides the default. When DS4_DIR is unset, DefaultDir returns
// "$HOME/.ds4" when the user home directory can be determined, otherwise ".ds4".
//
// The ".ds4" fallback is relative to the working directory and is used only
// for data (models, config, scratch). It is never used to locate libds4:
// DefaultLibraryDir returns "" and DefaultLibraryPath skips the DS4_DIR/lib
// candidate in that case, because loading a shared library from a
// working-directory-relative path is the binary-planting vector described at
// DefaultLibraryPath. A shared location such as os.TempDir would be no
// better, since another local user can pre-create it. Set DS4_DIR to load a
// library when no home directory is available.
func DefaultDir() string {
	if dir, ok := resolveDefaultDir(); ok {
		return dir
	}
	return ".ds4"
}

// resolveDefaultDir returns the ds4go data directory from DS4_DIR or the
// user home directory. ok is false when neither is available, which is the
// only case in which DefaultDir falls back to the working-directory-relative
// ".ds4"; library lookups must not use that fallback.
func resolveDefaultDir() (dir string, ok bool) {
	if dir := os.Getenv("DS4_DIR"); dir != "" {
		return dir, true
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ds4"), true
	}
	return "", false
}

// DefaultLibraryDir returns the directory where libds4 is installed by
// default: the "lib" subdirectory of DefaultDir.
//
// It returns "" when neither DS4_DIR nor a home directory is available, since
// the library is never searched relative to the working directory (see
// DefaultDir and DefaultLibraryPath).
func DefaultLibraryDir() string {
	dir, ok := resolveDefaultDir()
	if !ok {
		return ""
	}
	return filepath.Join(dir, "lib")
}

// DefaultModelsDir returns the directory where downloaded models are stored:
// the "models" subdirectory of DefaultDir.
func DefaultModelsDir() string {
	return filepath.Join(DefaultDir(), "models")
}

// DefaultModelPath returns the path to the default model symlink.
//
// The default model is a symlink at $DS4_DIR/models/<DefaultModelSymlink> that
// points to the active downloaded model. Use ds4go model set to switch it.
func DefaultModelPath() string {
	return filepath.Join(DefaultDir(), "models", models.DefaultModelSymlink)
}

// DefaultMTPPath returns the path to the installed MTP companion model,
// or empty string if it is not present.
func DefaultMTPPath() string {
	model, ok := models.Lookup(models.MTPAlias)
	if !ok {
		return ""
	}
	p := filepath.Join(DefaultDir(), "models", model.FileName)
	if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Size() > 0 {
		return p
	}
	return ""
}

// DefaultLibraryPath returns the preferred libds4 shared-library path, or ""
// when none is installed.
//
// Search order is DS4_LIB, DS4_DIR/lib, then executable-local paths.
//
// The current working directory is deliberately NOT searched: loading a
// shared library from the CWD would let an attacker who can write a file
// into a directory the user happens to run ds4go from plant a malicious
// libds4 and gain code execution (binary planting). For the same reason a
// bare library name is never returned for the OS loader to resolve: dyld
// and Windows LoadLibrary both search the working directory for a leaf
// name, and the DS4_DIR/lib candidate is skipped when DefaultDir would
// fall back to the working-directory-relative ".ds4" (no DS4_DIR and no
// home directory). Use DS4_LIB or DS4_DIR to load a library from a
// non-default location.
func DefaultLibraryPath() string {
	if path := os.Getenv("DS4_LIB"); path != "" {
		return path
	}
	for _, candidate := range defaultLibraryCandidates() {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

// defaultLibraryCandidates lists the paths DefaultLibraryPath probes after
// DS4_LIB, in order. None is relative to the working directory: the
// DS4_DIR/lib candidate is omitted when no absolute data directory resolves.
func defaultLibraryCandidates() []string {
	name := libraryFileName()
	var candidates []string
	if libDir := DefaultLibraryDir(); libDir != "" {
		candidates = append(candidates, filepath.Join(libDir, name))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, name), filepath.Join(dir, "lib", name))
	}
	return candidates
}

func libraryFileName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libds4.dylib"
	case "windows":
		return "libds4.dll"
	default:
		return "libds4.so"
	}
}

// installMetadataROCmArchs returns the rocm_archs list recorded in the
// ds4go-install.json beside libPath, or nil.
func installMetadataROCmArchs(libPath string) []string {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(libPath), "ds4go-install.json"))
	if err != nil {
		return nil
	}
	var meta struct {
		ROCmArchs []string `json:"rocm_archs"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return nil
	}
	return meta.ROCmArchs
}

// DetectDefaultBackend probes the environment and installation metadata to determine
// the preferred backend for the shared library at libPath.
//
// Passing an empty string probes using the default library path.
func DetectDefaultBackend(libPath string) Backend {
	resolvedPath := libPath
	if resolvedPath == "" {
		resolvedPath = DefaultLibraryPath()
	}
	if resolvedPath != "" {
		dir := filepath.Dir(resolvedPath)
		metaPath := filepath.Join(dir, "ds4go-install.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Backend string `json:"backend"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				switch strings.ToLower(meta.Backend) {
				case "metal":
					return BackendMetal
				case "cuda":
					return BackendCUDA
				case "rocm":
					return BackendCUDA
				case "cpu":
					return BackendCPU
				}
			}
		}
	}

	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return BackendMetal
	}
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/dev/nvidiactl"); err == nil {
			return BackendCUDA
		}
		if _, err := os.Stat("/dev/nvidia0"); err == nil {
			return BackendCUDA
		}
		if _, err := exec.LookPath("nvidia-smi"); err == nil {
			return BackendCUDA
		}
		if _, err := os.Stat("/dev/kfd"); err == nil {
			return BackendCUDA
		}
		if _, err := os.Stat("/opt/rocm"); err == nil {
			return BackendCUDA
		}
		return BackendCPU
	}
	return BackendCPU
}
