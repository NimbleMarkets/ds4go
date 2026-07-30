package workspacetool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRawReadTruncationNoticeIsLeadingHeader(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\nthree\nfour\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, MaxReadLines: 2})

	out, err := invoke(t, w.ReadTool(), `{"path":"a.txt","raw":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "one\ntwo\n") {
		t.Fatalf("raw truncated read must end with the exact file bytes, got %q", out)
	}
	first, _, _ := strings.Cut(out, "\n")
	if !strings.Contains(first, "continue_offset=3") {
		t.Fatalf("raw truncated read must lead with a truncation notice, got %q", out)
	}
}

func TestRawReadCompleteIsPureBytes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.ReadTool(), `{"path":"a.txt","raw":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "one\ntwo\n" {
		t.Fatalf("complete raw read must be the exact file bytes, got %q", out)
	}
}

func TestChunkedReadBoundsHugeSingleLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.txt")
	// One line with no newline, larger than MaxReadBytes.
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 8192)), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, MaxReadBytes: 1024})

	target, err := w.resolvePath("big.txt", accessRead)
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.readRange(target, 1, 1, false, false, true)
	if err == nil {
		t.Fatalf("expected MaxReadBytes error, got output of %d bytes", len(out))
	}
	if !strings.Contains(err.Error(), "MaxReadBytes") {
		t.Fatalf("err = %v, want MaxReadBytes", err)
	}
}

func TestWholeReadBoundsRenderedOutput(t *testing.T) {
	root := t.TempDir()
	// Raw size (600 bytes) is under MaxReadBytes, but numbered rendering with
	// per-line prefixes pushes the returned output well over it.
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte(strings.Repeat("x\n", 300)), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, MaxReadBytes: 700})
	target, err := w.resolvePath("f.txt", accessRead)
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.readWhole(target, false)
	if err == nil {
		t.Fatalf("expected rendered whole-read output to be bounded by MaxReadBytes, got %d bytes", len(out))
	}
	if !strings.Contains(err.Error(), "MaxReadBytes") {
		t.Fatalf("err = %v, want MaxReadBytes", err)
	}
}

func TestWholeReadHeaderOnlyRespectsTinyCap(t *testing.T) {
	root := t.TempDir()
	// An empty file renders only the header line (no numbered lines), so the
	// in-loop size check never runs; the header alone exceeds a tiny cap.
	if err := os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, MaxReadBytes: 8})
	target, err := w.resolvePath("empty.txt", accessRead)
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.readWhole(target, false)
	if err == nil {
		t.Fatalf("header-only render must be bounded by MaxReadBytes, got %d bytes: %q", len(out), out)
	}
	if !strings.Contains(err.Error(), "MaxReadBytes") {
		t.Fatalf("err = %v, want MaxReadBytes", err)
	}
}

func TestSearchReportsResultLimitTruncation(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("needle\n")
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(b.String()), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.SearchTool(), `{"query":"needle","max_results":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "max_results=5") {
		t.Fatalf("a search stopped at the result cap must say so, got %q", out)
	}
}

func TestSearchCompleteEmitsNoTruncationNotice(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("needle\nother\nneedle\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.SearchTool(), `{"query":"needle","max_results":50}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "truncated") || strings.Contains(out, "max_results=") {
		t.Fatalf("an exhaustive search must not claim truncation, got %q", out)
	}
}

func TestSearchReportsDepthLimitTruncation(t *testing.T) {
	root := t.TempDir()
	deep := root
	for i := 0; i < 30; i++ {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "deep.txt"), []byte("needle\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.SearchTool(), `{"query":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "deep.txt") {
		t.Fatalf("the depth cap should have stopped short of the file, got %q", out)
	}
	if !strings.Contains(out, "depth") {
		t.Fatalf("a search stopped by the depth cap must say so, got %q", out)
	}
}

func TestSearchSkipsOversizeFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("needle "+strings.Repeat("a", 4096)+"\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte("needle\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, MaxSearchFileBytes: 128})
	out, err := invoke(t, w.SearchTool(), `{"query":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "big.txt") || !strings.Contains(out, "small.txt") {
		t.Fatalf("search must skip files over MaxSearchFileBytes, got %q", out)
	}
}

func TestSearchSymlinkCycleVisitsEachDirOnce(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("needle\n"), 0666); err != nil {
		t.Fatal(err)
	}
	// A self-referential directory symlink forms a cycle.
	if err := os.Symlink(".", filepath.Join(root, "self")); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, FollowSymlinks: true})
	out, err := invoke(t, w.SearchTool(), `{"query":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "f.txt"); n != 1 {
		t.Fatalf("symlink cycle must not revisit dirs: f.txt appeared %d times: %q", n, out)
	}
}

func TestSearchRejectsInvalidGlob(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("needle\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.SearchTool(), `{"query":"needle","glob":"[x"}`)
	if err != nil {
		t.Fatalf("invalid glob must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "glob") {
		t.Fatalf("invalid glob observation = %q", out)
	}
}

func TestSearchFollowsSymlinksWhenEnabled(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("needle here\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "realdir"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "realdir", "d.txt"), []byte("needle deep\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("realdir", filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}

	off := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, off.SearchTool(), `{"query":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "link.txt") || strings.Contains(out, "linkdir") {
		t.Fatalf("default search must skip symlinks, got %q", out)
	}

	on := newTestWorkspace(t, Config{Root: root, FollowSymlinks: true})
	out, err = invoke(t, on.SearchTool(), `{"query":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "link.txt") {
		t.Fatalf("FollowSymlinks search must match a symlinked file, got %q", out)
	}
	// The symlinked directory resolves to realdir, which is already walked, so
	// its content is found exactly once (no duplicate via the symlink path).
	if n := strings.Count(out, "d.txt"); n != 1 {
		t.Fatalf("symlinked-dir content should be found once, saw %d: %q", n, out)
	}
}

func TestSearchSymlinkOutsideRootSkippedWithoutAllowOutsideRoot(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("needle secret\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, FollowSymlinks: true})
	out, err := invoke(t, w.SearchTool(), `{"query":"needle"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "link.txt") {
		t.Fatalf("search must not follow a symlink out of root without AllowOutsideRoot, got %q", out)
	}
}

func TestSearchSeparatorGlobMatchesRelativePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.go"), []byte("needle\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("needle\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root})
	out, err := invoke(t, w.SearchTool(), `{"query":"needle","glob":"sub/*.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "b.go") || strings.Contains(out, "b.txt") {
		t.Fatalf("separator glob search = %q", out)
	}
}
