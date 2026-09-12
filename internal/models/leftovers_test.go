package models

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSized(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", n)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLeftoversFindsPartialQuarantinedAndStaleLocks(t *testing.T) {
	m := testManager(t.TempDir())
	q2, _ := lookup("q2-imatrix")
	writeSized(t, filepath.Join(m.ModelsDir, q2.FileName+".part"), 300)
	writeSized(t, filepath.Join(m.ModelsDir, "Old-Build.gguf.part"), 50) // no longer in the catalog
	writeSized(t, filepath.Join(m.ModelsDir, q2.FileName+".bad-deadbeef0000"), 70)
	writeSized(t, filepath.Join(m.ModelsDir, "Some-Model.gguf.lock"), 0)
	writeSized(t, filepath.Join(m.ModelsDir, "Installed.gguf"), 10) // untouched

	got, err := m.Leftovers()
	if err != nil {
		t.Fatalf("Leftovers: %v", err)
	}
	byName := map[string]Leftover{}
	for _, l := range got {
		byName[filepath.Base(l.Path)] = l
	}
	if len(got) != 4 {
		t.Fatalf("found %d leftovers, want 4: %+v", len(got), got)
	}
	if l := byName[q2.FileName+".part"]; l.Kind != LeftoverPartial || l.Bytes != 300 || l.Alias != "q2-imatrix" || l.Locked {
		t.Errorf("catalog partial = %+v", l)
	}
	if l := byName["Old-Build.gguf.part"]; l.Kind != LeftoverPartial || l.Bytes != 50 || l.Alias != "" {
		t.Errorf("orphan partial = %+v", l)
	}
	if l := byName[q2.FileName+".bad-deadbeef0000"]; l.Kind != LeftoverQuarantined || l.Bytes != 70 || l.Alias != "q2-imatrix" {
		t.Errorf("quarantined = %+v", l)
	}
	if l := byName["Some-Model.gguf.lock"]; l.Kind != LeftoverLock || l.Locked {
		t.Errorf("stale lock = %+v", l)
	}
	if _, ok := byName["Installed.gguf"]; ok {
		t.Error("an installed model was listed as a leftover")
	}
}

func TestLeftoversMarksInProgressDownloads(t *testing.T) {
	m := testManager(t.TempDir())
	q2, _ := lookup("q2-imatrix")
	out := filepath.Join(m.ModelsDir, q2.FileName)
	writeSized(t, out+".part", 100)
	lock, err := TryLock(out + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	got, err := m.Leftovers()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got {
		if !l.Locked {
			t.Errorf("%s reported as removable while its download lock is held", l.Path)
		}
	}
	if len(got) != 2 {
		t.Errorf("found %d leftovers (part + live lock), want 2: %+v", len(got), got)
	}
}

func TestRemoveLeftoversSkipsLockedAndReportsRemoved(t *testing.T) {
	m := testManager(t.TempDir())
	q2, _ := lookup("q2-imatrix")
	enc, _ := lookup("vision-encoder")
	live := filepath.Join(m.ModelsDir, q2.FileName)
	writeSized(t, live+".part", 100)
	lock, err := TryLock(live + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	stale := filepath.Join(m.ModelsDir, enc.FileName)
	writeSized(t, stale+".part", 40)
	writeSized(t, stale+".bad-0123456789ab", 40)
	writeSized(t, stale+".lock", 0)

	items, err := m.Leftovers()
	if err != nil {
		t.Fatal(err)
	}
	removed, err := m.RemoveLeftovers(items)
	if err != nil {
		t.Fatalf("RemoveLeftovers: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("removed %d, want 3 (stale part, bad, lock): %+v", len(removed), removed)
	}
	if _, err := os.Stat(live + ".part"); err != nil {
		t.Error("in-progress partial was removed")
	}
	for _, p := range []string{stale + ".part", stale + ".bad-0123456789ab", stale + ".lock"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still present", p)
		}
	}
}

func TestDeletePartialKeepsTheInstalledFile(t *testing.T) {
	m := testManager(t.TempDir())
	q2, _ := lookup("q2-imatrix")
	out := filepath.Join(m.ModelsDir, q2.FileName)
	writeSized(t, out, 10)
	writeSized(t, out+".part", 20)
	if err := m.DeletePartial("q2-imatrix"); err != nil {
		t.Fatalf("DeletePartial: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("installed file was removed")
	}
	if _, err := os.Stat(out + ".part"); !os.IsNotExist(err) {
		t.Error("partial file still present")
	}
	if err := m.DeletePartial("q2-imatrix"); err == nil || !strings.Contains(err.Error(), "no partial download") {
		t.Errorf("second call: err = %v", err)
	}
	if err := m.DeletePartial("nope"); err == nil {
		t.Error("unknown alias accepted")
	}
}
