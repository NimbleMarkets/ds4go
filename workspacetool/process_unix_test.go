//go:build !windows

package workspacetool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestBashStopKillsChildProcessesByDefault(t *testing.T) {
	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root, AllowShell: true})
	childPath := filepath.Join(root, "child.pid")
	out, err := invoke(t, w.BashTool(), `{"command":"sleep 30 & echo $! > child.pid; wait","timeout_sec":60,"refresh_sec":0.2}`)
	if err != nil {
		t.Fatal(err)
	}
	job := parseBashJobID(t, out)
	child := readPIDFile(t, childPath)

	if _, err := invoke(t, w.BashStopTool(), `{"job":`+strconv.Itoa(job)+`}`); err != nil {
		t.Fatal(err)
	}
	if processExistsEventually(child, false) {
		_ = syscall.Kill(child, syscall.SIGKILL)
		t.Fatalf("child process %d still exists after bash_stop", child)
	}
}

func TestBashStopKillsTermResistantChild(t *testing.T) {
	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root, AllowShell: true})
	childPath := filepath.Join(root, "child.pid")
	// The child ignores SIGTERM, so a graceful stop of the shell alone would
	// leave it running; bash_stop must escalate to SIGKILL on the group.
	out, err := invoke(t, w.BashTool(),
		`{"command":"sh -c 'trap \"\" TERM; echo $$ > child.pid; sleep 30' & wait","timeout_sec":60,"refresh_sec":0.2}`)
	if err != nil {
		t.Fatal(err)
	}
	job := parseBashJobID(t, out)
	child := readPIDFile(t, childPath)

	if _, err := invoke(t, w.BashStopTool(), `{"job":`+strconv.Itoa(job)+`}`); err != nil {
		t.Fatal(err)
	}
	if processExistsEventually(child, false) {
		_ = syscall.Kill(child, syscall.SIGKILL)
		t.Fatalf("TERM-resistant child %d still exists after bash_stop", child)
	}
}

func TestShellGroupNotSignaledAfterReap(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	orig := killShellGroup
	killShellGroup = func(cmd *exec.Cmd) {
		mu.Lock()
		calls++
		mu.Unlock()
		orig(cmd)
	}
	t.Cleanup(func() { killShellGroup = orig })

	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root, AllowShell: true})
	// The command completes and is reaped, but stays registered.
	out, err := invoke(t, w.BashTool(), `{"command":"printf hi","timeout_sec":10,"refresh_sec":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status=done") {
		t.Fatalf("bash output = %q", out)
	}
	job := parseBashJobID(t, out)

	// The shell is already reaped, so its process-group id may have been reused.
	// Cleanup must never raw-signal it, not even once.
	if _, err := invoke(t, w.BashStopTool(), `{"job":`+strconv.Itoa(job)+`,"kill_children":true}`); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 0 {
		t.Fatalf("process group signaled %d times after the shell was reaped; a reaped group must never be raw-signaled", n)
	}
}

// TestBashStopLeavesChildThatOutlivedShell documents the deliberate limitation
// of the safe cleanup model: once a shell has exited and been reaped, its
// process group is not raw-signaled (the id may have been reused), so a
// backgrounded child that outlived the shell is left running.
func TestBashStopLeavesChildThatOutlivedShell(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	orig := killShellGroup
	killShellGroup = func(cmd *exec.Cmd) {
		mu.Lock()
		calls++
		mu.Unlock()
		orig(cmd)
	}
	t.Cleanup(func() { killShellGroup = orig })

	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root, AllowShell: true})
	childPath := filepath.Join(root, "child.pid")
	// No wait: the shell backgrounds the child and exits immediately, so the
	// job is reaped while the child lingers.
	out, err := invoke(t, w.BashTool(),
		`{"command":"sleep 30 & echo $! > child.pid","timeout_sec":60,"refresh_sec":0.2}`)
	if err != nil {
		t.Fatal(err)
	}
	job := parseBashJobID(t, out)
	child := readPIDFile(t, childPath)
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	if _, err := invoke(t, w.BashStopTool(), `{"job":`+strconv.Itoa(job)+`,"kill_children":true}`); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 0 {
		t.Fatalf("reaped shell's group was raw-signaled %d times; post-exit cleanup must skip it", n)
	}
	if !processExistsEventually(child, true) {
		t.Fatalf("child %d was killed via a post-exit group signal; the safe model must leave it", child)
	}
}

func TestBashCancellationStopsJob(t *testing.T) {
	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root, AllowShell: true})
	pidPath := filepath.Join(root, "shell.pid")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			w.mu.Lock()
			n := len(w.jobs)
			w.mu.Unlock()
			if n > 0 {
				time.Sleep(20 * time.Millisecond)
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	_, err := w.BashTool().Invoke(ctx,
		json.RawMessage(`{"command":"echo $$ > shell.pid; sleep 30","timeout_sec":60,"refresh_sec":9}`))
	if err == nil {
		t.Fatal("expected cancellation to be reported as an error")
	}
	shellPID := readPIDFile(t, pidPath)
	if processExistsEventually(shellPID, false) {
		_ = syscall.Kill(shellPID, syscall.SIGKILL)
		t.Fatalf("canceled command %d kept running", shellPID)
	}
}

func TestBashStopCanLeaveChildProcesses(t *testing.T) {
	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root, AllowShell: true})
	childPath := filepath.Join(root, "child.pid")
	out, err := invoke(t, w.BashTool(), `{"command":"sleep 30 & echo $! > child.pid; wait","timeout_sec":60,"refresh_sec":0.2}`)
	if err != nil {
		t.Fatal(err)
	}
	job := parseBashJobID(t, out)
	child := readPIDFile(t, childPath)

	if _, err := invoke(t, w.BashStopTool(), `{"job":`+strconv.Itoa(job)+`,"kill_children":false}`); err != nil {
		t.Fatal(err)
	}
	if !processExistsEventually(child, true) {
		t.Fatalf("child process %d was stopped despite kill_children=false", child)
	}
	_ = syscall.Kill(child, syscall.SIGKILL)
}

func parseBashJobID(t *testing.T, out string) int {
	t.Helper()
	re := regexp.MustCompile(`bash job=([0-9]+) `)
	m := re.FindStringSubmatch(out)
	if len(m) != 2 {
		t.Fatalf("missing job id in output: %q", out)
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	var data []byte
	var err error
	for i := 0; i < 20; i++ {
		data, err = os.ReadFile(path)
		if err == nil && len(strings.TrimSpace(string(data))) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func processExistsEventually(pid int, want bool) bool {
	for i := 0; i < 20; i++ {
		exists := syscall.Kill(pid, 0) == nil
		if exists == want {
			return exists
		}
		time.Sleep(50 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) == nil
}
