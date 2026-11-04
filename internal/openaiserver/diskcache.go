package openaiserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Cache policy is Go-owned; payloads are opaque libds4 files. A private, locked
// directory prevents multiple servers from racing eviction or publishing entries.
// An entry directory is published atomically only after both files are durable.
type diskCache struct {
	minTokens      int
	hits           uint64
	mu             sync.Mutex
	dir, namespace string
	budget         int64
	lock           *os.File
}
type cacheEntry struct {
	Namespace string
	Tokens    []int
	Bytes     int64
	SHA256    string
	path      string
	used      time.Time
	size      int64
}

func openDiskCache(dir, namespace string, budget int64) (*diskCache, error) {
	if budget <= 0 {
		return nil, errors.New("disk cache budget must be positive")
	}
	dir = filepath.Join(dir, "ds4go-kv-v1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("disk cache directory must be a private directory (mode 0700)")
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("disk cache is already in use: %w", err)
	}
	c := &diskCache{dir: dir, namespace: namespace, budget: budget, lock: f}
	// Remove unpublished entries left by interrupted writes, only in our private format directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.Close()
		return nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				c.Close()
				return nil, err
			}
		}
	}
	if err := c.evict(0); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
func (c *diskCache) Close() {
	if c != nil && c.lock != nil {
		c.lock.Close()
		c.lock = nil
	}
}

func cacheKey(namespace string, tokens []int) string {
	data, _ := json.Marshal(struct {
		Namespace string
		Tokens    []int
	}{namespace, tokens})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (c *diskCache) entries() ([]cacheEntry, error) {
	dirs, err := os.ReadDir(c.dir)
	if err != nil {
		return nil, err
	}
	var entries []cacheEntry
	for _, d := range dirs {
		if !d.IsDir() || len(d.Name()) != 64 {
			continue
		}
		if _, err := hex.DecodeString(d.Name()); err != nil {
			continue
		}
		path := filepath.Join(c.dir, d.Name())
		// Metadata is bounded independently of the caller's disk budget.
		st, err := os.Lstat(filepath.Join(path, "metadata.json"))
		if err != nil || !st.Mode().IsRegular() || st.Size() > 8<<20 {
			if err := os.RemoveAll(path); err != nil {
				return nil, err
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(path, "metadata.json"))
		if err != nil {
			return nil, err
		}
		var e cacheEntry
		if json.Unmarshal(data, &e) != nil || e.Bytes <= 0 || len(e.Tokens) == 0 || cacheKey(e.Namespace, e.Tokens) != d.Name() {
			if err := os.RemoveAll(path); err != nil {
				return nil, err
			}
			continue
		}
		payload, err := os.Lstat(filepath.Join(path, "payload"))
		if err != nil || !payload.Mode().IsRegular() {
			if err := os.RemoveAll(path); err != nil {
				return nil, err
			}
			continue
		}
		e.path = path
		e.used = st.ModTime()
		e.size = payload.Size() + st.Size()
		entries = append(entries, e)
	}
	return entries, nil
}
func (c *diskCache) evict(extra int64) error {
	entries, err := c.entries()
	if err != nil {
		return err
	}
	var total int64
	for _, e := range entries {
		total += e.size
	}
	slices.SortFunc(entries, func(a, b cacheEntry) int { return a.used.Compare(b.used) })
	for _, e := range entries {
		if total <= c.budget-extra {
			break
		}
		if err := os.RemoveAll(e.path); err != nil {
			return err
		}
		total -= e.size
	}
	return nil
}
func fileDigest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// restore chooses an exact token prefix longer than the resident prefix. A load
// callback failure may have mutated native state: the caller must discard it.
func (c *diskCache) restore(tokens []int, resident int, load func(string, uint64, []int) error) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := c.entries()
	if err != nil {
		return false, err
	}
	var best *cacheEntry
	for i := range entries {
		e := &entries[i]
		if e.Namespace != c.namespace || len(e.Tokens) < c.minTokens || len(e.Tokens) <= resident || len(e.Tokens) > len(tokens) || e.size > c.budget || !slices.Equal(e.Tokens, tokens[:len(e.Tokens)]) {
			continue
		}
		if best == nil || len(e.Tokens) > len(best.Tokens) {
			best = e
		}
	}
	if best == nil {
		return false, nil
	}
	path := filepath.Join(best.path, "payload")
	sum, n, err := fileDigest(path)
	if err != nil || n != best.Bytes || sum != best.SHA256 {
		_ = os.RemoveAll(best.path)
		return false, errors.New("discarded corrupt disk KV checkpoint")
	}
	if err := load(path, uint64(n), best.Tokens); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			_ = os.RemoveAll(best.path)
		}
		return true, err
	}
	c.hits++
	now := time.Now()
	_ = os.Chtimes(filepath.Join(best.path, "metadata.json"), now, now)
	return true, nil
}
func (c *diskCache) store(tokens []int, estimate uint64, save func(string) error) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(tokens) == 0 || estimate == 0 || estimate > uint64(c.budget) {
		return false, nil
	}
	key := cacheKey(c.namespace, tokens)
	dest := filepath.Join(c.dir, key)
	if _, err := os.Stat(dest); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	// Reserve budget before writing; account for metadata as well as native payload.
	meta := cacheEntry{Namespace: c.namespace, Tokens: tokens, Bytes: int64(estimate), SHA256: strings.Repeat("0", 64)}
	data, _ := json.Marshal(meta)
	if len(data) > 8<<20 || int64(len(data)) > c.budget-int64(estimate) {
		return false, nil
	}
	if err := c.evict(int64(estimate) + int64(len(data))); err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp(c.dir, ".tmp-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	path := filepath.Join(tmp, "payload")
	if err := save(path); err != nil {
		return false, err
	}
	sum, n, err := fileDigest(path)
	if err != nil {
		return false, err
	}
	if n != int64(estimate) {
		return false, fmt.Errorf("KV payload size %d differs from estimate %d", n, estimate)
	}
	meta.SHA256 = sum
	data, _ = json.Marshal(meta)
	if err := os.Chmod(path, 0600); err != nil {
		return false, err
	}
	// SavePayloadFile closes the native stream; explicitly sync the completed file.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := os.WriteFile(filepath.Join(tmp, "metadata.json"), data, 0600); err != nil {
		return false, err
	}
	f, err = os.Open(filepath.Join(tmp, "metadata.json"))
	if err != nil {
		return false, err
	}
	err = f.Sync()
	f.Close()
	if err != nil {
		return false, err
	}
	f, err = os.Open(tmp)
	if err != nil {
		return false, err
	}
	err = f.Sync()
	f.Close()
	if err != nil {
		return false, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return false, err
	}
	f, err = os.Open(c.dir)
	if err != nil {
		return false, err
	}
	err = f.Sync()
	f.Close()
	return true, err
}
