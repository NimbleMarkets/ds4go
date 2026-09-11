package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/spf13/cobra"
)

// testModelManager returns a manager over a temp models directory where the
// q2-imatrix model is installed and active, and everything else is absent.
func testModelManager(t *testing.T, withDefault bool) *models.Manager {
	t.Helper()
	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var installed models.Model
	for _, m := range models.Curated() {
		if m.Alias == "q2-imatrix" {
			installed = m
		}
	}
	if err := os.WriteFile(filepath.Join(modelsDir, installed.FileName), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withDefault {
		if err := os.Symlink(installed.FileName, filepath.Join(modelsDir, models.DefaultModelSymlink)); err != nil {
			t.Fatal(err)
		}
	}
	return &models.Manager{
		DS4Dir:      dir,
		ModelsDir:   modelsDir,
		ConfigPath:  filepath.Join(dir, models.ConfigFileName),
		Out:         io.Discard,
		ProgressOut: io.Discard,
	}
}

type listDoc struct {
	ModelsDir  string            `json:"modelsDir"`
	LibraryDir string            `json:"libraryDir"`
	Default    *string           `json:"default"`
	Models     []json.RawMessage `json:"models"`
}

func decodeList(t *testing.T, out []byte) (listDoc, []map[string]any) {
	t.Helper()
	var doc listDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("list --json is not valid JSON: %v\n%s", err, out)
	}
	entries := make([]map[string]any, len(doc.Models))
	for i, raw := range doc.Models {
		if err := json.Unmarshal(raw, &entries[i]); err != nil {
			t.Fatal(err)
		}
	}
	return doc, entries
}

func TestModelListJSONShape(t *testing.T) {
	m := testModelManager(t, true)
	var buf bytes.Buffer
	if err := runModelList(&buf, m, modelListOptions{JSON: true}); err != nil {
		t.Fatalf("runModelList: %v", err)
	}
	doc, entries := decodeList(t, buf.Bytes())
	if doc.ModelsDir != m.ModelsDir {
		t.Errorf("modelsDir = %q, want %q", doc.ModelsDir, m.ModelsDir)
	}
	if doc.LibraryDir == "" {
		t.Error("libraryDir is empty")
	}
	if doc.Default == nil || *doc.Default != "q2-imatrix" {
		t.Errorf("default = %v, want q2-imatrix", doc.Default)
	}
	if len(entries) != len(models.Curated()) {
		t.Fatalf("models has %d entries, want the whole catalog (%d)", len(entries), len(models.Curated()))
	}
	var active map[string]any
	for _, e := range entries {
		if e["alias"] == "q2-imatrix" {
			active = e
		}
	}
	if active == nil {
		t.Fatal("q2-imatrix missing from models")
	}
	if active["installed"] != true || active["default"] != true {
		t.Errorf("q2-imatrix installed=%v default=%v, want both true", active["installed"], active["default"])
	}
	if p, _ := active["path"].(string); !strings.HasPrefix(p, m.ModelsDir) {
		t.Errorf("path = %v, want it under %s", active["path"], m.ModelsDir)
	}
}

func TestModelListJSONDefaultIsNullWithoutActiveModel(t *testing.T) {
	m := testModelManager(t, false)
	var buf bytes.Buffer
	if err := runModelList(&buf, m, modelListOptions{JSON: true}); err != nil {
		t.Fatalf("runModelList: %v", err)
	}
	doc, _ := decodeList(t, buf.Bytes())
	if doc.Default != nil {
		t.Errorf("default = %q, want null", *doc.Default)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"default": null`)) {
		t.Errorf("default key must be present as null:\n%s", buf.String())
	}
}

func TestModelListJSONFilters(t *testing.T) {
	m := testModelManager(t, true)
	total := len(models.Curated())

	var buf bytes.Buffer
	if err := runModelList(&buf, m, modelListOptions{JSON: true, Installed: true}); err != nil {
		t.Fatalf("--installed: %v", err)
	}
	_, entries := decodeList(t, buf.Bytes())
	if len(entries) != 1 || entries[0]["alias"] != "q2-imatrix" {
		t.Errorf("--installed models = %d entries, want just q2-imatrix", len(entries))
	}

	buf.Reset()
	if err := runModelList(&buf, m, modelListOptions{JSON: true, Available: true}); err != nil {
		t.Fatalf("--available: %v", err)
	}
	_, entries = decodeList(t, buf.Bytes())
	if len(entries) != total-1 {
		t.Errorf("--available models = %d entries, want %d", len(entries), total-1)
	}
	for _, e := range entries {
		if e["installed"] == true {
			t.Errorf("--available included installed model %v", e["alias"])
		}
	}
}

func TestModelListTextFilters(t *testing.T) {
	m := testModelManager(t, true)
	run := func(opts modelListOptions) string {
		var buf bytes.Buffer
		if err := runModelList(&buf, m, opts); err != nil {
			t.Fatalf("runModelList(%+v): %v", opts, err)
		}
		return buf.String()
	}
	all := run(modelListOptions{})
	for _, want := range []string{"Installed:", "Available to download:", "Default: q2-imatrix", "q2-imatrix", "glm53-q2"} {
		if !strings.Contains(all, want) {
			t.Errorf("default listing lacks %q:\n%s", want, all)
		}
	}
	installed := run(modelListOptions{Installed: true})
	if !strings.Contains(installed, "Installed:") || strings.Contains(installed, "Available to download:") {
		t.Errorf("--installed listing shows the wrong groups:\n%s", installed)
	}
	if strings.Contains(installed, "glm53-q2") {
		t.Errorf("--installed listing shows an uninstalled model:\n%s", installed)
	}
	available := run(modelListOptions{Available: true})
	if strings.Contains(available, "Installed:") || !strings.Contains(available, "Available to download:") {
		t.Errorf("--available listing shows the wrong groups:\n%s", available)
	}
	if strings.Contains(available, "(active default)") {
		t.Errorf("--available listing shows the installed default:\n%s", available)
	}
}

func TestModelListRejectsBothFilters(t *testing.T) {
	m := testModelManager(t, true)
	err := runModelList(io.Discard, m, modelListOptions{Installed: true, Available: true})
	if err == nil {
		t.Fatal("--installed with --available returned nil, want an error")
	}
}

// The flags are real cobra flags, so --help documents them and cobra rejects
// the contradictory pair before the handler runs.
func TestModelListCommandFlags(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	cmd := newModelCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"list", "--installed", "--available", "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("list --installed --available succeeded, want a mutual-exclusion error")
	}
	var list *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "list" {
			list = sub
		}
	}
	if list == nil {
		t.Fatal("no list subcommand")
	}
	for _, name := range []string{"json", "installed", "available", "all"} {
		if list.Flags().Lookup(name) == nil {
			t.Errorf("list has no --%s flag", name)
		}
	}
}
