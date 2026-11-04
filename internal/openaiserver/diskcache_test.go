package openaiserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

func putCache(t *testing.T, c *diskCache, tokens []int, payload string) {
	t.Helper()
	ok, err := c.store(tokens, uint64(len(payload)), func(path string) error { return os.WriteFile(path, []byte(payload), 0600) })
	if err != nil || !ok {
		t.Fatalf("store: %v, %v", ok, err)
	}
}
func TestDiskCacheRestartPrefixAndIdentity(t *testing.T) {
	dir := t.TempDir()
	c, err := openDiskCache(dir, "model-a", 4096)
	if err != nil {
		t.Fatal(err)
	}
	putCache(t, c, []int{1, 2}, "short")
	putCache(t, c, []int{1, 2, 3}, "long")
	c.Close()
	c, err = openDiskCache(dir, "model-a", 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hit, err := c.restore([]int{1, 2, 3, 4}, 0, func(path string, n uint64, ids []int) error {
		b, err := os.ReadFile(path)
		if string(b) != "long" || n != 4 || !slices.Equal(ids, []int{1, 2, 3}) {
			t.Errorf("wrong checkpoint: %s %d %v", b, n, ids)
		}
		return err
	})
	if err != nil || !hit {
		t.Fatalf("restore: %v %v", hit, err)
	}
	never := func(string, uint64, []int) error { t.Fatal("unexpected native load"); return nil }
	for _, tokens := range [][]int{{1, 9}, {1}} {
		if hit, err := c.restore(tokens, 0, never); hit || err != nil {
			t.Fatal(hit, err)
		}
	}
	if hit, err := c.restore([]int{1, 2, 3, 4}, 3, never); hit || err != nil {
		t.Fatal(hit, err)
	}
	c.namespace = "other-model"
	if hit, err := c.restore([]int{1, 2, 3, 4}, 0, never); hit || err != nil {
		t.Fatal(hit, err)
	}
}
func TestDiskCacheCorruptionAndNativeFailure(t *testing.T) {
	c, err := openDiskCache(t.TempDir(), "m", 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ids := []int{1, 2}
	putCache(t, c, ids, "valid")
	path := filepath.Join(c.dir, cacheKey("m", ids), "payload")
	if err := os.WriteFile(path, []byte("wrong"), 0600); err != nil {
		t.Fatal(err)
	}
	hit, err := c.restore(ids, 0, func(string, uint64, []int) error { t.Fatal("corrupt payload reached native loader"); return nil })
	if hit || err == nil {
		t.Fatalf("corruption: %v %v", hit, err)
	}
	putCache(t, c, ids, "valid")
	hit, err = c.restore(ids, 0, func(string, uint64, []int) error { return errors.New("native rejected payload") })
	if !hit || err == nil {
		t.Fatalf("native failure not reported as possible mutation: %v %v", hit, err)
	}
	entries, err := c.entries()
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
func TestDiskCacheBudgetAndAtomicPublication(t *testing.T) {
	dir := t.TempDir()
	c, err := openDiskCache(dir, "m", 700)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	putCache(t, c, []int{1}, strings.Repeat("a", 200))
	putCache(t, c, []int{2}, strings.Repeat("b", 200))
	old := time.Now().Add(-time.Hour)
	path := filepath.Join(c.dir, cacheKey("m", []int{1}), "metadata.json")
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	putCache(t, c, []int{3}, strings.Repeat("c", 200))
	entries, err := c.entries()
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		total += e.size
		if slices.Equal(e.Tokens, []int{1}) {
			t.Fatal("oldest entry not evicted")
		}
	}
	if total > c.budget {
		t.Fatal("budget exceeded", total)
	}
	called := false
	ok, err := c.store([]int{4}, 1000, func(string) error { called = true; return nil })
	if ok || err != nil || called {
		t.Fatal("oversized payload written", ok, err)
	}
	ok, err = c.store([]int{4}, 2, func(path string) error { os.WriteFile(path, []byte("x"), 0600); return errors.New("write failed") })
	if ok || err == nil {
		t.Fatal("partial save accepted")
	}
	if _, err := os.Stat(filepath.Join(c.dir, cacheKey("m", []int{4}))); !os.IsNotExist(err) {
		t.Fatal("partial entry published")
	}
	dirs, _ := os.ReadDir(c.dir)
	for _, d := range dirs {
		if strings.HasPrefix(d.Name(), ".tmp-") {
			t.Fatal("temporary entry leaked")
		}
	}
}
func TestDiskCacheLockRecoveryAndConcurrency(t *testing.T) {
	dir := t.TempDir()
	c, err := openDiskCache(dir, "m", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := openDiskCache(dir, "m", 4096); err == nil {
		other.Close()
		t.Fatal("second server acquired cache")
	}
	tmp := filepath.Join(c.dir, ".tmp-interrupted")
	if err := os.Mkdir(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c, err = openDiskCache(dir, "m", 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("interrupted save not cleaned up")
	}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			ids := []int{i + 1}
			_, err := c.store(ids, 4, func(path string) error { return os.WriteFile(path, []byte("data"), 0600) })
			if err != nil {
				t.Error(err)
			}
			_, err = c.restore(ids, 0, func(string, uint64, []int) error { return nil })
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
func TestColdCheckpointPolicy(t *testing.T) {
	cfg := &cliopts.ServerConfig{KVCacheMinTokens: 512, KVCacheColdMaxTokens: 30000, KVCacheBoundaryTrimTokens: 32, KVCacheBoundaryAlignTokens: 2048}
	for _, tc := range []struct{ input, want int }{{500, 0}, {2048, 0}, {2080, 2048}, {5000, 4096}, {30001, 0}} {
		if got := coldCheckpointTokens(cfg, tc.input); got != tc.want {
			t.Errorf("%d: %d want %d", tc.input, got, tc.want)
		}
	}
	cfg.KVCacheBoundaryAlignTokens = 0
	cfg.KVCacheBoundaryTrimTokens = 0
	if got := coldCheckpointTokens(cfg, 512); got != 512 {
		t.Fatal(got)
	}
}

func TestDiskCacheMinimumAndCancelledLoad(t *testing.T) {
	c, err := openDiskCache(t.TempDir(), "m", 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	putCache(t, c, []int{1, 2}, "data")
	c.minTokens = 3
	hit, err := c.restore([]int{1, 2}, 0, func(string, uint64, []int) error { t.Fatal("loaded below configured minimum"); return nil })
	if hit || err != nil {
		t.Fatal(hit, err)
	}
	c.minTokens = 1
	_, err = c.restore([]int{1, 2}, 0, func(string, uint64, []int) error { return context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entries, err := c.entries()
	if err != nil || len(entries) != 1 {
		t.Fatal("cancellation removed a good entry", err)
	}
}
