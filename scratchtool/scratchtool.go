package scratchtool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	ds4 "github.com/NimbleMarkets/ds4go"
)

// SystemHint is an optional line callers can prepend to the system prompt
// when the scratch tools are registered.
const SystemHint = "You have a session scratchpad (scratch_list/get/set/append/delete). Use it for plans and intermediate notes. Do not paste the pad into the user reply unless asked."

const (
	defaultSession       = "default"
	defaultMaxKeys       = 32
	defaultMaxValueBytes = 16 << 10
	defaultMaxTotalBytes = 128 << 10
)

// Config controls scratchpad storage and limits.
type Config struct {
	// Dir is the session directory. Empty → DefaultDir(Session).
	Dir string
	// Session names the pad namespace. Empty → "default".
	// Must pass the same key rules as pad keys.
	Session string
	// MaxKeys default 32.
	MaxKeys int
	// MaxValueBytes default 16KiB.
	MaxValueBytes int
	// MaxTotalBytes default 128KiB.
	MaxTotalBytes int
	// ReadOnly registers get/list only.
	ReadOnly bool
}

// DefaultDir returns the default session directory for a scratchpad session:
// the "scratch/<session>" subdirectory of the ds4go data directory
// (DS4_DIR, default ~/.ds4). An empty session means "default".
func DefaultDir(session string) string {
	if session == "" {
		session = defaultSession
	}
	return filepath.Join(ds4.DefaultDir(), "scratch", session)
}

// Store is one session's key/value scratchpad, persisted one file per key.
type Store struct {
	cfg Config
	dir string
	// root confines all per-key file I/O to the session directory so a
	// planted symlink cannot reach outside it, even under concurrent path
	// swaps (workspacetool's symlink-TOCTOU pattern).
	root *os.Root

	mu sync.Mutex
}

// New creates the session directory if needed and returns a Store for it.
//
// The session name must pass the same rules as pad keys, so a session can
// never escape the scratch base directory.
func New(cfg Config) (*Store, error) {
	if cfg.Session == "" {
		cfg.Session = defaultSession
	}
	if err := validateKey(cfg.Session); err != nil {
		return nil, fmt.Errorf("scratchtool: bad session name: %w", err)
	}
	dir := cfg.Dir
	if dir == "" {
		dir = DefaultDir(cfg.Session)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("scratchtool: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return nil, fmt.Errorf("scratchtool: %w", err)
	}
	root, err := os.OpenRoot(absDir)
	if err != nil {
		return nil, fmt.Errorf("scratchtool: %w", err)
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = defaultMaxKeys
	}
	if cfg.MaxValueBytes <= 0 {
		cfg.MaxValueBytes = defaultMaxValueBytes
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = defaultMaxTotalBytes
	}
	return &Store{cfg: cfg, dir: absDir, root: root}, nil
}

// Close releases the session-directory handle. Every mutation is already
// durable via write-temp-and-rename, so Close flushes nothing.
func (s *Store) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

// Register adds scratch_list, scratch_get, scratch_set, scratch_append, and
// scratch_delete to reg. It fails when Config.ReadOnly is set, so registered
// tools are always usable; use RegisterReadOnly instead.
func (s *Store) Register(reg *ds4.ToolRegistry) error {
	if reg == nil {
		return errors.New("scratchtool: nil registry")
	}
	if s.cfg.ReadOnly {
		return errors.New("scratchtool: Register requires a writable store; use RegisterReadOnly")
	}
	for _, tool := range []ds4.ToolHandler{
		s.listTool(),
		s.getTool(),
		s.setTool(),
		s.appendTool(),
		s.deleteTool(),
	} {
		if err := reg.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

// RegisterReadOnly adds scratch_list and scratch_get to reg.
func (s *Store) RegisterReadOnly(reg *ds4.ToolRegistry) error {
	if reg == nil {
		return errors.New("scratchtool: nil registry")
	}
	for _, tool := range []ds4.ToolHandler{
		s.listTool(),
		s.getTool(),
	} {
		if err := reg.Register(tool); err != nil {
			return err
		}
	}
	return nil
}
