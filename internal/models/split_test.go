package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// setCuratedParts points a split model's parts at test payloads.
func setCuratedParts(t *testing.T, alias string, parts []ModelPart) {
	t.Helper()
	for i := range curated {
		if curated[i].Alias == alias {
			old := curated[i].Parts
			curated[i].Parts = parts
			t.Cleanup(func() { curated[i].Parts = old })
			return
		}
	}
	t.Fatalf("unknown curated alias %q", alias)
}

// splitServer serves one payload per published file name and records fetches.
func splitServer(t *testing.T, payloads map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var fetched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, ok := payloads[filepath.Base(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Linked-Size", strconv.Itoa(len(payload)))
		w.Header().Set("X-Linked-Etag", sha256Hex(payload))
		if r.Method == http.MethodHead {
			return
		}
		fetched = append(fetched, filepath.Base(r.URL.Path))
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv, &fetched
}

func splitFixture(t *testing.T) (m *Manager, model Model, p1, p2 string) {
	t.Helper()
	m = testManager(t.TempDir())
	model, _ = lookup("v41-q4")
	p1, p2 = strings.Repeat("a", 3000), strings.Repeat("b", 500)
	setCuratedHash(t, "v41-q4", sha256Hex(p1+p2))
	setCuratedParts(t, "v41-q4", []ModelPart{
		{FileName: model.FileName + ".part1", SizeGB: 0.1, Bytes: int64(len(p1)), SHA256: sha256Hex(p1)},
		{FileName: model.FileName + ".part2", SizeGB: 0.1, Bytes: int64(len(p2)), SHA256: sha256Hex(p2)},
	})
	srv, _ := splitServer(t, map[string]string{model.FileName + ".part1": p1, model.FileName + ".part2": p2})
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) { return 1 << 60, nil })
	return m, model, p1, p2
}

// A split model is fetched part by part, joined into FileName, verified
// against the joined hash, and the parts are removed; nothing but the final
// GGUF remains, and it becomes the default like any first model.
func TestDownloadJoinsSplitParts(t *testing.T) {
	m, model, p1, p2 := splitFixture(t)
	got, err := m.Download(context.Background(), "v41-q4", "", false)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	out := filepath.Join(m.ModelsDir, model.FileName)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != p1+p2 {
		t.Errorf("joined file has %d bytes, want %d", len(data), len(p1+p2))
	}
	entries, _ := os.ReadDir(m.ModelsDir)
	for _, e := range entries {
		if n := e.Name(); strings.Contains(n, ".part") || strings.HasSuffix(n, ".assembling") {
			t.Errorf("leftover %s after a successful join", n)
		}
	}
	if !m.installed(model) || got.Alias != "v41-q4" {
		t.Errorf("model not installed after join: %+v", got)
	}
	if cfg, _ := m.LoadConfig(); cfg.DefaultModel != "v41-q4" {
		t.Errorf("DefaultModel = %q, want v41-q4", cfg.DefaultModel)
	}
}

// An interrupted join leaves <file>.assembling holding part1 plus some of
// part2. Rerunning truncates back to part1's size and appends again rather
// than re-downloading part1.
func TestDownloadResumesAnInterruptedJoin(t *testing.T) {
	m, model, p1, p2 := splitFixture(t)
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(m.ModelsDir, model.FileName)
	if err := os.WriteFile(out+".assembling", []byte(p1+p2[:100]), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out+".part2", []byte(p2), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, fetched := splitServer(t, map[string]string{model.FileName + ".part1": p1, model.FileName + ".part2": p2})
	withRepo(t, srv.URL)
	if _, err := m.Download(context.Background(), "v41-q4", "", false); err != nil {
		t.Fatalf("Download: %v", err)
	}
	data, _ := os.ReadFile(out)
	if string(data) != p1+p2 {
		t.Errorf("joined file has %d bytes, want %d", len(data), len(p1+p2))
	}
	for _, f := range *fetched {
		if strings.HasSuffix(f, ".part1") {
			t.Error("part1 was re-downloaded although the assembling file already held it")
		}
	}
}

func TestDownloadRejectsAJoinThatDoesNotMatchTheCatalogHash(t *testing.T) {
	m, model, _, _ := splitFixture(t)
	setCuratedHash(t, "v41-q4", strings.Repeat("0", 64))
	_, err := m.Download(context.Background(), "v41-q4", "", false)
	if err == nil {
		t.Fatal("Download accepted a joined file with the wrong hash")
	}
	if _, statErr := os.Stat(filepath.Join(m.ModelsDir, model.FileName)); !os.IsNotExist(statErr) {
		t.Error("a mismatched joined file was left in place as the model")
	}
}

// Partial state and leftovers understand the pieces of a split download.
func TestSplitPartsCountAsPartialAndLeftovers(t *testing.T) {
	m, model, p1, _ := splitFixture(t)
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(m.ModelsDir, model.FileName)
	if err := os.WriteFile(out+".part1", []byte(p1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out+".part2.part", []byte("bb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if partial, n := m.partial(model); !partial || n != int64(len(p1)+2) {
		t.Errorf("partial = (%v, %d), want (true, %d)", partial, n, len(p1)+2)
	}
	items, err := m.Leftovers()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]LeftoverKind{}
	for _, it := range items {
		kinds[filepath.Base(it.Path)] = it.Kind
	}
	if kinds[model.FileName+".part1"] != LeftoverPartial || kinds[model.FileName+".part2.part"] != LeftoverPartial {
		t.Errorf("leftovers = %v, want both split pieces listed as partial", kinds)
	}
	if err := os.WriteFile(out+".assembling", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	items, _ = m.Leftovers()
	found := false
	for _, it := range items {
		if strings.HasSuffix(it.Path, ".assembling") && it.Kind == LeftoverPartial {
			found = true
		}
	}
	if !found {
		t.Error("an .assembling file is not listed as a partial leftover")
	}
}

func TestDryRunListsSplitParts(t *testing.T) {
	m, model, _, _ := splitFixture(t)
	var out strings.Builder
	m.Out = &out
	if _, err := m.DownloadDryRun(context.Background(), "v41-q4", ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{model.FileName + ".part1", model.FileName + ".part2", "join"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out.String())
		}
	}
}

// splitPieces lays down every kind of on-disk piece a split download can
// leave: a complete published part, a resume file for the next part, and an
// interrupted join.
func splitPieces(t *testing.T, m *Manager, model Model) []string {
	t.Helper()
	out := filepath.Join(m.ModelsDir, model.FileName)
	pieces := []string{out + ".part1", out + ".part2.part", out + ".assembling"}
	for _, p := range pieces {
		writeSized(t, p, 5)
	}
	return pieces
}

// DeletePartial removes all of a split download's pieces, not only the
// joined name's ".part", and leaves the installed file alone.
func TestDeletePartialRemovesSplitPieces(t *testing.T) {
	m, model, _, _ := splitFixture(t)
	out := filepath.Join(m.ModelsDir, model.FileName)
	writeSized(t, out, 10)
	pieces := splitPieces(t, m, model)
	if err := m.DeletePartial("v41-q4"); err != nil {
		t.Fatalf("DeletePartial: %v", err)
	}
	for _, p := range pieces {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still present", filepath.Base(p))
		}
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("installed file was removed")
	}
	if p, _ := m.partial(model); p {
		t.Error("still reported as partial after DeletePartial")
	}
	if err := m.DeletePartial("v41-q4"); err == nil || !strings.Contains(err.Error(), "no partial download") {
		t.Errorf("second call: err = %v", err)
	}
}

// Delete removes the installed split model and every piece of a download in
// flight for it, and clears the default when the model was the default.
func TestDeleteRemovesSplitPiecesAndClearsDefault(t *testing.T) {
	m, model, _, _ := splitFixture(t)
	out := filepath.Join(m.ModelsDir, model.FileName)
	writeSized(t, out, 10)
	if err := m.Set("v41-q4"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	pieces := splitPieces(t, m, model)
	if err := m.Delete("v41-q4"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, p := range append(pieces, out) {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still present", filepath.Base(p))
		}
	}
	cfg, err := m.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "" {
		t.Errorf("DefaultModel = %q, want cleared", cfg.DefaultModel)
	}
	if _, err := os.Lstat(filepath.Join(m.ModelsDir, DefaultModelSymlink)); !os.IsNotExist(err) {
		t.Error("default link still present")
	}
	// Pieces alone (nothing installed) are enough for Delete to act on.
	splitPieces(t, m, model)
	if err := m.Delete("v41-q4"); err != nil {
		t.Fatalf("Delete of pieces only: %v", err)
	}
	if p, _ := m.partial(model); p {
		t.Error("pieces remain after Delete")
	}
}
