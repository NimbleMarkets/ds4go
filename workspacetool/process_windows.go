//go:build windows

package workspacetool

import (
	"os/exec"
	"time"
)

func prepareShellCommand(cmd *exec.Cmd) {}

// killShellGroup best-effort terminates the shell. Windows has no POSIX process
// groups here, so child processes are stopped only on a best-effort basis. It
// is a package var so tests can observe group-kill attempts.
var killShellGroup = func(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// terminateShell stops the shell process. Windows lacks SIGTERM, so this is a
// direct kill; the os.Process API is safe against PID reuse.
func terminateShell(cmd *exec.Cmd, wait func(time.Duration) bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
