package openaiserver

import (
	"os"
	"path/filepath"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

func TestCacheIdentityTracksModelRuntimeOptionsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "model.gguf")
	library := filepath.Join(dir, "libds4.so")
	for _, path := range []string{model, library} {
		if err := os.WriteFile(path, []byte("one"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetPath(library)
	previous := ds4api.CurrentDefaultLibrary()
	ds4.SetDefaultLibrary(lib)
	defer ds4.SetDefaultLibrary(previous)
	opts := ds4.EngineOptions{ModelPath: model, ContextSize: 2048}
	cfg := &cliopts.ServerConfig{}
	identity := func() string {
		t.Helper()
		key, err := cacheNamespace(cfg, opts)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	before := identity()
	if identity() != before {
		t.Fatal("unstable identity")
	}
	opts.ContextSize = 4096
	if identity() == before {
		t.Fatal("context change reused cache")
	}
	opts.ContextSize = 2048
	if err := os.WriteFile(model, []byte("different model"), 0600); err != nil {
		t.Fatal(err)
	}
	after := identity()
	if after == before {
		t.Fatal("changed weights reused cache")
	}
	before = after
	if err := os.WriteFile(library, []byte("different runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	after = identity()
	if after == before {
		t.Fatal("changed runtime reused cache")
	}
	before = after
	t.Setenv("DS4_TEST_CACHE_PRECISION", "changed")
	if identity() == before {
		t.Fatal("changed runtime environment reused cache")
	}
}
