package ds4_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
)

func catalogSnapshot(t *testing.T) map[string]ds4.ModelInfo {
	t.Helper()
	entries, err := ds4.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]ds4.ModelInfo, len(entries))
	for _, m := range entries {
		if (m.Family == "") != m.Optional {
			t.Errorf("family grouping missing or assigned to a companion: %+v", m)
		}
		if m.Alias == "" || result[m.Alias].Alias != "" {
			t.Fatalf("empty or duplicate alias: %q", m.Alias)
		}
		result[m.Alias] = m
	}
	return result
}

func TestPublicModelCatalogFreshInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("DS4_DIR", dir)
	entries := catalogSnapshot(t)
	vision := entries["vision-q2"]
	if !vision.IsChatModel() || !vision.Vision || vision.Encoder != "vision-encoder" || vision.DSpark != "vision-dspark-support" {
		t.Fatalf("vision selector metadata: %+v", vision)
	}
	if vision.Notes == "" || vision.RecommendedRAM == "" || vision.SizeGB <= 0 {
		t.Fatalf("missing display metadata: %+v", vision)
	}
	for alias, want := range map[string]string{
		"q2-imatrix":    "deepseek-v4-flash",
		"q2-q4-imatrix": "deepseek-v4-flash",
		"vision-q2":     "deepseek-flash-vision",
		"glm53-q2":      "glm-5.3-flash",
		"glm53-full-q2": "glm-5.3",
		"v41-q2":        "deepseek-v4.1-flash",
	} {
		if got := entries[alias].Family; got != want {
			t.Errorf("%s: Family = %q, want %q", alias, got, want)
		}
	}
	for _, alias := range []string{"vision-encoder", "vision-dspark-support", "mtp", "dspark-support", "v41-vision", "glm53-vision"} {
		if m := entries[alias]; m.Alias == "" || m.IsChatModel() {
			t.Errorf("companion offered as chat model: %q", alias)
		}
	}
	if (ds4.ModelInfo{}).IsChatModel() {
		t.Error("zero value is not a chat model")
	}
	for _, m := range entries {
		if m.Installed || m.Default || m.Partial || m.PartialBytes != 0 {
			t.Errorf("unexpected local state: %+v", m)
		}
		if m.Path != filepath.Join(dir, "models", m.FileName) {
			t.Errorf("incorrect destination: %+v", m)
		}
		if m.Distributed && m.IsChatModel() {
			t.Errorf("distributed piece offered for standalone chat: %q", m.Alias)
		}
		if m.Vision && entries[m.Encoder].Alias == "" {
			t.Errorf("missing encoder entry for %q", m.Alias)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("listing created installation: %v", err)
	}
}

func TestPublicModelCatalogLocalState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	before := catalogSnapshot(t)
	if err := os.MkdirAll(ds4.DefaultModelsDir(), 0755); err != nil {
		t.Fatal(err)
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	model := before["vision-q2"]
	write(model.Path, "model fixture")
	write(before["vision-encoder"].Path, "encoder fixture")
	write(before["vision-q2-q4"].Path+".part", "partial")
	write(before["q2-imatrix"].Path, "") // Empty files are unavailable.
	if err := os.Mkdir(before["mtp"].Path, 0755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, "ds4go.json"), `{"defaultModel":"vision-q2","models":{"vision-q2":{"sha256":"saved-hash"}}}`)
	after := catalogSnapshot(t)
	selected := after["vision-q2"]
	if !selected.Installed || !selected.Default || selected.SHA256 != "saved-hash" {
		t.Fatalf("installed/default state: %+v", selected)
	}
	if encoder := after[selected.Encoder]; !encoder.Installed || encoder.IsChatModel() {
		t.Fatalf("encoder state: %+v", encoder)
	}
	if partial := after["vision-q2-q4"]; !partial.Partial || partial.PartialBytes != 7 || partial.Installed {
		t.Fatalf("partial state: %+v", partial)
	}
	if after["q2-imatrix"].Installed || after["mtp"].Installed {
		t.Fatal("empty file or directory counted as installed")
	}
	if before["vision-q2"].Installed {
		t.Fatal("older snapshot changed")
	}
	if ds4.ResolveModelPath(selected.Alias) != selected.Path {
		t.Fatal("selector path disagrees with alias resolution")
	}
	if err := os.Link(selected.Path, ds4.DefaultModelPath()); err != nil {
		t.Fatal(err)
	}
	if info, ok := ds4.ResolveModelInfo(ds4.DefaultModelPath()); !ok || info != selected {
		t.Fatalf("reverse lookup differs from selector: %+v, %v", info, ok)
	}
	// Returning values prevents a consumer's edits from modifying future catalog reads.
	list, err := ds4.ListModels()
	if err != nil {
		t.Fatal(err)
	}
	list[0].Alias = "consumer-edit"
	if catalogSnapshot(t)["consumer-edit"].Alias != "" {
		t.Fatal("caller mutated catalog")
	}
}

func TestPublicModelCatalogConfigError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DS4_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "ds4go.json"), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	if list, err := ds4.ListModels(); err == nil || list != nil {
		t.Fatalf("expected config error, got %v, %v", list, err)
	}
}
