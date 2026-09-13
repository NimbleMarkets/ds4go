package models

import (
	"os"
	"path/filepath"
	"testing"
)

// ClassifyLoadedFiles maps the GGUF files a process has open onto catalog
// aliases and roles so a status view can say "q2-imatrix (model) with mtp
// and vision-encoder" instead of listing file names. The active-model link
// resolves to the model it points at; unknown files pass through unnamed.
func TestClassifyLoadedFiles(t *testing.T) {
	dir := t.TempDir()
	q2, _ := lookup("q2-imatrix")
	mtp, _ := lookup(MTPAlias)
	enc, _ := lookup("vision-encoder")
	for _, m := range []Model{q2, mtp, enc} {
		if err := os.WriteFile(filepath.Join(dir, m.FileName), []byte("gguf"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, DefaultModelSymlink)
	if err := os.Link(filepath.Join(dir, q2.FileName), link); err != nil {
		t.Skipf("hard link unavailable: %v", err)
	}
	custom := filepath.Join(dir, "custom.gguf")
	_ = os.WriteFile(custom, []byte("x"), 0o600)

	got := ClassifyLoadedFiles([]string{link, filepath.Join(dir, mtp.FileName), filepath.Join(dir, enc.FileName), custom})
	want := []LoadedFile{
		{Path: link, Alias: "q2-imatrix", Role: RoleModel},
		{Path: filepath.Join(dir, mtp.FileName), Alias: "mtp", Role: RoleMTP},
		{Path: filepath.Join(dir, enc.FileName), Alias: "vision-encoder", Role: RoleVision},
		{Path: custom, Alias: "", Role: RoleUnknown},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// Models come first, then companions, so the main model leads the line.
	sorted := ClassifyLoadedFiles([]string{filepath.Join(dir, enc.FileName), filepath.Join(dir, mtp.FileName), link})
	if sorted[0].Role != RoleModel || sorted[1].Role != RoleMTP || sorted[2].Role != RoleVision {
		t.Errorf("order = %+v, want model, mtp, vision", sorted)
	}
}
