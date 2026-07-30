package workspacetool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type accessMode int

const (
	accessRead accessMode = iota
	accessWrite
)

// pathTarget is a validated path plus how to access it. When root is non-nil,
// operations go through that *os.Root and cannot escape it even under
// concurrent symlink swaps; rel is the root-relative name. When root is nil the
// caller opted out of confinement (AllowOutsideRoot) and abs is used directly.
type pathTarget struct {
	abs  string
	rel  string
	root *os.Root
}

func (t pathTarget) open() (*os.File, error) {
	if t.root != nil {
		return t.root.Open(t.rel)
	}
	return os.Open(t.abs)
}

func (t pathTarget) readFile() ([]byte, error) {
	if t.root != nil {
		return t.root.ReadFile(t.rel)
	}
	return os.ReadFile(t.abs)
}

func (t pathTarget) writeFile(data []byte, perm os.FileMode) error {
	if t.root != nil {
		return t.root.WriteFile(t.rel, data, perm)
	}
	return os.WriteFile(t.abs, data, perm)
}

func (t pathTarget) readDir() ([]os.DirEntry, error) {
	if t.root == nil {
		return os.ReadDir(t.abs)
	}
	f, err := t.root.Open(t.rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

func (w *Workspace) resolvePath(userPath string, mode accessMode) (pathTarget, error) {
	if userPath == "" {
		return pathTarget{}, fmt.Errorf("path is required")
	}
	if mode == accessWrite && !w.cfg.AllowWrite {
		return pathTarget{}, fmt.Errorf("write access is disabled")
	}

	var p string
	if filepath.IsAbs(userPath) {
		p = filepath.Clean(userPath)
	} else {
		p = filepath.Join(w.root, userPath)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return pathTarget{}, err
	}
	abs = filepath.Clean(abs)

	// Shell job output files live in the workspace-owned temp dir and are
	// advertised to the model via output_path, so reads must reach them.
	if mode == accessRead {
		if t, ok := w.jobOutputTarget(abs); ok {
			return t, nil
		}
	}

	if w.cfg.AllowOutsideRoot {
		// The caller trusts absolute paths; only enforce the no-symlink policy
		// on the target itself so symlinked ancestors like /tmp still resolve.
		if !w.cfg.FollowSymlinks {
			if st, err := os.Lstat(abs); err == nil && st.Mode()&os.ModeSymlink != 0 {
				return pathTarget{}, fmt.Errorf("path is a symlink: %s", userPath)
			}
		}
		return pathTarget{abs: abs}, nil
	}

	rel, err := filepath.Rel(w.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return pathTarget{}, fmt.Errorf("path escapes workspace root: %s", userPath)
	}
	// Policy pre-check for a clear error when symlinks are disallowed; os.Root
	// is the authoritative guard, so a raced check cannot escape the root.
	if !w.cfg.FollowSymlinks {
		if err := rejectSymlinkComponentFrom(w.root, abs); err != nil {
			return pathTarget{}, err
		}
	}
	return pathTarget{abs: abs, rel: rel, root: w.osRoot}, nil
}

// jobOutputTarget returns a read target for a path inside the workspace-owned
// temp dir, routed through the temp-dir root so it stays confined there.
func (w *Workspace) jobOutputTarget(abs string) (pathTarget, bool) {
	w.mu.Lock()
	tmpDir, tmpRoot := w.tmpDir, w.tmpRoot
	w.mu.Unlock()
	if tmpDir == "" || tmpRoot == nil || !pathWithin(tmpDir, abs) {
		return pathTarget{}, false
	}
	rel, err := filepath.Rel(tmpDir, abs)
	if err != nil {
		return pathTarget{}, false
	}
	return pathTarget{abs: abs, rel: rel, root: tmpRoot}, true
}

func rejectSymlinkComponentFrom(root, abs string) error {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	cur := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return nil
		}
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path uses symlink component: %s", cur)
		}
	}
	return nil
}

func pathWithin(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func isBinary(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func clampInt(v, def, min, max int) int {
	if v <= 0 {
		v = def
	}
	if v < min {
		return min
	}
	if max > 0 && v > max {
		return max
	}
	return v
}
