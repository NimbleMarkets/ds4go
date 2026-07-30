package workspacetool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	ds4 "github.com/NimbleMarkets/ds4go"
)

type bashArgs struct {
	Command    string  `json:"command"`
	TimeoutSec float64 `json:"timeout_sec"`
	RefreshSec float64 `json:"refresh_sec"`
}

type bashJobArgs struct {
	Job          int     `json:"job"`
	PID          int     `json:"pid"`
	RefreshSec   float64 `json:"refresh_sec"`
	KillChildren *bool   `json:"kill_children"`
}

type bashJob struct {
	id      int
	command string
	path    string
	cmd     *exec.Cmd
	started time.Time
	timeout time.Duration

	mu            sync.Mutex
	done          bool
	groupSignaled bool
	exitCode      int
	err           error
}

// BashTool returns a tool that runs a shell command.
func (w *Workspace) BashTool() ds4.ToolHandler {
	return newTool(schema("bash", "Run a shell command.", bashParams),
		func(ctx context.Context, a bashArgs) (string, error) {
			if !w.cfg.AllowShell {
				return "", fmt.Errorf("shell access is disabled")
			}
			if a.Command == "" {
				return "", fmt.Errorf("bash requires command")
			}
			if err := w.confirm(ctx, Action{Kind: ActionShell, Command: a.Command}); err != nil {
				return "", err
			}
			job, err := w.startBashJob(a.Command, durationSeconds(a.TimeoutSec, defaultShellTimeout))
			if err != nil {
				return "", err
			}
			w.waitForJob(ctx, job, durationSeconds(a.RefreshSec, 1*time.Second))
			if ctx != nil && ctx.Err() != nil {
				// The run is aborting; stop the command we just started rather
				// than orphaning it with an unreported job ID.
				job.stop(!w.cfg.LeaveChildProcesses)
				job.wait(jobStopGrace)
				w.dropJob(job.id)
				return "", ctx.Err()
			}
			return w.observeJob(job)
		})
}

// BashStatusTool returns a tool that reports status and output for a shell job.
func (w *Workspace) BashStatusTool() ds4.ToolHandler {
	return newTool(schema("bash_status", "Report current status and new output for a bash job.", bashStatusParams),
		func(ctx context.Context, a bashJobArgs) (string, error) {
			job, err := w.findJob(a.Job, a.PID)
			if err != nil {
				return "", err
			}
			if a.RefreshSec > 0 {
				w.waitForJob(ctx, job, durationSeconds(a.RefreshSec, 0))
			}
			return w.observeJob(job)
		})
}

// BashStopTool returns a tool that terminates a shell job.
func (w *Workspace) BashStopTool() ds4.ToolHandler {
	return newTool(schema("bash_stop", "Terminate a running bash job and report its final output.", bashStopParams),
		func(ctx context.Context, a bashJobArgs) (string, error) {
			_ = ctx
			job, err := w.findJob(a.Job, a.PID)
			if err != nil {
				return "", err
			}
			killChildren := !w.cfg.LeaveChildProcesses
			if a.KillChildren != nil {
				killChildren = *a.KillChildren
			}
			// A job that outlives the grace period is still reported: observeJob
			// renders its true status=running rather than claiming it stopped.
			job.stop(killChildren)
			job.wait(jobStopGrace)
			return w.observeJob(job)
		})
}

func (w *Workspace) startBashJob(command string, timeout time.Duration) (*bashJob, error) {
	tmpDir, err := w.ensureTempDir()
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(tmpDir, "bash-*.out")
	if err != nil {
		return nil, err
	}
	path := f.Name()

	args := shellArgs(command)
	cmd := exec.Command(w.cfg.Shell, args...)
	cmd.Dir = w.root
	if w.cfg.Env != nil {
		cmd.Env = w.cfg.Env
	} else {
		cmd.Env = os.Environ()
	}
	cmd.Stdout = f
	cmd.Stderr = f
	prepareShellCommand(cmd)

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	_ = f.Close()

	w.mu.Lock()
	id := w.nextJob
	w.nextJob++
	job := &bashJob{
		id:       id,
		command:  command,
		path:     path,
		cmd:      cmd,
		started:  time.Now(),
		timeout:  timeout,
		exitCode: -1,
	}
	w.jobs[id] = job
	w.mu.Unlock()

	go func() {
		err := cmd.Wait()
		job.mu.Lock()
		job.done = true
		job.err = err
		if cmd.ProcessState != nil {
			job.exitCode = cmd.ProcessState.ExitCode()
		}
		job.mu.Unlock()
	}()
	if timeout > 0 {
		go func() {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			<-timer.C
			job.mu.Lock()
			done := job.done
			job.mu.Unlock()
			if !done {
				job.stop(!w.cfg.LeaveChildProcesses)
			}
		}()
	}
	return job, nil
}

// shellArgs invokes a non-login, non-interactive shell. The command still runs
// with the caller's environment (Config.Env, defaulting to os.Environ), so a
// login shell would add nothing but its profile's side effects — whose banners
// and echoes would land in the captured output and reach the model as if they
// were command output.
func shellArgs(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"/C", command}
	}
	return []string{"-c", command}
}

func (w *Workspace) ensureTempDir() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.tmpDir != "" {
		return w.tmpDir, nil
	}
	dir, err := os.MkdirTemp("", "ds4go-workspace-*")
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	w.tmpDir = dir
	w.tmpRoot = root
	return dir, nil
}

func (w *Workspace) dropJob(id int) {
	w.mu.Lock()
	delete(w.jobs, id)
	w.mu.Unlock()
}

func (w *Workspace) findJob(id, pid int) (*bashJob, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	job := w.jobs[id]
	if job == nil {
		return nil, fmt.Errorf("bash job not found: job=%d pid=%d", id, pid)
	}
	if pid > 0 && job.cmd.Process != nil && job.cmd.Process.Pid != pid {
		return nil, fmt.Errorf("bash job pid mismatch: job=%d pid=%d", id, pid)
	}
	return job, nil
}

func (w *Workspace) waitForJob(ctx context.Context, job *bashJob, d time.Duration) {
	if d <= 0 {
		return
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ctx != nil && ctx.Err() != nil {
			return
		}
		if job.isDone() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// observeJob reports job status and a bounded output snippet. Finished jobs
// stay registered until Close so they can be observed repeatedly.
func (w *Workspace) observeJob(job *bashJob) (string, error) {
	done, exitCode, errText := job.status()
	status := "running"
	if done {
		status = "done"
	}
	elapsed := time.Since(job.started).Seconds()
	pid := 0
	if job.cmd.Process != nil {
		pid = job.cmd.Process.Pid
	}
	var out strings.Builder
	fmt.Fprintf(&out, "bash job=%d pid=%d status=%s elapsed_sec=%.1f timeout_sec=%.0f\n",
		job.id, pid, status, elapsed, job.timeout.Seconds())
	if done {
		fmt.Fprintf(&out, "exit_status=%d\n", exitCode)
		if errText != "" && exitCode == 0 {
			fmt.Fprintf(&out, "wait_error=%s\n", errText)
		}
	}
	snippet, size, lines, err := boundedFileSnippet(job.path, w.cfg.MaxOutputBytes, done)
	if err != nil {
		fmt.Fprintf(&out, "output_error=%v\n", err)
	} else {
		fmt.Fprintf(&out, "output_path=%s (%d bytes, %d lines)\n", job.path, size, lines)
		out.WriteString(snippet)
	}
	return out.String(), nil
}

func (j *bashJob) status() (done bool, exitCode int, errText string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.err != nil {
		errText = j.err.Error()
	}
	return j.done, j.exitCode, errText
}

func (j *bashJob) isDone() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done
}

func (j *bashJob) stop(killChildren bool) {
	if killChildren {
		// Kill the shell's process group, but only while the shell is still
		// running. An unreaped shell keeps its process-group id reserved, so the
		// signal is guaranteed to target the shell's own group and its children.
		j.killGroupOnce()
	}
	// Terminate the shell itself (reuse-safe via os.Process) so a job whose
	// children are left alone still stops.
	terminateShell(j.cmd, func(d time.Duration) bool {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			if j.isDone() {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return j.isDone()
	})
}

// killGroupOnce raw-signals the shell's process group, at most once and only
// while the shell has not been reaped. A reaped shell's group id may have been
// reused, so signaling it could hit an unrelated process; such post-exit
// signals are skipped, which means a backgrounded child that outlives its shell
// is left running rather than risk a mis-targeted kill.
func (j *bashJob) killGroupOnce() {
	j.mu.Lock()
	if j.groupSignaled || j.done {
		j.mu.Unlock()
		return
	}
	j.groupSignaled = true
	j.mu.Unlock()
	killShellGroup(j.cmd)
}

// wait blocks until the job has been reaped or timeout elapses, reporting
// whether it finished. Every wait is bounded: stop() escalates to SIGKILL, but
// a process wedged in uninterruptible I/O survives even that, and an unbounded
// spin here would hang the tool loop or Close along with it.
func (j *bashJob) wait(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if j.isDone() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func durationSeconds(v float64, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return time.Duration(v * float64(time.Second))
}

// boundedFileSnippet returns a snippet of at most maxBytes from the job
// output file. Memory stays bounded by maxBytes plus a fixed scan buffer even
// for arbitrarily large output files; the file may still be growing.
func boundedFileSnippet(path string, maxBytes int64, done bool) (snippet string, size int64, lines int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, 0, err
	}
	size = st.Size()

	buf := make([]byte, 64<<10)
	var scanned int64
	var last byte
	for scanned < size {
		want := int64(len(buf))
		if remaining := size - scanned; remaining < want {
			want = remaining
		}
		n, rerr := f.ReadAt(buf[:want], scanned)
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
			scanned += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", 0, 0, rerr
		}
	}
	size = scanned
	if size > 0 && last != '\n' {
		lines++
	}

	readChunk := func(off, n int64) (string, error) {
		data := make([]byte, n)
		read, rerr := f.ReadAt(data, off)
		if rerr != nil && rerr != io.EOF {
			return "", rerr
		}
		return string(data[:read]), nil
	}

	if size <= maxBytes {
		data, rerr := readChunk(0, size)
		if rerr != nil {
			return "", 0, 0, rerr
		}
		return "<output>\n" + data + closeBlock(data, "output"), size, lines, nil
	}
	if done {
		tail, rerr := readChunk(size-maxBytes, maxBytes)
		if rerr != nil {
			return "", 0, 0, rerr
		}
		// Align to the next newline when possible so the tail starts cleanly.
		if i := strings.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
			tail = tail[i+1:]
		}
		return fmt.Sprintf("<tail %d bytes>\n%s%s", len(tail), tail, closeBlock(tail, "tail")), size, lines, nil
	}
	head, rerr := readChunk(0, maxBytes)
	if rerr != nil {
		return "", 0, 0, rerr
	}
	return fmt.Sprintf("<head %d bytes>\n%s%s", len(head), head, closeBlock(head, "head")), size, lines, nil
}

func closeBlock(s, name string) string {
	if s != "" && !strings.HasSuffix(s, "\n") {
		return "\n</" + name + ">\n"
	}
	return "</" + name + ">\n"
}
