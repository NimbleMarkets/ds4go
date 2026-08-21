package models

import (
	"fmt"
	"os"
	"path/filepath"
)

// diskSpaceReserve is free space required beyond the download itself, so a
// large model cannot leave the volume at literally zero. Fixed rather than a
// share of the volume: 5% of a 4 TB disk would reserve 200 GiB and block
// downloads that would have been fine.
const diskSpaceReserve = 2 << 30 // 2 GiB

// availableBytesFunc reports usable free space for a path. Indirected so tests
// can simulate a full volume.
var availableBytesFunc = availableBytes

// availableBytesFor reports free space for the volume path will live on,
// walking up to the nearest existing ancestor. The models directory does not
// exist until the first download creates it, and dry-run never creates it, but
// the volume is already determined by its parent.
func availableBytesFor(path string) (uint64, error) {
	dir := filepath.Clean(path)
	for {
		if _, err := os.Stat(dir); err == nil {
			return availableBytesFunc(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return availableBytesFunc(dir) // reached the root; report its error
		}
		dir = parent
	}
}

// requiredDownloadBytes returns the free space a download needs: the bytes
// still to fetch, plus the reserve. Resuming a nearly complete part-file needs
// only the remainder, not the whole model.
func requiredDownloadBytes(total, alreadyHave int64) uint64 {
	remaining := total - alreadyHave
	if remaining < 0 {
		remaining = 0
	}
	return uint64(remaining) + diskSpaceReserve
}

// checkDiskSpace reports whether dir's volume can hold the remaining bytes of a
// download of totalBytes, of which alreadyHave are already on disk.
//
// It is deliberately permissive about not knowing: a total size of zero (the
// remote never reported one) or a failing space lookup both skip the check.
// Refusing a download because we could not measure something would trade a
// clear failure for a mysterious one.
func (m *Manager) checkDiskSpace(dir, fileName string, totalBytes, alreadyHave int64) error {
	if totalBytes <= 0 {
		return nil
	}
	avail, err := availableBytesFor(dir)
	if err != nil {
		return nil
	}
	need := requiredDownloadBytes(totalBytes, alreadyHave)
	if avail >= need {
		return nil
	}
	remaining := need - diskSpaceReserve
	return fmt.Errorf("not enough free space for %s:\n"+
		"  need      %s  (%s remaining + %s reserve)\n"+
		"  available %s  on %s\n"+
		"  short by  %s\n"+
		"Re-run with --force to download anyway",
		fileName,
		formatBytes(int64(need)), formatBytes(int64(remaining)), formatBytes(diskSpaceReserve),
		formatBytes(int64(avail)), dir,
		formatBytes(int64(need-avail)))
}

// diskSpaceReport renders the dry-run line describing the space situation.
func (m *Manager) diskSpaceReport(dir string, totalBytes, alreadyHave int64) string {
	if totalBytes <= 0 {
		return "unknown (remote size not reported)"
	}
	avail, err := availableBytesFor(dir)
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	need := requiredDownloadBytes(totalBytes, alreadyHave)
	status := "sufficient"
	if avail < need {
		status = fmt.Sprintf("INSUFFICIENT, short by %s (use --force to override)",
			formatBytes(int64(need-avail)))
	}
	return fmt.Sprintf("%s free, need %s — %s",
		formatBytes(int64(avail)), formatBytes(int64(need)), status)
}
