package workspacetool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func skipIfNoSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
}

func TestReadDeniesInRootSymlinkEscapeWhenFollowingSymlinks(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, FollowSymlinks: true, AllowWrite: true})

	out, err := invoke(t, w.ReadTool(), `{"path":"link.txt"}`)
	if err != nil {
		t.Fatalf("read escape must be an observation, got error: %v", err)
	}
	if strings.Contains(out, "SECRET") {
		t.Fatalf("in-root symlink to outside root must not be readable, got %q", out)
	}

	out, err = invoke(t, w.WriteTool(), `{"path":"link.txt","content":"pwn"}`)
	if err != nil {
		t.Fatalf("write escape must be an observation, got error: %v", err)
	}
	if data, _ := os.ReadFile(secret); string(data) != "SECRET" {
		t.Fatalf("write escaped root through symlink: outside file = %q (observation %q)", data, out)
	}
}

func TestWriteDeniesDanglingSymlinkEscapeWhenFollowingSymlinks(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "target.txt")
	if err := os.Symlink(target, filepath.Join(root, "dangle.txt")); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, FollowSymlinks: true, AllowWrite: true})
	if _, err := invoke(t, w.WriteTool(), `{"path":"dangle.txt","content":"pwn"}`); err != nil {
		t.Fatalf("write through dangling symlink must be an observation, got error: %v", err)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Fatal("write escaped root by creating the dangling symlink's outside target")
	}
}

func TestSearchDirSymlinkEscapeDenied(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "f.txt"), []byte("data\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, FollowSymlinks: true, AllowWrite: true})
	out, err := invoke(t, w.ReadTool(), `{"path":"linkdir/f.txt"}`)
	if err != nil {
		t.Fatalf("read through symlinked dir must be an observation, got error: %v", err)
	}
	if strings.Contains(out, "data") {
		t.Fatalf("read escaped root through a symlinked directory, got %q", out)
	}
}

func TestReadAllowsInRootSymlinkWhenFollowingSymlinks(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("hello\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, FollowSymlinks: true})
	out, err := invoke(t, w.ReadTool(), `{"path":"link.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("in-root symlink to in-root file should be readable, got %q", out)
	}
}

// TestWriteSymlinkSwapCannotEscapeRoot exercises the TOCTOU window: a
// background goroutine repeatedly swaps an in-root path between a real
// directory and a symlink pointing outside root while writes run. os.Root must
// ensure no write ever lands outside root, regardless of timing.
func TestWriteSymlinkSwapCannotEscapeRoot(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	outside := t.TempDir()
	canary := filepath.Join(outside, "canary.txt")
	if err := os.WriteFile(canary, []byte("ORIGINAL"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, FollowSymlinks: true, AllowWrite: true})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		linkDir := filepath.Join(root, "d")
		realDir := filepath.Join(root, "real_d")
		_ = os.MkdirAll(realDir, 0777)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(linkDir)
			_ = os.Symlink(outside, linkDir)
			_ = os.Remove(linkDir)
			_ = os.Symlink(realDir, linkDir)
		}
	}()

	for i := 0; i < 400; i++ {
		_, _ = invoke(t, w.WriteTool(), `{"path":"d/canary.txt","content":"pwn"}`)
	}
	close(stop)
	wg.Wait()

	if data, _ := os.ReadFile(canary); string(data) != "ORIGINAL" {
		t.Fatalf("write escaped root during symlink swap: canary = %q", data)
	}
}

func TestResolveAllowOutsideRootWorksUnderSymlinkedAncestors(t *testing.T) {
	skipIfNoSymlinks(t)
	realDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(realDir, "f.txt"), []byte("data\n"), 0666); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(t.TempDir(), "ln")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{AllowOutsideRoot: true})
	if _, err := w.resolvePath(filepath.Join(linkDir, "f.txt"), accessRead); err != nil {
		t.Fatalf("outside-root read through ancestor symlink should succeed with AllowOutsideRoot: %v", err)
	}
}

func TestResolveAllowOutsideRootStillRejectsSymlinkTarget(t *testing.T) {
	skipIfNoSymlinks(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "real.txt"), []byte("data\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(outside, "link.txt")); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{AllowOutsideRoot: true})
	if _, err := w.resolvePath(filepath.Join(outside, "link.txt"), accessRead); err == nil {
		t.Fatal("expected rejection when the outside-root target itself is a symlink")
	}
}
