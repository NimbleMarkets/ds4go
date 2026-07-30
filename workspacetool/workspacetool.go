// Package workspacetool provides optional local workspace tools for ds4go
// tool loops.
package workspacetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	ds4 "github.com/NimbleMarkets/ds4go"
)

const (
	defaultReadLines       = 500
	defaultMaxReadBytes    = 4 << 20
	defaultMaxSearchBytes  = 1 << 20
	defaultMaxSearchResult = 50
	defaultMaxOutputBytes  = 64 << 10
	defaultListEntries     = 300
	defaultShellTimeout    = 60 * time.Second

	// jobStopGrace bounds how long a stop waits for a shell job to be reaped.
	// stop() escalates to SIGKILL, so exceeding this means the process is
	// unkillable (wedged in uninterruptible I/O) and waiting longer will not
	// help.
	jobStopGrace = 5 * time.Second
)

// ActionKind identifies a class of side effect.
type ActionKind string

const (
	ActionWrite ActionKind = "write"
	ActionEdit  ActionKind = "edit"
	ActionShell ActionKind = "shell"
)

// Action describes a side-effecting operation before it is executed.
type Action struct {
	Kind    ActionKind
	Path    string
	Command string
}

// Config controls workspace tool behavior and safety policy.
type Config struct {
	// Root is the workspace root. Relative paths resolve under this directory.
	// It defaults to the current working directory.
	Root string

	// AllowWrite enables write and edit tools.
	AllowWrite bool
	// AllowShell enables bash, bash_status, and bash_stop tools.
	AllowShell bool
	// AllowOutsideRoot permits paths outside Root.
	AllowOutsideRoot bool
	// FollowSymlinks allows paths that traverse symlink components.
	FollowSymlinks bool
	// LeaveChildProcesses makes shell stop/timeout/close target only the shell
	// process by default. The default is to stop child processes too on
	// platforms with process-group termination support.
	LeaveChildProcesses bool

	MaxReadBytes       int64
	MaxReadLines       int
	MaxSearchResults   int
	MaxSearchFileBytes int64
	MaxOutputBytes     int64
	MaxListEntries     int

	// Confirm is called before side-effecting operations when non-nil.
	Confirm func(context.Context, Action) (bool, error)

	// Env is the environment used for shell commands. It defaults to os.Environ.
	Env []string
	// Shell is the shell executable used by bash. It defaults to $SHELL or
	// /bin/sh on Unix, and cmd.exe on Windows.
	Shell string
}

// Workspace owns tool state for one local workspace.
type Workspace struct {
	cfg Config

	root   string
	osRoot *os.Root

	tmpDir  string
	tmpRoot *os.Root

	mu      sync.Mutex
	more    moreState
	jobs    map[int]*bashJob
	nextJob int
}

type moreState struct {
	path     string
	nextLine int
	raw      bool
	valid    bool
}

// New creates a Workspace tool provider.
func New(cfg Config) (*Workspace, error) {
	root := cfg.Root
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = wd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// A root handle confines all in-root file I/O so it cannot escape via
	// symlink, even under concurrent swaps (finding: symlink TOCTOU).
	osRoot, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, err
	}
	if cfg.MaxReadBytes <= 0 {
		cfg.MaxReadBytes = defaultMaxReadBytes
	}
	if cfg.MaxReadLines <= 0 {
		cfg.MaxReadLines = defaultReadLines
	}
	if cfg.MaxSearchResults <= 0 {
		cfg.MaxSearchResults = defaultMaxSearchResult
	}
	if cfg.MaxSearchFileBytes <= 0 {
		cfg.MaxSearchFileBytes = defaultMaxSearchBytes
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = defaultMaxOutputBytes
	}
	if cfg.MaxListEntries <= 0 {
		cfg.MaxListEntries = defaultListEntries
	}
	if cfg.Shell == "" {
		cfg.Shell = defaultShell()
	}
	return &Workspace{
		cfg:     cfg,
		root:    absRoot,
		osRoot:  osRoot,
		jobs:    make(map[int]*bashJob),
		nextJob: 1,
	}, nil
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		if s := os.Getenv("COMSPEC"); s != "" {
			return s
		}
		return "cmd.exe"
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// Close stops any running shell jobs and removes temporary output files.
func (w *Workspace) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	jobs := make([]*bashJob, 0, len(w.jobs))
	for _, job := range w.jobs {
		jobs = append(jobs, job)
	}
	w.jobs = make(map[int]*bashJob)
	tmpDir := w.tmpDir
	w.tmpDir = ""
	tmpRoot := w.tmpRoot
	w.tmpRoot = nil
	w.mu.Unlock()
	var stuck []int
	for _, job := range jobs {
		job.stop(!w.cfg.LeaveChildProcesses)
		if !job.wait(jobStopGrace) {
			stuck = append(stuck, job.id)
		}
	}
	var errs []error
	if len(stuck) > 0 {
		// Teardown always completes, but an unkillable job is reported rather
		// than silently abandoned: its output file is about to be removed.
		// Jobs come from map iteration, so sort for a stable message.
		slices.Sort(stuck)
		errs = append(errs, fmt.Errorf("workspacetool: shell jobs %v did not exit within %s", stuck, jobStopGrace))
	}
	if tmpRoot != nil {
		_ = tmpRoot.Close()
	}
	if w.osRoot != nil {
		_ = w.osRoot.Close()
	}
	if tmpDir != "" {
		if err := os.RemoveAll(tmpDir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RegisterReadOnly registers read, more, list, and search.
func (w *Workspace) RegisterReadOnly(reg *ds4.ToolRegistry) error {
	if reg == nil {
		return errors.New("workspacetool: nil registry")
	}
	for _, tool := range []ds4.ToolHandler{
		w.ReadTool(),
		w.MoreTool(),
		w.ListTool(),
		w.SearchTool(),
	} {
		if err := reg.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

// RegisterEditing registers read-only tools plus write and edit. It fails
// unless AllowWrite is enabled, so registered tools are always usable.
func (w *Workspace) RegisterEditing(reg *ds4.ToolRegistry) error {
	if !w.cfg.AllowWrite {
		return errors.New("workspacetool: RegisterEditing requires AllowWrite")
	}
	if err := w.RegisterReadOnly(reg); err != nil {
		return err
	}
	if err := reg.Register(w.WriteTool()); err != nil {
		return err
	}
	return reg.Register(w.EditTool())
}

// RegisterShell registers bash, bash_status, and bash_stop. It fails unless
// AllowShell is enabled, so registered tools are always usable.
func (w *Workspace) RegisterShell(reg *ds4.ToolRegistry) error {
	if reg == nil {
		return errors.New("workspacetool: nil registry")
	}
	if !w.cfg.AllowShell {
		return errors.New("workspacetool: RegisterShell requires AllowShell")
	}
	for _, tool := range []ds4.ToolHandler{
		w.BashTool(),
		w.BashStatusTool(),
		w.BashStopTool(),
	} {
		if err := reg.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

// RegisterAll registers the read-only tools plus every tool group the
// configuration permits: write and edit when AllowWrite is set, and the shell
// tools when AllowShell is set. Disabled groups are not advertised.
func (w *Workspace) RegisterAll(reg *ds4.ToolRegistry) error {
	if err := w.RegisterReadOnly(reg); err != nil {
		return err
	}
	if w.cfg.AllowWrite {
		if err := reg.Register(w.WriteTool()); err != nil {
			return err
		}
		if err := reg.Register(w.EditTool()); err != nil {
			return err
		}
	}
	if w.cfg.AllowShell {
		return w.RegisterShell(reg)
	}
	return nil
}

// fatalToolError marks a failure that must abort the tool loop instead of
// being returned to the model as an observation.
type fatalToolError struct{ err error }

func (e fatalToolError) Error() string { return e.err.Error() }
func (e fatalToolError) Unwrap() error { return e.err }

func (w *Workspace) confirm(ctx context.Context, action Action) error {
	if w.cfg.Confirm == nil {
		return nil
	}
	ok, err := w.cfg.Confirm(ctx, action)
	if err != nil {
		return fatalToolError{err}
	}
	if !ok {
		return fmt.Errorf("%s denied", action.Kind)
	}
	return nil
}

func newTool[A any](schema ds4.ToolSchema, run func(context.Context, A) (string, error)) ds4.ToolHandler {
	return ds4.Tool{ToolSchema: schema, Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		var a A
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &a); err != nil {
				return fmt.Sprintf("ERROR: %s: bad args: %v\n", schema.Name, err), nil
			}
		}
		out, err := run(ctx, a)
		// A context canceled mid-run (e.g. while polling a shell job) must
		// abort the tool loop, even when the handler itself returned no error.
		if ctx != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err == nil {
			return out, nil
		}
		var fatal fatalToolError
		if errors.As(err, &fatal) {
			return "", fatal.err
		}
		// Routine failures become observations so the model can correct
		// itself; a returned error aborts the entire tool loop.
		return "ERROR: " + err.Error() + "\n", nil
	}}
}
