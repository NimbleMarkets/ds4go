package models

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LeftoverKind classifies a file in the models directory that is not an
// installed model.
type LeftoverKind string

const (
	// LeftoverPartial is a resumable ".part" download.
	LeftoverPartial LeftoverKind = "partial"
	// LeftoverQuarantined is a ".bad-<hash>" file set aside after a hash
	// mismatch.
	LeftoverQuarantined LeftoverKind = "quarantined"
	// LeftoverLock is a ".lock" file left by a finished download. The lock
	// itself is advisory and released on exit; only the empty file remains.
	LeftoverLock LeftoverKind = "lock"
)

// Leftover is one removable file in the models directory.
type Leftover struct {
	Path  string
	Kind  LeftoverKind
	Bytes int64
	// Alias is the catalog alias the file belongs to, or "" when the catalog
	// no longer lists the model.
	Alias string
	// Locked reports that a download for this model is in progress; such
	// files are listed but never removed.
	Locked bool
}

// Leftovers scans the models directory for partial downloads, quarantined
// files, and lock files. Installed models are never included.
func (m *Manager) Leftovers() ([]Leftover, error) {
	entries, err := os.ReadDir(m.ModelsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	byFile := map[string]string{}
	for _, model := range Curated() {
		byFile[model.FileName] = model.Alias
	}
	var out []Leftover
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		var kind LeftoverKind
		var base string
		switch {
		case strings.HasSuffix(name, ".part"):
			kind, base = LeftoverPartial, splitPartBase(strings.TrimSuffix(name, ".part"))
		case strings.HasSuffix(name, ".assembling"):
			kind, base = LeftoverPartial, strings.TrimSuffix(name, ".assembling")
		case splitPartPattern.MatchString(name):
			kind, base = LeftoverPartial, splitPartBase(name)
		case strings.HasSuffix(name, ".lock"):
			kind, base = LeftoverLock, strings.TrimSuffix(name, ".lock")
		case strings.Contains(name, ".bad-"):
			kind, base = LeftoverQuarantined, name[:strings.Index(name, ".bad-")]
		default:
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		item := Leftover{Path: filepath.Join(m.ModelsDir, name), Kind: kind, Bytes: info.Size(), Alias: byFile[base]}
		if kind != LeftoverQuarantined {
			holder, err := GetLockHolder(filepath.Join(m.ModelsDir, base+".lock"))
			item.Locked = err == nil && holder != 0
		}
		out = append(out, item)
	}
	return out, nil
}

// RemoveLeftovers deletes the given leftovers, skipping any whose download is
// in progress, and returns the ones it removed. A lock file is only removed
// while this process holds it, so a downloader cannot be racing on it.
func (m *Manager) RemoveLeftovers(items []Leftover) ([]Leftover, error) {
	stateLock, err := m.lockState()
	if err != nil {
		return nil, err
	}
	defer stateLock.Close()
	var removed []Leftover
	for _, item := range items {
		if item.Locked {
			continue
		}
		lockPath := item.Path
		if item.Kind == LeftoverPartial {
			lockPath = splitPartBase(strings.TrimSuffix(strings.TrimSuffix(item.Path, ".part"), ".assembling")) + ".lock"
		}
		if item.Kind != LeftoverQuarantined {
			lock, err := TryLock(lockPath)
			if errors.Is(err, ErrLocked) {
				continue // a download started since the scan
			}
			if err != nil {
				return removed, err
			}
			err = os.Remove(item.Path)
			if err == nil && item.Kind == LeftoverPartial {
				// TryLock created or reused the lock file; with the partial
				// gone it has nothing to guard, so do not leave it behind.
				err = os.Remove(lockPath)
			}
			lock.Close()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
		} else if err := os.Remove(item.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed = append(removed, item)
		fmt.Fprintf(m.Out, "Removed %s\n", item.Path)
	}
	return removed, nil
}

// DeletePartial removes a model's partial download and nothing else; the
// installed file, if any, stays.
func (m *Manager) DeletePartial(alias string) error {
	model, ok := lookup(alias)
	if !ok {
		return unknownAlias(alias)
	}
	if p, _ := m.partial(model); !p {
		return fmt.Errorf("no partial download for %s", alias)
	}
	out := filepath.Join(m.ModelsDir, model.FileName)
	lock, err := TryLock(out + ".lock")
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return fmt.Errorf("cannot delete the partial download of %s: a download for it is still in progress — cancel that download first", alias)
		}
		return err
	}
	defer lock.Close()
	stateLock, err := m.lockState()
	if err != nil {
		return err
	}
	defer stateLock.Close()
	if err := os.Remove(out + ".part"); err != nil {
		return err
	}
	fmt.Fprintf(m.Out, "Removed partial download %s\n", out+".part")
	return nil
}
