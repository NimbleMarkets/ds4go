package ds4

import (
	"os"
	"path/filepath"
	"runtime"
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

// DefaultLibraryPath returns the preferred libds4 shared-library path.
//
// Search order is DS4_LIB, DS4_DIR/lib, executable-local paths, working
// directory paths, and finally the platform library name for system loader
// lookup.
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
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, name), filepath.Join(cwd, "lib", name))
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
