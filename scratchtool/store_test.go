package scratchtool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T, cfg Config) *Store {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewDefaults(t *testing.T) {
	s := newTestStore(t, Config{})
	if s.cfg.MaxKeys != 32 {
		t.Errorf("MaxKeys = %d, want 32", s.cfg.MaxKeys)
	}
	if s.cfg.MaxValueBytes != 16<<10 {
		t.Errorf("MaxValueBytes = %d, want %d", s.cfg.MaxValueBytes, 16<<10)
	}
	if s.cfg.MaxTotalBytes != 128<<10 {
		t.Errorf("MaxTotalBytes = %d, want %d", s.cfg.MaxTotalBytes, 128<<10)
	}
}

func TestNewDefaultDirUsesSession(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DS4_DIR", base)
	s, err := New(Config{Session: "mysession"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	want := filepath.Join(base, "scratch", "mysession")
	if s.dir != want {
		t.Errorf("dir = %q, want %q", s.dir, want)
	}
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Errorf("session dir not created: %v", err)
	}
}

func TestNewRejectsBadSession(t *testing.T) {
	for _, session := range []string{"../x", "a/b", "..", "UPPER", ".hidden"} {
		if _, err := New(Config{Dir: t.TempDir(), Session: session}); err == nil {
			t.Errorf("New(Session=%q) = nil error, want error", session)
		}
	}
}

func TestSetGetDelete(t *testing.T) {
	s := newTestStore(t, Config{})
	if err := s.set("plan", []byte("step one")); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.get("plan")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "step one" {
		t.Errorf("get = %q, want %q", got, "step one")
	}
	if err := s.set("plan", []byte("step two")); err != nil {
		t.Fatalf("set replace: %v", err)
	}
	got, err = s.get("plan")
	if err != nil {
		t.Fatalf("get after replace: %v", err)
	}
	if string(got) != "step two" {
		t.Errorf("get after replace = %q, want %q", got, "step two")
	}
	if err := s.deleteKey("plan"); err != nil {
		t.Fatalf("deleteKey: %v", err)
	}
	if _, err := s.get("plan"); err == nil {
		t.Error("get after delete = nil error, want unknown key")
	}
}

func TestUnknownKeyErrors(t *testing.T) {
	s := newTestStore(t, Config{})
	if _, err := s.get("plan"); err == nil || !strings.Contains(err.Error(), `unknown key "plan"`) {
		t.Errorf("get missing = %v, want unknown key error", err)
	}
	if err := s.deleteKey("plan"); err == nil || !strings.Contains(err.Error(), `unknown key "plan"`) {
		t.Errorf("deleteKey missing = %v, want unknown key error", err)
	}
}

func TestAppend(t *testing.T) {
	s := newTestStore(t, Config{})
	if err := s.appendValue("findings", []byte("- foo")); err != nil {
		t.Fatalf("appendValue create: %v", err)
	}
	if err := s.appendValue("findings", []byte("\n- bar")); err != nil {
		t.Fatalf("appendValue: %v", err)
	}
	got, err := s.get("findings")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "- foo\n- bar" {
		t.Errorf("get = %q, want %q", got, "- foo\n- bar")
	}
}

func TestList(t *testing.T) {
	s := newTestStore(t, Config{})
	entries, err := s.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("list of empty store = %d entries, want 0", len(entries))
	}
	if err := s.set("plan", []byte("abcd")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.set("findings", []byte("xy")); err != nil {
		t.Fatalf("set: %v", err)
	}
	entries, err = s.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("list = %d entries, want 2", len(entries))
	}
	// Stable sort: lexicographic by key.
	if entries[0].key != "findings" || entries[1].key != "plan" {
		t.Errorf("list order = %q, %q; want findings, plan", entries[0].key, entries[1].key)
	}
	if entries[0].size != 2 || entries[1].size != 4 {
		t.Errorf("list sizes = %d, %d; want 2, 4", entries[0].size, entries[1].size)
	}
	if entries[0].modified.IsZero() {
		t.Error("list entry has zero modified time")
	}
}

func TestListSkipsDotfilesAndIndex(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, Config{Dir: dir})
	if err := s.set("plan", []byte("x")); err != nil {
		t.Fatalf("set: %v", err)
	}
	for _, name := range []string{".hidden", indexFileName, ".plan.tmp.123"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("junk"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	entries, err := s.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].key != "plan" {
		t.Errorf("list = %+v, want only plan", entries)
	}
}

func TestMaxKeys(t *testing.T) {
	s := newTestStore(t, Config{MaxKeys: 2})
	if err := s.set("a", []byte("1")); err != nil {
		t.Fatalf("set a: %v", err)
	}
	if err := s.set("b", []byte("2")); err != nil {
		t.Fatalf("set b: %v", err)
	}
	err := s.set("c", []byte("3"))
	if err == nil || !strings.Contains(err.Error(), "MaxKeys") {
		t.Errorf("set over MaxKeys = %v, want MaxKeys error", err)
	}
	// Replacing an existing key is not a new key.
	if err := s.set("a", []byte("replaced")); err != nil {
		t.Errorf("replace at MaxKeys: %v", err)
	}
}

func TestMaxValueBytes(t *testing.T) {
	s := newTestStore(t, Config{MaxValueBytes: 4})
	err := s.set("plan", []byte("12345"))
	if err == nil || !strings.Contains(err.Error(), "MaxValueBytes") {
		t.Errorf("set over MaxValueBytes = %v, want MaxValueBytes error", err)
	}
	if err := s.set("plan", []byte("1234")); err != nil {
		t.Fatalf("set at MaxValueBytes: %v", err)
	}
	// Append limits apply to the combined size.
	err = s.appendValue("plan", []byte("5"))
	if err == nil || !strings.Contains(err.Error(), "MaxValueBytes") {
		t.Errorf("append over MaxValueBytes = %v, want MaxValueBytes error", err)
	}
}

func TestMaxTotalBytes(t *testing.T) {
	s := newTestStore(t, Config{MaxTotalBytes: 6})
	if err := s.set("a", []byte("1234")); err != nil {
		t.Fatalf("set a: %v", err)
	}
	err := s.set("b", []byte("345"))
	if err == nil || !strings.Contains(err.Error(), "MaxTotalBytes") {
		t.Errorf("set over MaxTotalBytes = %v, want MaxTotalBytes error", err)
	}
	// Replacing a value counts the replacement, not the sum of both versions.
	if err := s.set("a", []byte("123456")); err != nil {
		t.Errorf("replace within MaxTotalBytes: %v", err)
	}
}

func TestFailedSetLeavesOldValue(t *testing.T) {
	s := newTestStore(t, Config{MaxValueBytes: 8})
	if err := s.set("plan", []byte("original")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.set("plan", []byte("too long value")); err == nil {
		t.Fatal("oversized set succeeded, want error")
	}
	got, err := s.get("plan")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("get after failed set = %q, want %q", got, "original")
	}
}

func TestAtomicReplaceLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, Config{Dir: dir})
	long := strings.Repeat("new value ", 100)
	if err := s.set("plan", []byte("old")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.set("plan", []byte(long)); err != nil {
		t.Fatalf("set replace: %v", err)
	}
	// The rename destination holds the complete new value.
	raw, err := os.ReadFile(filepath.Join(dir, "plan"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(raw) != long {
		t.Errorf("plan file = %d bytes, want %d", len(raw), len(long))
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, ent := range names {
		if ent.Name() != "plan" {
			t.Errorf("unexpected leftover file %q", ent.Name())
		}
	}
}

func TestValuesSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, Config{Dir: dir})
	if err := s.set("plan", []byte("durable")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := newTestStore(t, Config{Dir: dir})
	got, err := reopened.get("plan")
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if string(got) != "durable" {
		t.Errorf("get after reopen = %q, want %q", got, "durable")
	}
}

func TestConcurrentMutation(t *testing.T) {
	s := newTestStore(t, Config{})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := fmt.Sprintf("worker-%d", g)
			for range 20 {
				if err := s.set(key, []byte("value")); err != nil {
					t.Errorf("set: %v", err)
					return
				}
				if err := s.appendValue("shared", []byte("x")); err != nil {
					t.Errorf("appendValue: %v", err)
					return
				}
				if _, err := s.list(); err != nil {
					t.Errorf("list: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	got, err := s.get("shared")
	if err != nil {
		t.Fatalf("get shared: %v", err)
	}
	if len(got) != 8*20 {
		t.Errorf("shared = %d bytes, want %d", len(got), 8*20)
	}
}
