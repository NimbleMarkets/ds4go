package ds4

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

// DefaultDir returns the ds4go data directory.
//
// DS4_DIR overrides the default. When DS4_DIR is unset, DefaultDir returns
// "$HOME/.ds4" when the user home directory can be determined, otherwise ".ds4".
func DefaultDir() string {
	if dir := os.Getenv("DS4_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ds4")
	}
	return ".ds4"
}

// DefaultLibraryDir returns the directory where libds4 is installed by
// default: the "lib" subdirectory of DefaultDir.
func DefaultLibraryDir() string {
	return filepath.Join(DefaultDir(), "lib")
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

// DefaultLibraryPath returns the preferred libds4 shared-library path.
//
// Search order is DS4_LIB, DS4_DIR/lib, executable-local paths, and finally
// the platform library name for system loader lookup.
//
// The current working directory is deliberately NOT searched: loading a
// shared library from the CWD would let an attacker who can write a file
// into a directory the user happens to run ds4go from plant a malicious
// libds4 and gain code execution (binary planting). Use DS4_LIB or DS4_DIR
// to load a library from a non-default location.
func DefaultLibraryPath() string {
	if path := os.Getenv("DS4_LIB"); path != "" {
		return path
	}

	name := libraryFileName()
	var candidates []string
	if ds4Dir := DefaultDir(); ds4Dir != "" {
		candidates = append(candidates, filepath.Join(ds4Dir, "lib", name))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, name), filepath.Join(dir, "lib", name))
	}
	candidates = append(candidates, name)

	for _, candidate := range candidates {
		if candidate == name {
			return candidate
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
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
