package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func setCuratedHash(t *testing.T, alias, sha string) {
	t.Helper()
	for i := range curated {
		if curated[i].Alias == alias {
			old := curated[i].SHA256
			curated[i].SHA256 = sha
			t.Cleanup(func() { curated[i].SHA256 = old })
			return
		}
	}
	t.Fatalf("unknown curated alias %q", alias)
}

func TestListMarksInstalledAndDefault(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)
	model, _ := lookup("q2-imatrix")
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.ModelsDir, model.FileName), []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("q2-imatrix"); err != nil {
		t.Fatal(err)
	}

	list, cfg, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "q2-imatrix" {
		t.Fatalf("DefaultModel = %q", cfg.DefaultModel)
	}
	found := false
	for _, item := range list {
		if item.Alias == "q2-imatrix" {
			found = true
			if !item.Installed || !item.Default {
				t.Fatalf("q2-imatrix installed/default = %t/%t", item.Installed, item.Default)
			}
		}
	}
	if !found {
		t.Fatal("q2-imatrix not listed")
	}
}

func TestListDoesNotMarkDefaultBeforeInstall(t *testing.T) {
	m := testManager(t.TempDir())
	list, cfg, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "" {
		t.Fatalf("DefaultModel = %q, want empty", cfg.DefaultModel)
	}
	for _, item := range list {
		if item.Default {
			t.Fatalf("%s marked default before install", item.Alias)
		}
	}
}

func TestDownloadWritesModelAndConfig(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)
	model, _ := lookup("q2-imatrix")
	payload := "fake gguf"
	sum := sha256.Sum256([]byte(payload))
	expected := hex.EncodeToString(sum[:])
	setCuratedHash(t, "q2-imatrix", expected)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, model.FileName) {
			t.Fatalf("unexpected URL path %s", r.URL.Path)
		}
		w.Header().Set("X-Linked-Etag", expected)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()
	oldRepo := hfRepoBase
	hfRepoBase = srv.URL
	defer func() { hfRepoBase = oldRepo }()

	if _, err := m.Download(context.Background(), "q2-imatrix", ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(m.ModelsDir, model.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("downloaded payload = %q", got)
	}
	cfg, err := m.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "q2-imatrix" {
		t.Fatalf("DefaultModel = %q", cfg.DefaultModel)
	}
}

func TestRemoteMetadataUsesLinkedETagNotXetHash(t *testing.T) {
	m := testManager(t.TempDir())
	fileSHA := strings.Repeat("a", 64)
	xetHash := strings.Repeat("b", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/model.gguf":
			w.Header().Set("X-Linked-Etag", fileSHA)
			w.Header().Set("X-Xet-Hash", xetHash)
			w.Header().Set("X-Linked-Size", "1234")
			w.Header().Set("Location", "/cas/"+xetHash)
			w.WriteHeader(http.StatusFound)
		case "/cas/" + xetHash:
			w.Header().Set("ETag", xetHash)
			w.Header().Set("Content-Length", "1234")
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	meta, err := m.remoteMetadata(context.Background(), srv.URL+"/model.gguf", "")
	if err != nil {
		t.Fatal(err)
	}
	if meta.SHA256 != fileSHA {
		t.Fatalf("SHA256 = %q, want linked file sha %q", meta.SHA256, fileSHA)
	}
	if meta.Size != 1234 {
		t.Fatalf("Size = %d, want 1234", meta.Size)
	}
}

func TestSHA256FromHeadersIgnoresBareETag(t *testing.T) {
	fileSHA := strings.Repeat("a", 64)
	xetHash := strings.Repeat("b", 64)

	// HF resolve response: X-Linked-Etag is the authoritative content SHA256.
	resolve := http.Header{}
	resolve.Set("X-Linked-Etag", `"`+fileSHA+`"`)
	resolve.Set("X-Xet-Hash", xetHash)
	resolve.Set("ETag", `"`+xetHash+`"`)
	if got := sha256FromHeaders(resolve); got != fileSHA {
		t.Fatalf("resolve response: got %q, want linked sha %q", got, fileSHA)
	}

	// cas-bridge CDN response (after redirect): only a bare ETag, which is the
	// Xet reconstruction hash, not the content SHA256. Must not be trusted.
	cdn := http.Header{}
	cdn.Set("ETag", `"`+xetHash+`"`)
	if got := sha256FromHeaders(cdn); got != "" {
		t.Fatalf("cdn response: got %q, want empty (bare ETag not a content hash)", got)
	}
}

func TestNewManagerUsesBoundedHTTPTransport(t *testing.T) {
	m := NewManager()
	if m.HTTPClient == nil {
		t.Fatal("HTTPClient is nil")
	}
	if m.HTTPClient.Timeout != 0 {
		t.Fatalf("HTTPClient.Timeout = %s, want zero total timeout for large downloads", m.HTTPClient.Timeout)
	}
	tr, ok := m.HTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTPClient.Transport = %T, want *http.Transport", m.HTTPClient.Transport)
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Fatal("ResponseHeaderTimeout is not set")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Fatal("TLSHandshakeTimeout is not set")
	}
	if tr.IdleConnTimeout <= 0 {
		t.Fatal("IdleConnTimeout is not set")
	}
}

func TestListMarksPartialDownload(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)
	model, _ := lookup("q2-imatrix")
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.ModelsDir, model.FileName+".part"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, _, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item.Alias == "q2-imatrix" {
			if !item.Partial || item.PartialBytes != int64(len("partial")) {
				t.Fatalf("partial = %t/%d", item.Partial, item.PartialBytes)
			}
			return
		}
	}
	t.Fatal("q2-imatrix not listed")
}

func TestDownloadQuarantinesHashMismatch(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)
	model, _ := lookup("q2-imatrix")
	setCuratedHash(t, "q2-imatrix", strings.Repeat("0", 64))
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Linked-Etag", strings.Repeat("0", 64))
		if r.Method == http.MethodHead {
			return
		}
		gets++
		_, _ = io.WriteString(w, "wrong bytes")
	}))
	defer srv.Close()
	oldRepo := hfRepoBase
	hfRepoBase = srv.URL
	defer func() { hfRepoBase = oldRepo }()

	if _, err := m.Download(context.Background(), "q2-imatrix", ""); err == nil {
		t.Fatal("Download succeeded, want hash mismatch")
	}
	if _, err := os.Stat(filepath.Join(m.ModelsDir, model.FileName+".part")); err != nil {
		t.Fatalf("partial file not kept: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(m.ModelsDir, model.FileName+".bad-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("bad partial files = %v, want one quarantined file", matches)
	}
	if gets != 2 {
		t.Fatalf("GET count = %d, want initial download plus one restart", gets)
	}
	if _, err := os.Stat(filepath.Join(m.ModelsDir, model.FileName)); !os.IsNotExist(err) {
		t.Fatalf("final file exists after mismatch: %v", err)
	}
}

func TestDownloadPromotesCompletePartialBeforeRange(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)
	model, _ := lookup("mtp")
	payload := "complete model"
	sum := sha256.Sum256([]byte(payload))
	expected := hex.EncodeToString(sum[:])
	setCuratedHash(t, "mtp", expected)
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(m.ModelsDir, model.FileName+".part")
	if err := os.WriteFile(part, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	var getCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.Header().Set("X-Linked-Etag", expected)
		if r.Method == http.MethodGet {
			getCalled = true
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
	}))
	defer srv.Close()
	oldRepo := hfRepoBase
	hfRepoBase = srv.URL
	defer func() { hfRepoBase = oldRepo }()

	if _, err := m.Download(context.Background(), "mtp", ""); err != nil {
		t.Fatal(err)
	}
	if getCalled {
		t.Fatal("GET was called; complete partial should have been promoted after HEAD")
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("partial still exists: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(m.ModelsDir, model.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("final payload = %q", got)
	}
}

func TestSetRejectsMTPDefault(t *testing.T) {
	m := testManager(t.TempDir())
	if err := m.Set("mtp"); err == nil {
		t.Fatal("Set(mtp) succeeded, want error")
	}
}

func TestSaveConfigLeavesValidJSON(t *testing.T) {
	m := testManager(t.TempDir())
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 50; i++ {
		cfg := Config{
			DefaultModel: "q2-imatrix",
			DS4Dir:       m.DS4Dir,
			Models: map[string]Model{
				"q2-imatrix": {Alias: "q2-imatrix", FileName: fmt.Sprintf("model-%d.gguf", i)},
			},
		}
		if err := m.SaveConfig(cfg); err != nil {
			t.Fatalf("SaveConfig(%d): %v", i, err)
		}
		raw, err := os.ReadFile(m.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Config
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("config JSON invalid after save %d: %v", i, err)
		}
	}

	matches, err := filepath.Glob(m.ConfigPath + ".tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temp config files left behind: %v", matches)
	}
}

func TestSetKeepsDefaultModelPathResolvableDuringConcurrentSwaps(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"q2-imatrix", "q4-imatrix"} {
		model, _ := lookup(alias)
		if err := os.WriteFile(filepath.Join(m.ModelsDir, model.FileName), []byte(alias), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Set("q2-imatrix"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		aliases := []string{"q4-imatrix", "q2-imatrix"}
		for i := 0; i < 200; i++ {
			if err := m.Set(aliases[i%len(aliases)]); err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
		}
		close(done)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		link := filepath.Join(m.ModelsDir, DefaultModelSymlink)
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := os.Stat(link); err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
		}
	}()

	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatalf("concurrent Set/resolution failed: %v", err)
	default:
	}
}

func defaultAlias(t *testing.T, m *Manager) string {
	t.Helper()
	list, _, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, model := range list {
		if model.Default {
			return model.Alias
		}
	}
	return ""
}

func TestDownloadDefaultsToFirstInferenceableModel(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)

	// Per-model payloads with matching pinned hashes.
	payloads := map[string]string{}
	for _, alias := range []string{"q2-imatrix", "q4-imatrix", "mtp"} {
		model, _ := lookup(alias)
		payload := "weights-of-" + alias
		payloads[model.FileName] = payload
		sum := sha256.Sum256([]byte(payload))
		setCuratedHash(t, alias, hex.EncodeToString(sum[:]))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, ok := payloads[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		sum := sha256.Sum256([]byte(payload))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.Header().Set("X-Linked-Etag", hex.EncodeToString(sum[:]))
		if r.Method == http.MethodGet {
			io.WriteString(w, payload)
		}
	}))
	defer srv.Close()
	oldRepo := hfRepoBase
	hfRepoBase = srv.URL
	defer func() { hfRepoBase = oldRepo }()

	ctx := context.Background()

	// An adjunct model (mtp) alone never produces a default.
	if _, err := m.Download(ctx, "mtp", ""); err != nil {
		t.Fatalf("download mtp: %v", err)
	}
	if got := defaultAlias(t, m); got != "" {
		t.Fatalf("default after mtp-only download = %q, want none", got)
	}

	// The first inferenceable model becomes the default.
	if _, err := m.Download(ctx, "q2-imatrix", ""); err != nil {
		t.Fatalf("download q2-imatrix: %v", err)
	}
	if got := defaultAlias(t, m); got != "q2-imatrix" {
		t.Fatalf("default after first inferenceable download = %q, want q2-imatrix", got)
	}

	// A later inferenceable download must not steal the default.
	if _, err := m.Download(ctx, "q4-imatrix", ""); err != nil {
		t.Fatalf("download q4-imatrix: %v", err)
	}
	if got := defaultAlias(t, m); got != "q2-imatrix" {
		t.Fatalf("default after second download = %q, want q2-imatrix unchanged", got)
	}
}

func TestDeleteRemovesModelAndPartial(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)
	model, _ := lookup("q2-imatrix")
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(m.ModelsDir, model.FileName)
	if err := os.WriteFile(full, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full+".part", []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("q2-imatrix"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("model file still present: %v", err)
	}
	if _, err := os.Stat(full + ".part"); !os.IsNotExist(err) {
		t.Fatalf("partial file still present: %v", err)
	}
}

func TestDeleteClearsDefault(t *testing.T) {
	dir := t.TempDir()
	m := testManager(dir)
	model, _ := lookup("q2-imatrix")
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.ModelsDir, model.FileName), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("q2-imatrix"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := m.Delete("q2-imatrix"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	cfg, err := m.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DefaultModel != "" {
		t.Fatalf("DefaultModel = %q, want cleared", cfg.DefaultModel)
	}
	if _, err := os.Lstat(filepath.Join(m.ModelsDir, DefaultModelSymlink)); !os.IsNotExist(err) {
		t.Fatalf("default link still present: %v", err)
	}
}

func TestDeleteUnknownAndMissing(t *testing.T) {
	m := testManager(t.TempDir())
	if err := m.Delete("nope"); err == nil {
		t.Fatal("expected error for unknown alias")
	}
	if err := m.Delete("q2-imatrix"); err == nil {
		t.Fatal("expected error for a model that is not installed")
	}
}

func testManager(dir string) *Manager {
	return &Manager{
		DS4Dir:      dir,
		ModelsDir:   filepath.Join(dir, "models"),
		ConfigPath:  filepath.Join(dir, ConfigFileName),
		HTTPClient:  http.DefaultClient,
		Out:         io.Discard,
		ProgressOut: io.Discard,
	}
}
