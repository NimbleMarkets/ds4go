package scratchtool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// entry describes one pad key for listings.
type entry struct {
	key      string
	size     int64
	modified time.Time
}

// list returns the pad entries sorted by key. Directory entries that are not
// valid keys (dotfiles, temp files, index.json, subdirectories) are skipped.
func (s *Store) list() ([]entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() ([]entry, error) {
	dirents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	entries := make([]entry, 0, len(dirents))
	for _, ent := range dirents {
		name := ent.Name()
		if validateKey(name) != nil || !ent.Type().IsRegular() {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		entries = append(entries, entry{key: name, size: info.Size(), modified: info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	return entries, nil
}

// readLocked returns the value stored under key. A missing key returns an
// error satisfying errors.Is(err, fs.ErrNotExist); a key naming a symlink,
// directory, or other non-regular file is rejected. The check-then-read pair
// runs through the root handle, so even a concurrent swap of the checked
// file for a symlink cannot read outside the session directory.
func (s *Store) readLocked(key string) ([]byte, error) {
	info, err := s.root.Lstat(key)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("key %q is not a regular pad file", key)
	}
	return s.root.ReadFile(key)
}

// get returns the full value stored under key.
func (s *Store) get(key string) ([]byte, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.readLocked(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("unknown key %q", key)
	}
	return value, err
}

// set replaces the value stored under key, creating the key if missing.
func (s *Store) set(key string, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setLocked(key, value)
}

// appendValue appends to the value stored under key, creating the key if
// missing. Limits apply to the combined size.
func (s *Store) appendValue(key string, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.readLocked(key)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return s.setLocked(key, append(old, value...))
}

// setLocked enforces MaxKeys, MaxValueBytes, and MaxTotalBytes, then writes
// the value atomically: a temp file in the session directory renamed over the
// destination, so a mid-write failure never leaves a truncated value.
func (s *Store) setLocked(key string, value []byte) error {
	if len(value) > s.cfg.MaxValueBytes {
		return fmt.Errorf("value for key %q is %d bytes; MaxValueBytes is %d", key, len(value), s.cfg.MaxValueBytes)
	}
	entries, err := s.listLocked()
	if err != nil {
		return err
	}
	exists := false
	var total int64
	for _, ent := range entries {
		if ent.key == key {
			exists = true
			continue // replaced: count the new value below instead
		}
		total += ent.size
	}
	if !exists && len(entries) >= s.cfg.MaxKeys {
		return fmt.Errorf("store already holds %d keys; MaxKeys is %d", len(entries), s.cfg.MaxKeys)
	}
	if total+int64(len(value)) > int64(s.cfg.MaxTotalBytes) {
		return fmt.Errorf("store would hold %d bytes; MaxTotalBytes is %d", total+int64(len(value)), s.cfg.MaxTotalBytes)
	}

	tmp, err := os.CreateTemp(s.dir, "."+key+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(value); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := s.root.Rename(filepath.Base(tmpName), key); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// deleteKey removes key from the pad. A missing key is an error.
func (s *Store) deleteKey(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.root.Remove(key)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unknown key %q", key)
	}
	return err
}
