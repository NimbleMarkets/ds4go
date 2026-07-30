package workspacetool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func skipIfWindowsShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell output commands differ on Windows")
	}
}

var outputPathRe = regexp.MustCompile(`output_path=(\S+)`)

func TestBashOutputPathIsReadable(t *testing.T) {
	skipIfWindowsShell(t)
	w := newTestWorkspace(t, Config{Root: t.TempDir(), AllowShell: true})
	out, err := invoke(t, w.BashTool(), `{"command":"echo alpha; echo beta","timeout_sec":10,"refresh_sec":5}`)
	if err != nil {
		t.Fatal(err)
	}
	m := outputPathRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no output_path in observation: %q", out)
	}
	readOut, err := invoke(t, w.ReadTool(), fmt.Sprintf(`{"path":%q}`, m[1]))
	if err != nil {
		t.Fatalf("reading advertised output_path must not be fatal: %v", err)
	}
	if strings.Contains(readOut, "ERROR:") || !strings.Contains(readOut, "alpha") {
		t.Fatalf("advertised output_path must be readable by the read tool, got %q", readOut)
	}
}

func TestBashStatusRepeatsAfterDone(t *testing.T) {
	skipIfWindowsShell(t)
	w := newTestWorkspace(t, Config{Root: t.TempDir(), AllowShell: true})
	out, err := invoke(t, w.BashTool(), `{"command":"printf hi","timeout_sec":10,"refresh_sec":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status=done") {
		t.Fatalf("bash observation = %q", out)
	}
	for i := 0; i < 2; i++ {
		out, err = invoke(t, w.BashStatusTool(), `{"job":1}`)
		if err != nil {
			t.Fatalf("bash_status after done (attempt %d) must not be fatal: %v", i+1, err)
		}
		if strings.Contains(out, "ERROR:") || !strings.Contains(out, "status=done") || !strings.Contains(out, "exit_status=0") {
			t.Fatalf("bash_status after done (attempt %d) = %q", i+1, out)
		}
	}
}

func TestBashCancellationIsReported(t *testing.T) {
	skipIfWindowsShell(t)
	w := newTestWorkspace(t, Config{Root: t.TempDir(), AllowShell: true})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			w.mu.Lock()
			n := len(w.jobs)
			w.mu.Unlock()
			if n > 0 {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	_, err := w.BashTool().Invoke(ctx, json.RawMessage(`{"command":"sleep 5","timeout_sec":10,"refresh_sec":9}`))
	if err == nil {
		t.Fatal("expected canceled context to be reported as a fatal error, got success")
	}
}

func TestJobWaitHonorsDeadline(t *testing.T) {
	// A job that never reaps (a process wedged in uninterruptible I/O survives
	// even SIGKILL) must not block the caller forever.
	job := &bashJob{exitCode: -1}
	start := time.Now()
	if job.wait(50 * time.Millisecond) {
		t.Fatal("wait must report failure when the job never finishes")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("wait blocked for %v; it must honor its deadline", elapsed)
	}
}

func TestJobWaitReportsCompletion(t *testing.T) {
	job := &bashJob{exitCode: -1}
	go func() {
		time.Sleep(10 * time.Millisecond)
		job.mu.Lock()
		job.done = true
		job.mu.Unlock()
	}()
	if !job.wait(5 * time.Second) {
		t.Fatal("wait must report success once the job finishes")
	}
}

func TestCloseStopsRunningJobPromptly(t *testing.T) {
	skipIfWindowsShell(t)
	w := newTestWorkspace(t, Config{Root: t.TempDir(), AllowShell: true})
	if _, err := invoke(t, w.BashTool(), `{"command":"sleep 30","timeout_sec":120,"refresh_sec":0.2}`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := w.Close(); err != nil {
		t.Fatalf("Close with a running job = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Close took %v; teardown must be bounded", elapsed)
	}
}

func TestShellUsesNonLoginShell(t *testing.T) {
	skipIfWindowsShell(t)
	// A login shell sources the user's profile, whose banners and echoes would
	// land in the captured job output and be fed to the model as if they were
	// command output.
	fake := filepath.Join(t.TempDir(), "fakeshell")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf 'flag:%s\\n' \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: t.TempDir(), AllowShell: true, Shell: fake})
	out, err := invoke(t, w.BashTool(), `{"command":"echo hi","timeout_sec":10,"refresh_sec":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "flag:-c") {
		t.Fatalf("shell must be invoked non-login with -c, got %q", out)
	}
}

func TestBoundedFileSnippetHeadTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0666); err != nil {
		t.Fatal(err)
	}

	snippet, size, lines, err := boundedFileSnippet(path, 1000, true)
	if err != nil {
		t.Fatal(err)
	}
	if size != 19 || lines != 4 {
		t.Fatalf("size=%d lines=%d", size, lines)
	}
	if !strings.Contains(snippet, "one\ntwo\nthree\nfour\n") {
		t.Fatalf("full snippet = %q", snippet)
	}

	snippet, _, _, err = boundedFileSnippet(path, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snippet, "one") || !strings.Contains(snippet, "four") {
		t.Fatalf("tail snippet = %q", snippet)
	}

	snippet, _, _, err = boundedFileSnippet(path, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snippet, "one") || strings.Contains(snippet, "four") {
		t.Fatalf("head snippet = %q", snippet)
	}
}

func TestBoundedFileSnippetBoundsMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.out")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 1023) + "\n"
	for i := 0; i < 8*1024; i++ {
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	snippet, size, lines, err := boundedFileSnippet(path, 4096, true)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if size != 8*1024*1024 {
		t.Fatalf("size = %d", size)
	}
	if lines != 8*1024 {
		t.Fatalf("lines = %d", lines)
	}
	if len(snippet) > 4096+64 {
		t.Fatalf("snippet length = %d, want <= maxBytes plus framing", len(snippet))
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
		t.Fatalf("boundedFileSnippet allocated %d bytes for a %d-byte cap on an 8MB file", alloc, 4096)
	}
}
