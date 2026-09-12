package models

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// splitPartPattern matches the published pieces of a split GGUF
// ("<file>.gguf.part1", "<file>.gguf.part2", ...), as distinct from the
// downloader's own "<file>.part" resume files.
var splitPartPattern = regexp.MustCompile(`\.part[0-9]+$`)

// splitPartBase strips a split-part suffix, leaving the joined file's name.
func splitPartBase(name string) string {
	return splitPartPattern.ReplaceAllString(name, "")
}

// splitJoinScratchBytes is the extra space a join needs beyond the parts:
// the parts after the first are appended onto the first, so they exist
// twice until removed. Upstream documents this as "allow another 37 GiB".
func splitJoinScratchBytes(model Model) int64 {
	var n int64
	for _, part := range model.Parts[1:] {
		n += part.Bytes
	}
	return n
}

// partialSplit reports whether any piece of a split download is on disk and
// how many bytes those pieces hold: complete parts, their resume files, and
// an interrupted join.
func (m *Manager) partialSplit(model Model) (bool, int64) {
	var total int64
	found := false
	add := func(path string) {
		if st, err := os.Stat(path); err == nil && !st.IsDir() && st.Size() > 0 {
			total += st.Size()
			found = true
		}
	}
	for _, part := range model.Parts {
		add(filepath.Join(m.ModelsDir, part.FileName))
		add(filepath.Join(m.ModelsDir, part.FileName+".part"))
	}
	add(filepath.Join(m.ModelsDir, model.FileName+".assembling"))
	return found, total
}

// downloadSplit mirrors upstream download_model.sh's download_ds41_q4: fetch
// every part (each resumable and verified on its own), then join them into
// FileName by appending onto the first part, verify the joined file against
// the catalog hash, and remove the pieces. An interrupted join leaves
// <FileName>.assembling; rerunning truncates it to the first part's boundary
// and appends again without re-downloading the first part.
func (m *Manager) downloadSplit(ctx context.Context, model Model, token string, force bool) (Model, error) {
	out := filepath.Join(m.ModelsDir, model.FileName)
	assembling := out + ".assembling"
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		if model.SHA256 != "" {
			if err := validateSHA256(out, model.SHA256); err != nil {
				return Model{}, err
			}
			fmt.Fprintf(m.Out, "Already downloaded and verified: %s\n", out)
		} else {
			fmt.Fprintf(m.Out, "Already downloaded: %s\n", out)
		}
		return model, nil
	}
	if !force {
		var total int64
		for _, part := range model.Parts {
			total += part.Bytes
		}
		_, have := m.partial(model)
		if err := m.checkDiskSpace(m.ModelsDir, model.FileName, total+splitJoinScratchBytes(model), have); err != nil {
			return Model{}, err
		}
	}
	lock, err := TryLock(out + ".lock")
	if err != nil {
		return Model{}, err
	}
	defer lock.Close()

	first := model.Parts[0]
	if _, err := os.Stat(assembling); err != nil {
		firstOut := filepath.Join(m.ModelsDir, first.FileName)
		if err := m.ensurePart(ctx, model, first, firstOut, token); err != nil {
			return Model{}, err
		}
		if err := os.Rename(firstOut, assembling); err != nil {
			return Model{}, err
		}
	}
	st, err := os.Stat(assembling)
	if err != nil {
		return Model{}, err
	}
	if st.Size() < first.Bytes {
		return Model{}, fmt.Errorf("%s is smaller than %s (%s < %s); remove it to restart the download",
			assembling, first.FileName, formatBytes(st.Size()), formatBytes(first.Bytes))
	}
	if st.Size() > first.Bytes {
		fmt.Fprintf(m.Out, "Resuming interrupted join: truncating %s to %s\n", assembling, formatBytes(first.Bytes))
		if err := os.Truncate(assembling, first.Bytes); err != nil {
			return Model{}, err
		}
	}
	for _, part := range model.Parts[1:] {
		partOut := filepath.Join(m.ModelsDir, part.FileName)
		if err := m.ensurePart(ctx, model, part, partOut, token); err != nil {
			return Model{}, err
		}
		fmt.Fprintf(m.Out, "Joining %s onto %s\n", part.FileName, filepath.Base(assembling))
		if err := appendFile(assembling, partOut); err != nil {
			return Model{}, err
		}
		if err := os.Remove(partOut); err != nil {
			return Model{}, err
		}
	}
	if model.SHA256 != "" {
		fmt.Fprintf(m.Out, "Verifying joined %s\n", model.FileName)
		if err := validateSHA256(assembling, model.SHA256); err != nil {
			if qerr := quarantineBadPartial(assembling, err); qerr != nil {
				return Model{}, qerr
			}
			return Model{}, err
		}
		fmt.Fprintf(m.Out, "Verified sha256: %s\n", model.SHA256)
	}
	if err := os.Rename(assembling, out); err != nil {
		return Model{}, err
	}
	stateLock, err := m.lockState()
	if err != nil {
		return Model{}, err
	}
	defer stateLock.Close()
	return m.recordDownloadLocked(model, model.SHA256)
}

// ensurePart makes sure one part is complete on disk, verifying a file that
// is already present rather than fetching it again.
func (m *Manager) ensurePart(ctx context.Context, model Model, part ModelPart, out, token string) error {
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		if err := validateSHA256(out, part.SHA256); err == nil {
			fmt.Fprintf(m.Out, "Already downloaded and verified: %s\n", out)
			return nil
		} else if isHashMismatch(err) {
			if qerr := quarantineBadPartial(out, err); qerr != nil {
				return qerr
			}
			fmt.Fprintf(m.Out, "Re-downloading %s after hash mismatch\n", part.FileName)
		} else {
			return err
		}
	}
	_, err := m.downloadFile(ctx, partDownloadURL(model, part), out, token, part.SHA256)
	return err
}

// appendFile appends src's bytes onto dst and syncs dst.
func appendFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, in); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
