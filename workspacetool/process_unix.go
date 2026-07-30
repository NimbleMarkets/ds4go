//go:build !windows

package workspacetool

import (
	"os/exec"
	"syscall"
	"time"
)

func prepareShellCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killShellGroup force-kills the shell's entire process group. It is a raw
// group signal with no PID-reuse guard: it is race-free only while the group is
// non-empty (its id cannot then be reused). Once the shell has exited and any
// children are gone, the group id may belong to an unrelated process, so a
// caller invoking this after exit accepts a best-effort, racy outcome. It is a
// package var so tests can observe group-kill attempts.
var killShellGroup = func(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// terminateShell stops just the shell process, not its group. It uses the
// os.Process API, which refuses to signal a process that has already been
// reaped, so it is safe against PID reuse.
func terminateShell(cmd *exec.Cmd, wait func(time.Duration) bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	if wait(time.Second) {
		return
	}
	_ = cmd.Process.Kill()
}
