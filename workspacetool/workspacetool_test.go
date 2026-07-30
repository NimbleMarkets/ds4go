package workspacetool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newTestWorkspace(t *testing.T, cfg Config) *Workspace {
	t.Helper()
	if cfg.Root == "" {
		cfg.Root = t.TempDir()
	}
	w, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func invoke(t *testing.T, tool interface {
	Invoke(context.Context, json.RawMessage) (string, error)
}, args string) (string, error) {
	t.Helper()
	return tool.Invoke(context.Background(), json.RawMessage(args))
}

func TestReadAndMore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\nthree\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, MaxReadLines: 2})

	out, err := invoke(t, w.ReadTool(), `{"path":"a.txt","max_lines":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1 one") || !strings.Contains(out, "continue_offset=3") {
		t.Fatalf("read output = %q", out)
	}
	out, err = invoke(t, w.MoreTool(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3 three") {
		t.Fatalf("more output = %q", out)
	}
}

func TestResolveRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.ReadTool(), `{"path":"../outside.txt"}`)
	if err != nil {
		t.Fatalf("path escape must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") {
		t.Fatalf("path escape observation = %q", out)
	}
}

func TestResolveRejectsSymlinkByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.ReadTool(), `{"path":"link.txt"}`)
	if err != nil {
		t.Fatalf("symlink rejection must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "symlink") {
		t.Fatalf("symlink rejection observation = %q", out)
	}
}

func TestSearchLiteralRegexAndGlob(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\nfunc Alpha() {}\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("Alpha text\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root})

	out, err := invoke(t, w.SearchTool(), `{"query":"alpha","case_sensitive":false,"glob":"*.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go") || strings.Contains(out, "a.txt") {
		t.Fatalf("literal search output = %q", out)
	}
	out, err = invoke(t, w.SearchTool(), `{"query":"func [A-Z].*","mode":"regex"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "func Alpha") {
		t.Fatalf("regex search output = %q", out)
	}
}

func TestWriteRequiresAllowWrite(t *testing.T) {
	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.WriteTool(), `{"path":"x.txt","content":"x"}`)
	if err != nil {
		t.Fatalf("disabled write must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "write access is disabled") {
		t.Fatalf("disabled write observation = %q", out)
	}

	w = newTestWorkspace(t, Config{Root: root, AllowWrite: true})
	if _, err := invoke(t, w.WriteTool(), `{"path":"x.txt","content":"x"}`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "x.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x" {
		t.Fatalf("written data = %q", data)
	}
}

func TestEditExactAndAnchored(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "x.txt")
	if err := os.WriteFile(path, []byte("start\nold\nmiddle\nend\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, AllowWrite: true})

	if _, err := invoke(t, w.EditTool(), `{"path":"x.txt","old":"old","new":"new"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, w.EditTool(), `{"path":"x.txt","old":"start\n[upto]\nend","new":"done\n"}`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "done\n\n" {
		t.Fatalf("edited data = %q", data)
	}
}

func TestBashRequiresOptIn(t *testing.T) {
	w := newTestWorkspace(t, Config{})
	out, err := invoke(t, w.BashTool(), `{"command":"echo hi"}`)
	if err != nil {
		t.Fatalf("disabled shell must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "shell access is disabled") {
		t.Fatalf("disabled shell observation = %q", out)
	}
}

func TestBashRunsWhenEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell output command differs on Windows")
	}
	root := t.TempDir()
	w := newTestWorkspace(t, Config{Root: root, AllowShell: true})
	out, err := invoke(t, w.BashTool(), `{"command":"printf hi","timeout_sec":5,"refresh_sec":0.2}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status=done") || !strings.Contains(out, "hi") {
		t.Fatalf("bash output = %q", out)
	}
}
