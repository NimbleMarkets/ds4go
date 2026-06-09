package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/models"
)

func TestCandidateAssetNames(t *testing.T) {
	opts := Options{GOOS: "darwin", GOARCH: "arm64", Backend: "metal"}
	got := candidateAssetNames("v0.1.0", opts)
	want := "libds4-v0.1.0-darwin-arm64-metal.tar.gz"
	if got[0] != want {
		t.Fatalf("candidateAssetNames()[0] = %q, want %q", got[0], want)
	}
	if !contains(got, "libds4-v0.1.0-macos-arm64-metal.tar.gz") {
		t.Fatalf("candidateAssetNames() = %#v, want macos alias", got)
	}
}

func TestCandidateAssetNamesAMD64Alias(t *testing.T) {
	opts := Options{GOOS: "linux", GOARCH: "amd64", Backend: "cuda"}
	got := candidateAssetNames("v0.1.0", opts)
	if !contains(got, "libds4-v0.1.0-linux-x86_64-cuda.tar.gz") {
		t.Fatalf("candidateAssetNames() = %#v, want x86_64 alias", got)
	}
}

func TestCandidateAssetNamesROCm(t *testing.T) {
	opts := Options{GOOS: "linux", GOARCH: "amd64", Backend: "rocm"}
	got := candidateAssetNames("v0.1.0", opts)
	if !contains(got, "libds4-v0.1.0-linux-x86_64-rocm.tar.gz") {
		t.Fatalf("candidateAssetNames() = %#v, want x86_64 ROCm alias", got)
	}
}

func TestCatalogAssetFromReleaseAsset(t *testing.T) {
	got := catalogAssetFromReleaseAsset(asset{
		Name:               "libds4-v0.1.0-linux-x86_64-rocm.tar.gz",
		BrowserDownloadURL: "https://example.com/lib.tar.gz",
		Digest:             "sha256:abc",
	})
	if !got.Parsed {
		t.Fatal("Parsed = false, want true")
	}
	if got.GOOS != "linux" || got.GOARCH != "amd64" || got.Backend != "rocm" || got.Archive != "tar.gz" {
		t.Fatalf("parsed asset = %+v, want linux/amd64/rocm tar.gz", got)
	}
}

func TestCatalogFiltersReleaseAssets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{
				"tag_name": "v0.1.0",
				"assets": [
					{
						"name": "libds4-v0.1.0-linux-x86_64-rocm.tar.gz",
						"browser_download_url": "https://example.com/rocm.tar.gz",
						"digest": "sha256:rocm"
					},
					{
						"name": "libds4-v0.1.0-linux-x86_64-cuda.tar.gz",
						"browser_download_url": "https://example.com/cuda.tar.gz",
						"digest": "sha256:cuda"
					},
					{
						"name": "notes.txt",
						"browser_download_url": "https://example.com/notes.txt"
					}
				]
			}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	got, err := Catalog(context.Background(), Options{
		Repo:    "NimbleMarkets/ds4",
		Version: "v0.1.0",
		Backend: "rocm",
		GOOS:    "linux",
		GOARCH:  "amd64",
		Out:     io.Discard,
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				targetHost:   srv.Listener.Addr().String(),
				targetScheme: "http",
				underlying:   srv.Client().Transport,
			},
		},
	})
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if got.Repo != "NimbleMarkets/ds4" || got.Version != "v0.1.0" {
		t.Fatalf("catalog header = %+v, want repo/version", got)
	}
	if len(got.Assets) != 1 {
		t.Fatalf("len(Assets) = %d, want 1: %+v", len(got.Assets), got.Assets)
	}
	a := got.Assets[0]
	if a.Backend != "rocm" || a.GOOS != "linux" || a.GOARCH != "amd64" || !a.Selected {
		t.Fatalf("asset = %+v, want selected linux/amd64/rocm", a)
	}

	var out bytes.Buffer
	PrintCatalog(&out, got)
	text := out.String()
	if !strings.Contains(text, "Release: v0.1.0") || !strings.Contains(text, "rocm") || !strings.Contains(text, "*") {
		t.Fatalf("PrintCatalog output missing expected fields:\n%s", text)
	}
}

func TestNormalizeUsesDS4DirLib(t *testing.T) {
	t.Setenv("DS4_DIR", "/tmp/custom-ds4")
	opts := normalize(Options{})
	if opts.DestDir != filepath.Join("/tmp/custom-ds4", "lib") {
		t.Fatalf("DestDir = %q, want DS4_DIR/lib", opts.DestDir)
	}
}

func TestNormalizeDefaultsToHomeDS4Lib(t *testing.T) {
	t.Setenv("DS4_DIR", "")
	opts := normalize(Options{})
	if !strings.HasSuffix(opts.DestDir, filepath.Join(".ds4", "lib")) {
		t.Fatalf("DestDir = %q, want ~/.ds4/lib suffix", opts.DestDir)
	}
}

func TestVerifyAssetDigest(t *testing.T) {
	data := []byte("libds4 archive bytes")
	sum := sha256.Sum256(data)
	opts := Options{Out: io.Discard}

	// Matching digest passes.
	ok, err := verifyAssetDigest(opts, "lib.tar.gz", "sha256:"+hex.EncodeToString(sum[:]), data)
	if err != nil || !ok {
		t.Fatalf("matching digest: ok=%v err=%v", ok, err)
	}

	// Mismatched digest is fatal.
	if _, err := verifyAssetDigest(opts, "lib.tar.gz", "sha256:"+strings.Repeat("0", 64), data); err == nil {
		t.Fatal("mismatched digest: want error")
	}

	// A release with no digest warns and continues (not verified).
	if ok, err := verifyAssetDigest(opts, "lib.tar.gz", "", data); err != nil || ok {
		t.Fatalf("missing digest: want ok=false err=nil, got ok=%v err=%v", ok, err)
	}
}

func TestExtractTarGzLibrary(t *testing.T) {
	dir := t.TempDir()
	data := makeTarGz(t, "dist/libds4.dylib", []byte("native-lib"))
	if err := extractLibrary("libds4-v0.1.0-darwin-arm64-metal.tar.gz", data, dir, "darwin", false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "libds4.dylib"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "native-lib" {
		t.Fatalf("installed contents = %q", got)
	}
}

func TestWriteLibraryChecksum(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "libds4.so")
	payload := []byte("native-lib")
	if err := os.WriteFile(lib, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeLibraryChecksum(lib); err != nil {
		t.Fatalf("writeLibraryChecksum: %v", err)
	}
	sum := sha256.Sum256(payload)
	got, err := os.ReadFile(lib + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	if want := hex.EncodeToString(sum[:]) + "\n"; string(got) != want {
		t.Fatalf("sidecar = %q, want %q", got, want)
	}
}

func TestArchivePathIsSafe(t *testing.T) {
	for _, p := range []string{"libds4.so", "dist/libds4.so", "a/b/c.txt"} {
		if !archivePathIsSafe(p) {
			t.Errorf("archivePathIsSafe(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"", "../libds4.so", "a/../../etc/x", "/abs/libds4.so", `\win\x`, "dir/.."} {
		if archivePathIsSafe(p) {
			t.Errorf("archivePathIsSafe(%q) = true, want false", p)
		}
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	data := makeTarGz(t, "../../../tmp/libds4.so", []byte("evil"))
	if err := extractLibrary("x.tar.gz", data, t.TempDir(), "linux", false); err == nil {
		t.Fatal("extractLibrary accepted an archive with a path-traversal member")
	}
}

func TestExtractZipLibrary(t *testing.T) {
	dir := t.TempDir()
	data := makeZip(t, "dist/libds4.dll", []byte("native-lib"))
	if err := extractLibrary("libds4-v0.1.0-windows-amd64-cpu.zip", data, dir, "windows", false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "libds4.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "native-lib" {
		t.Fatalf("installed contents = %q", got)
	}
}

func TestDownloadReportsProgress(t *testing.T) {
	payload := strings.Repeat("x", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	var progress bytes.Buffer
	data, err := download(context.Background(), Options{
		HTTPClient:  srv.Client(),
		ProgressOut: &progress,
	}, srv.URL+"/libds4.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != payload {
		t.Fatal("download returned unexpected payload")
	}
	out := progress.String()
	if !strings.Contains(out, "Downloading:") || !strings.Contains(out, "100.0%") {
		t.Fatalf("progress output = %q, want downloading status and completion", out)
	}
}

func TestDownloadRejectsLargeContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2147483649")
	}))
	defer srv.Close()

	_, err := download(context.Background(), Options{
		HTTPClient: srv.Client(),
	}, srv.URL+"/libds4.tar.gz")
	if err == nil {
		t.Fatal("download succeeded, want size limit error")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("download error = %v, want size limit", err)
	}
}

func TestReadAllLimitedRejectsUnknownLengthOversize(t *testing.T) {
	_, err := readAllLimited(strings.NewReader("123456789"), 8)
	if err == nil {
		t.Fatal("readAllLimited succeeded, want size limit error")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("readAllLimited error = %v, want size limit", err)
	}
}

func makeTarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestDefaultBackendLinux(t *testing.T) {
	origCUDA := isCUDAPresentFunc
	origROCm := isROCmPresentFunc
	defer func() {
		isCUDAPresentFunc = origCUDA
		isROCmPresentFunc = origROCm
	}()

	// 1. Test case: CUDA GPU present on Linux
	isCUDAPresentFunc = func() bool { return true }
	isROCmPresentFunc = func() bool { return false }

	backend := defaultBackend("linux", "amd64")
	if backend != "cuda" {
		t.Errorf("defaultBackend(\"linux\", \"amd64\") with CUDA present = %q, want %q", backend, "cuda")
	}

	// 2. Test case: ROCm GPU present on Linux (no CUDA)
	isCUDAPresentFunc = func() bool { return false }
	isROCmPresentFunc = func() bool { return true }

	backend2 := defaultBackend("linux", "amd64")
	if backend2 != "rocm" {
		t.Errorf("defaultBackend(\"linux\", \"amd64\") with ROCm present = %q, want %q", backend2, "rocm")
	}

	// 3. Test case: Neither CUDA nor ROCm GPU present on Linux
	isCUDAPresentFunc = func() bool { return false }
	isROCmPresentFunc = func() bool { return false }

	backend3 := defaultBackend("linux", "amd64")
	if backend3 != "cpu" {
		t.Errorf("defaultBackend(\"linux\", \"amd64\") with no GPUs = %q, want %q", backend3, "cpu")
	}
}

type mockTransport struct {
	targetHost   string
	targetScheme string
	underlying   http.RoundTripper
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = m.targetScheme
	req.URL.Host = m.targetHost
	return m.underlying.RoundTrip(req)
}

func TestInstallMetadataAndUpgradeFlow(t *testing.T) {
	originalIsTerminal := isTerminalFunc
	defer func() { isTerminalFunc = originalIsTerminal }()

	// We'll mock isTerminalFunc to control interactivity in tests
	var isTerminalVal bool
	isTerminalFunc = func(r io.Reader) bool {
		return isTerminalVal
	}

	// Create a test server to mock GitHub Release requests
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/") {
			// Mock fetchRelease
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{
				"tag_name": "v0.1.20260520",
				"assets": [
					{
						"name": "libds4-v0.1.20260520-macos-arm64-metal.tar.gz",
						"browser_download_url": "http://`+r.Host+`/download/lib.tar.gz",
						"digest": "sha256:4c7d0d087b2de13cd2f0a8d4615b3c5c56c2057d2a58b885ff7489999b90468f"
					}
				]
			}`)
			return
		}
		if strings.Contains(r.URL.Path, "/download/") {
			// Serve mock tar.gz containing libds4.dylib with "native-lib" content
			tarGzData := makeTarGz(t, "dist/libds4.dylib", []byte("native-lib"))
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tarGzData)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	destDir := t.TempDir()
	opts := Options{
		Repo:        "NimbleMarkets/ds4",
		Version:     "v0.1.20260520",
		Backend:     "metal",
		GOOS:        "darwin",
		GOARCH:      "arm64",
		DestDir:     destDir,
		Out:         io.Discard,
		ProgressOut: io.Discard,
		HTTPClient: &http.Client{
			Transport: &mockTransport{
				targetHost:   srv.Listener.Addr().String(),
				targetScheme: "http",
				underlying:   srv.Client().Transport,
			},
		},
		SkipChecksum: true, // skip digest mismatch checks for easier mock asset names
	}

	// 1. Clean Install
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Clean install failed: %v", err)
	}

	libPath := filepath.Join(destDir, "libds4.dylib")
	if _, err := os.Stat(libPath); err != nil {
		t.Fatalf("Library not installed: %v", err)
	}

	// Verify metadata file was written
	metaPath := filepath.Join(destDir, "ds4go-install.json")
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("Metadata file not found: %v", err)
	}

	var meta InstallMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("Failed to parse metadata JSON: %v", err)
	}

	if meta.Version != "v0.1.20260520" || meta.Backend != "metal" || meta.AssetName != "libds4-v0.1.20260520-macos-arm64-metal.tar.gz" {
		t.Errorf("Metadata content mismatch: %+v", meta)
	}

	libSHA, _ := fileSHA256(libPath)
	if meta.SHA256 != libSHA {
		t.Errorf("Metadata SHA256 %q does not match file SHA256 %q", meta.SHA256, libSHA)
	}

	// 2. Re-install same version/asset (should exit successfully as already installed)
	var outBuf bytes.Buffer
	opts.Out = &outBuf
	res2, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Re-installing same version failed: %v", err)
	}
	if res2.Library != res.Library {
		t.Errorf("Expected same library path, got %q", res2.Library)
	}
	if !strings.Contains(outBuf.String(), "already installed") {
		t.Errorf("Expected 'already installed' message, got output: %q", outBuf.String())
	}

	// 3. Try installing a different version/asset on non-interactive terminal (should fail)
	opts.Version = "v0.2.0"
	isTerminalVal = false
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{
				"tag_name": "v0.2.0",
				"assets": [
					{
						"name": "libds4-v0.2.0-macos-arm64-metal.tar.gz",
						"browser_download_url": "http://`+r.Host+`/download/lib.tar.gz",
						"digest": ""
					}
				]
			}`)
			return
		}
		if strings.Contains(r.URL.Path, "/download/") {
			tarGzData := makeTarGz(t, "dist/libds4.dylib", []byte("native-lib-v2"))
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tarGzData)
			return
		}
	}))
	defer srv2.Close()
	opts.HTTPClient = &http.Client{
		Transport: &mockTransport{
			targetHost:   srv2.Listener.Addr().String(),
			targetScheme: "http",
			underlying:   srv2.Client().Transport,
		},
	}

	outBuf.Reset()
	_, err = Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Expected run on non-interactive terminal to fail when library already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Expected 'already exists' error, got: %v", err)
	}

	// 4. Try installing a different version with --force (should succeed)
	opts.Force = true
	res3, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Install with --force failed: %v", err)
	}
	if res3.Library != libPath {
		t.Errorf("Expected library path %q, got %q", libPath, res3.Library)
	}
	// Check content updated
	v2Data, _ := os.ReadFile(libPath)
	if string(v2Data) != "native-lib-v2" {
		t.Errorf("Expected updated library content, got %q", string(v2Data))
	}
	opts.Force = false

	// Let's reset the libds4 to version 1 for prompt tests
	tarGzData := makeTarGz(t, "dist/libds4.dylib", []byte("native-lib"))
	_ = os.WriteFile(libPath, tarGzData, 0o600)
	_ = writeLibraryChecksum(libPath)
	libSHA, _ = fileSHA256(libPath)
	meta.Version = "v0.1.20260520"
	meta.AssetName = "libds4-v0.1.20260520-macos-arm64-metal.tar.gz"
	meta.SHA256 = libSHA
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(metaPath, metaBytes, 0o600)

	// 5. Try installing a different version on interactive terminal, decline prompt
	isTerminalVal = true
	opts.In = bytes.NewBufferString("no\n")
	outBuf.Reset()
	_, err = Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Expected install to fail when user declines prompt")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("Expected cancelled error, got %v", err)
	}

	// 6. Try installing a different version on interactive terminal, accept prompt
	opts.In = bytes.NewBufferString("y\n")
	outBuf.Reset()
	res4, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Expected install with prompt confirmation to succeed: %v", err)
	}
	if res4.Library != libPath {
		t.Errorf("Expected library path %q, got %q", libPath, res4.Library)
	}

	// 7. Unmanaged library check
	// Delete metadata file to simulate unmanaged library
	_ = os.Remove(metaPath)
	// Declining prompt
	isTerminalVal = true
	opts.In = bytes.NewBufferString("n\n")
	_, err = Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Expected install to fail on unmanaged library when prompt declined")
	}

	// Accepting prompt on unmanaged library
	opts.In = bytes.NewBufferString("y\n")
	_, err = Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Expected install to succeed on unmanaged library when prompt accepted: %v", err)
	}
}

func TestValidate(t *testing.T) {
	// Temporarily override loadLibraryFunc
	oldLoad := loadLibraryFunc
	loadLibraryFunc = func(path string) (*ds4api.Library, error) {
		if strings.Contains(path, "load_fail") {
			return nil, errors.New("mock load failure")
		}
		return ds4api.NewMockLibrary(), nil
	}
	defer func() {
		loadLibraryFunc = oldLoad
	}()

	destDir := t.TempDir()
	if err := os.Chmod(destDir, 0o700); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		DestDir: destDir,
		GOOS:    "darwin",
		GOARCH:  "arm64",
		Out:     io.Discard,
	}

	// 1. Validate should fail if the library doesn't exist
	err := Validate(context.Background(), opts)
	if err == nil {
		t.Fatal("Expected error when library does not exist")
	}
	if !strings.Contains(err.Error(), "shared library not found") {
		t.Errorf("Expected 'shared library not found' error, got: %v", err)
	}

	// 2. Create the library file
	libPath := filepath.Join(destDir, "libds4.dylib")
	err = os.WriteFile(libPath, []byte("native-lib-data"), 0o600)
	if err != nil {
		t.Fatalf("Failed to create mock library: %v", err)
	}

	// 3. Successful validation (with warnings about missing sidecar and metadata)
	var outBuf bytes.Buffer
	opts.Out = &outBuf
	err = Validate(context.Background(), opts)
	if err != nil {
		t.Fatalf("Expected validation to succeed, got: %v", err)
	}
	output := outBuf.String()
	if !strings.Contains(output, "Shared library file exists") ||
		!strings.Contains(output, "warning: no checksum sidecar found") ||
		!strings.Contains(output, "warning: no install metadata file found") {
		t.Errorf("Unexpected validation output: %s", output)
	}

	// 4. Validation with sidecar file
	sha, _ := fileSHA256(libPath)
	sidecarPath := libPath + ".sha256"
	_ = os.WriteFile(sidecarPath, []byte(sha), 0o600)

	outBuf.Reset()
	err = Validate(context.Background(), opts)
	if err != nil {
		t.Fatalf("Expected validation to succeed with sidecar, got: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Sidecar checksum file matches") {
		t.Errorf("Expected sidecar verified message, got: %s", outBuf.String())
	}

	// 5. Validation with sidecar mismatch
	_ = os.WriteFile(sidecarPath, []byte("wrong-sha"), 0o600)
	err = Validate(context.Background(), opts)
	if err == nil {
		t.Fatal("Expected validation to fail with checksum mismatch in sidecar")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("Expected checksum mismatch error, got: %v", err)
	}
	_ = os.WriteFile(sidecarPath, []byte(sha), 0o600) // reset

	// 6. Validation with metadata file
	metaPath := filepath.Join(destDir, "ds4go-install.json")
	meta := InstallMetadata{
		Repo:        "NimbleMarkets/ds4",
		Version:     "v0.1.20260520",
		AssetName:   "libds4-v0.1.20260520-macos-arm64-metal.tar.gz",
		AssetURL:    "https://github.com/...",
		Backend:     "metal",
		GOOS:        "darwin",
		GOARCH:      "arm64",
		SHA256:      sha,
		InstalledAt: time.Now(),
	}
	metaBytes, _ := json.Marshal(meta)
	_ = os.WriteFile(metaPath, metaBytes, 0o600)

	outBuf.Reset()
	err = Validate(context.Background(), opts)
	if err != nil {
		t.Fatalf("Expected validation to succeed with metadata, got: %v", err)
	}
	output = outBuf.String()
	if !strings.Contains(output, "Install metadata matches local checksum") ||
		!strings.Contains(output, "[Metadata]") ||
		!strings.Contains(output, "Backend:     metal") {
		t.Errorf("Expected metadata printout, got: %s", output)
	}
	wantFP := "Fingerprint: " + sha[:8]
	if !strings.Contains(output, wantFP) {
		t.Errorf("Expected fingerprint line %q in output, got: %s", wantFP, output)
	}

	// 7. Validation with load failure
	opts.DestDir = filepath.Join(destDir, "load_fail")
	_ = os.MkdirAll(opts.DestDir, 0o755)
	failLibPath := filepath.Join(opts.DestDir, "libds4.dylib")
	_ = os.WriteFile(failLibPath, []byte("native-lib-data"), 0o600)

	err = Validate(context.Background(), opts)
	if err == nil {
		t.Fatal("Expected validation to fail on library load failure")
	}
	if !strings.Contains(err.Error(), "failed to load dynamic library") {
		t.Errorf("Expected dynamic library load error, got: %v", err)
	}
}

func TestUninstall(t *testing.T) {
	originalIsTerminal := isTerminalFunc
	defer func() { isTerminalFunc = originalIsTerminal }()

	var isTerminalVal bool
	isTerminalFunc = func(r io.Reader) bool {
		return isTerminalVal
	}

	destDir := t.TempDir()
	opts := Options{
		DestDir: destDir,
		GOOS:    "darwin",
		GOARCH:  "arm64",
		Out:     io.Discard,
	}

	// 1. Uninstall empty directory reports not installed
	var outBuf bytes.Buffer
	opts.Out = &outBuf
	err := Uninstall(context.Background(), opts)
	if err != nil {
		t.Fatalf("Expected uninstall on empty dir to succeed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "libds4 is not installed") {
		t.Errorf("Expected not installed message, got: %s", outBuf.String())
	}

	// 2. Install files to prepare for uninstall
	libPath := filepath.Join(destDir, "libds4.dylib")
	_ = os.WriteFile(libPath, []byte("native-lib-data"), 0o600)
	sidecarPath := libPath + ".sha256"
	_ = os.WriteFile(sidecarPath, []byte("sha-val"), 0o600)
	metaPath := filepath.Join(destDir, "ds4go-install.json")
	_ = os.WriteFile(metaPath, []byte("{}"), 0o600)

	// 3. Uninstall in interactive terminal, declining prompt
	isTerminalVal = true
	opts.In = bytes.NewBufferString("no\n")
	err = Uninstall(context.Background(), opts)
	if err == nil {
		t.Fatal("Expected uninstall to fail when prompt is declined")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("Expected cancelled error, got: %v", err)
	}

	// Verify files still exist
	if _, err := os.Stat(libPath); err != nil {
		t.Error("Expected library file to still exist")
	}

	// 4. Uninstall in interactive terminal, accepting prompt
	opts.In = bytes.NewBufferString("y\n")
	outBuf.Reset()
	err = Uninstall(context.Background(), opts)
	if err != nil {
		t.Fatalf("Expected uninstall to succeed when prompt is accepted: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Uninstalled libds4 and metadata files") {
		t.Errorf("Expected uninstalled message, got: %s", outBuf.String())
	}

	// Verify files are deleted
	if _, err := os.Stat(libPath); !os.IsNotExist(err) {
		t.Error("Expected library file to be deleted")
	}
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Error("Expected sidecar file to be deleted")
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Error("Expected metadata file to be deleted")
	}

	// 5. Re-create files for non-interactive test
	_ = os.WriteFile(libPath, []byte("native-lib-data"), 0o600)
	_ = os.WriteFile(sidecarPath, []byte("sha-val"), 0o600)
	_ = os.WriteFile(metaPath, []byte("{}"), 0o600)

	// Uninstall in non-interactive terminal without --force (should fail)
	isTerminalVal = false
	err = Uninstall(context.Background(), opts)
	if err == nil {
		t.Fatal("Expected uninstall to fail in non-interactive environment without --force")
	}
	if !strings.Contains(err.Error(), "pass --force to uninstall") {
		t.Errorf("Expected force warning error, got: %v", err)
	}

	// 6. Uninstall with --force (should succeed)
	opts.Force = true
	outBuf.Reset()
	err = Uninstall(context.Background(), opts)
	if err != nil {
		t.Fatalf("Expected forced uninstall to succeed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Uninstalled libds4 and metadata files") {
		t.Errorf("Expected success message, got: %s", outBuf.String())
	}

	// Verify files are deleted
	if _, err := os.Stat(libPath); !os.IsNotExist(err) {
		t.Error("Expected library file to be deleted")
	}
}

func TestParseLsofOutput(t *testing.T) {
	input := `p12289
cds4go-toy-svgpad
ftxt
n/Users/evan/.ds4/lib/libds4.dylib
p14898
cds4go-toy-svgpad
ftxt
n/Users/evan/.ds4/lib/libds4.dylib`

	results, err := parseLsofOutput([]byte(input))
	if err != nil {
		t.Fatalf("parseLsofOutput failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].PID != 12289 || results[0].Name != "ds4go-toy-svgpad" {
		t.Errorf("unexpected result 0: %+v", results[0])
	}
	if len(results[0].Files) != 1 || results[0].Files[0] != "/Users/evan/.ds4/lib/libds4.dylib" {
		t.Errorf("unexpected files for result 0: %v", results[0].Files)
	}

	if results[1].PID != 14898 || results[1].Name != "ds4go-toy-svgpad" {
		t.Errorf("unexpected result 1: %+v", results[1])
	}
}

func TestParseTasklistOutput(t *testing.T) {
	input := `Image Name                     PID Modules
========================= ======== ============================================
ds4go.exe                     4567 libds4.dll
my app.exe                    1234 libds4.dll`

	results, err := parseTasklistOutput(input, "libds4.dll")
	if err != nil {
		t.Fatalf("parseTasklistOutput failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].PID != 4567 || results[0].Name != "ds4go.exe" {
		t.Errorf("unexpected result 0: %+v", results[0])
	}
	if results[1].PID != 1234 || results[1].Name != "my app.exe" {
		t.Errorf("unexpected result 1: %+v", results[1])
	}
}

func TestDefaultModelsDirIgnoresDestDir(t *testing.T) {
	// Models always live under $DS4_DIR/models, regardless of where libds4
	// itself is installed. Previously Status/Validate derived modelsDir from
	// opts.DestDir (--lib), so a custom --lib redirected scanning to the wrong
	// directory. Pin DS4_DIR and verify the helper returns the canonical
	// location.
	t.Setenv("DS4_DIR", "/opt/myds4")
	got := defaultModelsDir()
	want := filepath.Join("/opt/myds4", "models")
	if got != want {
		t.Errorf("defaultModelsDir() = %q, want %q", got, want)
	}
}

func TestStatusNoHolders(t *testing.T) {
	destDir := t.TempDir()
	opts := Options{
		DestDir: destDir,
		GOOS:    "darwin",
		GOARCH:  "arm64",
	}

	// Create mock library file so FindLibraryHolders doesn't immediately return nil, nil
	libName := libraryFileName(opts.GOOS)
	libPath := filepath.Join(destDir, libName)
	err := os.WriteFile(libPath, []byte("native-lib-data"), 0o600)
	if err != nil {
		t.Fatalf("Failed to create mock library: %v", err)
	}

	var outBuf bytes.Buffer
	opts.Out = &outBuf

	err = Status(context.Background(), opts)
	if err != nil {
		t.Fatalf("expected Status to succeed: %v", err)
	}

	output := outBuf.String()
	if !strings.Contains(output, "No active processes are holding onto the library") {
		t.Errorf("expected 'No active processes' in output, got: %s", output)
	}
}

func TestFindDirHoldersNoHolders(t *testing.T) {
	tempDir := t.TempDir()
	holders, err := FindDirHolders(tempDir)
	if err != nil {
		t.Fatalf("expected FindDirHolders to succeed on empty dir, got: %v", err)
	}
	if len(holders) != 0 {
		t.Errorf("expected no holders on empty dir, got: %v", holders)
	}
}

func TestInstallMetadataBackCompatNoKind(t *testing.T) {
	// A metadata file written by an older ds4go version has no "kind" field.
	// Unmarshaling must succeed and the zero-value Kind must be treated as
	// "release" by the rest of the system.
	raw := []byte(`{
		"repo": "NimbleMarkets/ds4",
		"version": "v0.1.0",
		"asset_name": "libds4-v0.1.0-darwin-arm64-metal.tar.gz",
		"asset_url": "https://example.com/lib.tar.gz",
		"backend": "metal",
		"goos": "darwin",
		"goarch": "arm64",
		"sha256": "abc",
		"installed_at": "2026-05-23T12:00:00Z"
	}`)
	var meta InstallMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal legacy metadata: %v", err)
	}
	if meta.Kind != "" {
		t.Fatalf("Kind = %q, want empty (treated as release)", meta.Kind)
	}
	if meta.Source != "" {
		t.Fatalf("Source = %q, want empty for legacy metadata", meta.Source)
	}
}

func TestInstallMetadataPinnedRoundTrip(t *testing.T) {
	in := InstallMetadata{
		Kind:        "pinned",
		Source:      "/Users/dev/build/libds4.dylib",
		Backend:     "metal",
		GOOS:        "darwin",
		GOARCH:      "arm64",
		SHA256:      "deadbeef",
		InstalledAt: time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out InstallMetadata
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip: got %+v, want %+v", out, in)
	}
}

func TestFindDirHoldersNativeLock(t *testing.T) {
	tempDir := t.TempDir()

	modelName := "my-model.gguf"
	modelPath := filepath.Join(tempDir, modelName)
	lockPath := modelPath + ".run.lock"

	lock, err := models.TryLock(lockPath)
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}
	defer lock.Close()

	holders, err := FindDirHolders(tempDir)
	if err != nil {
		t.Fatalf("FindDirHolders failed: %v", err)
	}

	expectedPid := os.Getpid()
	files, ok := holders[expectedPid]
	if !ok {
		t.Fatalf("expected PID %d in holders, got: %v", expectedPid, holders)
	}

	if len(files) != 1 || files[0] != modelPath {
		t.Errorf("expected model path %s, got %v", modelPath, files)
	}
}

func TestReadInstallMetadataMissing(t *testing.T) {
	dir := t.TempDir()
	_, ok, err := readInstallMetadata(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("ok=true for missing metadata file, want false")
	}
}

func TestReadInstallMetadataCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MetadataFileName), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok, err := readInstallMetadata(dir)
	if err == nil {
		t.Fatal("want error for corrupt metadata, got nil")
	}
	if ok {
		t.Fatal("ok=true for corrupt metadata, want false")
	}
}

func TestReadInstallMetadataLegacyNoKind(t *testing.T) {
	dir := t.TempDir()
	raw := `{"repo":"r","version":"v","backend":"metal","goos":"darwin","goarch":"arm64","sha256":"abc","installed_at":"2026-05-23T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, MetadataFileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, ok, err := readInstallMetadata(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	if meta.Kind != "" {
		t.Fatalf("Kind=%q, want empty for legacy metadata", meta.Kind)
	}
}

func TestRunPinIntoEmptyDir(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom-lib"), 0o600); err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	opts := Options{
		Pin:     srcPath,
		Backend: "metal",
		GOOS:    "darwin",
		GOARCH:  "arm64",
		DestDir: destDir,
		Out:     io.Discard,
	}

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("pin install: %v", err)
	}

	libPath := filepath.Join(destDir, "libds4.dylib")
	got, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatalf("read installed lib: %v", err)
	}
	if string(got) != "custom-lib" {
		t.Fatalf("lib contents = %q, want %q", got, "custom-lib")
	}

	sidecar, err := os.ReadFile(libPath + ".sha256")
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if len(strings.TrimSpace(string(sidecar))) != 64 {
		t.Fatalf("sidecar = %q, want 64-char sha256", sidecar)
	}

	meta, ok, err := readInstallMetadata(destDir)
	if err != nil || !ok {
		t.Fatalf("metadata: ok=%v err=%v", ok, err)
	}
	if meta.Kind != KindPinned {
		t.Errorf("Kind = %q, want %q", meta.Kind, KindPinned)
	}
	if meta.Source != srcPath {
		t.Errorf("Source = %q, want %q", meta.Source, srcPath)
	}
	if meta.Backend != "metal" {
		t.Errorf("Backend = %q, want %q", meta.Backend, "metal")
	}
	if meta.Repo != "" || meta.Version != "" || meta.AssetName != "" || meta.AssetURL != "" {
		t.Errorf("release fields should be empty in pinned metadata: %+v", meta)
	}
}

func TestRunPinDefaultsBackendToCustom(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()

	if _, err := Run(context.Background(), Options{
		Pin:     srcPath,
		GOOS:    "darwin",
		GOARCH:  "arm64",
		DestDir: destDir,
		Out:     io.Discard,
		// Backend intentionally empty
	}); err != nil {
		t.Fatalf("pin install: %v", err)
	}
	meta, _, _ := readInstallMetadata(destDir)
	if meta.Backend != "custom" {
		t.Errorf("Backend = %q, want %q (default for pinned)", meta.Backend, "custom")
	}
}

func TestRunPinRejectsConflictingFlags(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()

	cases := []struct {
		name string
		opt  Options
	}{
		{"with-url", Options{Pin: srcPath, URL: "https://example.com/lib.tar.gz", DestDir: destDir, Out: io.Discard}},
		{"with-version", Options{Pin: srcPath, Version: "v0.1.0", DestDir: destDir, Out: io.Discard}},
		{"with-asset", Options{Pin: srcPath, Asset: "lib.tar.gz", DestDir: destDir, Out: io.Discard}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opt.GOOS, tc.opt.GOARCH = "darwin", "arm64"
			if _, err := Run(context.Background(), tc.opt); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestRunPinRejectsMissingSource(t *testing.T) {
	destDir := t.TempDir()
	_, err := Run(context.Background(), Options{
		Pin:     filepath.Join(t.TempDir(), "nope.dylib"),
		GOOS:    "darwin",
		GOARCH:  "arm64",
		DestDir: destDir,
		Out:     io.Discard,
	})
	if err == nil {
		t.Fatal("want error for missing source file, got nil")
	}
}

func TestRunPinRejectsNonRegularSource(t *testing.T) {
	srcDir := t.TempDir() // a directory, not a regular file
	destDir := t.TempDir()
	_, err := Run(context.Background(), Options{
		Pin:     srcDir,
		GOOS:    "darwin",
		GOARCH:  "arm64",
		DestDir: destDir,
		Out:     io.Discard,
	})
	if err == nil {
		t.Fatal("want error for non-regular source, got nil")
	}
}

func TestRunPinDryRunMakesNoChanges(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()

	var outBuf bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Pin: srcPath, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, DryRun: true, Out: &outBuf,
	}); err != nil {
		t.Fatalf("dry-run pin: %v", err)
	}
	if !strings.Contains(outBuf.String(), "would pin") {
		t.Errorf("expected 'would pin' in output, got: %q", outBuf.String())
	}
	if _, err := os.Stat(filepath.Join(destDir, "libds4.dylib")); !os.IsNotExist(err) {
		t.Errorf("dry-run created library: stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, MetadataFileName)); !os.IsNotExist(err) {
		t.Errorf("dry-run created metadata: stat err=%v", err)
	}
}

func TestRunPinOverExistingPinRefusesWithoutForce(t *testing.T) {
	srcA := filepath.Join(t.TempDir(), "libds4.dylib")
	if err := os.WriteFile(srcA, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	srcB := filepath.Join(t.TempDir(), "libds4.dylib")
	if err := os.WriteFile(srcB, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()

	// First pin.
	if _, err := Run(context.Background(), Options{
		Pin: srcA, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}

	// Second pin without --force must refuse.
	_, err := Run(context.Background(), Options{
		Pin: srcB, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	})
	if err == nil {
		t.Fatal("want error for pin-over-pin without --force, got nil")
	}
	if !strings.Contains(err.Error(), "pinned") {
		t.Errorf("error = %q, want message mentioning 'pinned'", err)
	}
	// File should still be A.
	got, _ := os.ReadFile(filepath.Join(destDir, "libds4.dylib"))
	if string(got) != "a" {
		t.Errorf("lib contents after refused repin = %q, want %q", got, "a")
	}
}

func TestRunPinOverExistingPinWithForceRepins(t *testing.T) {
	srcA := filepath.Join(t.TempDir(), "libds4.dylib")
	if err := os.WriteFile(srcA, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	srcB := filepath.Join(t.TempDir(), "libds4.dylib")
	if err := os.WriteFile(srcB, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()

	if _, err := Run(context.Background(), Options{
		Pin: srcA, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{
		Pin: srcB, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Force: true, Out: io.Discard,
	}); err != nil {
		t.Fatalf("pin-over-pin with --force: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(destDir, "libds4.dylib"))
	if string(got) != "b" {
		t.Errorf("after --force repin, lib contents = %q, want %q", got, "b")
	}
	meta, _, _ := readInstallMetadata(destDir)
	abs, _ := filepath.Abs(srcB)
	if meta.Source != abs {
		t.Errorf("Source = %q, want %q", meta.Source, abs)
	}
}

func TestRunReleaseRefusesAgainstPin(t *testing.T) {
	originalIsTerminal := isTerminalFunc
	defer func() { isTerminalFunc = originalIsTerminal }()
	isTerminalFunc = func(io.Reader) bool { return true } // even on TTY, must refuse

	// First, set up a pinned install.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom-lib"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	if _, err := Run(context.Background(), Options{
		Pin: srcPath, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}

	// Now attempt a release install over the pin.
	_, err := Run(context.Background(), Options{
		Repo: "NimbleMarkets/ds4", Version: "v0.1.0", Backend: "metal",
		GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir,
		Out:     io.Discard,
		// no Force
	})
	if err == nil {
		t.Fatal("release install over pin: want error, got nil")
	}
	if !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("error = %q, want message mentioning 'pinned'", err)
	}

	// File should be untouched.
	got, _ := os.ReadFile(filepath.Join(destDir, "libds4.dylib"))
	if string(got) != "custom-lib" {
		t.Fatalf("pinned file modified: %q", got)
	}
}

func TestUninstallPromptText(t *testing.T) {
	destDir := t.TempDir()

	// No metadata: generic prompt.
	if got := uninstallPrompt(destDir); !strings.Contains(got, "Uninstall libds4") || strings.Contains(got, "pinned") {
		t.Errorf("unmanaged prompt = %q, want generic uninstall text", got)
	}

	// Pinned: prompt names source.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{
		Pin: srcPath, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	got := uninstallPrompt(destDir)
	if !strings.Contains(got, "pinned to") {
		t.Errorf("pinned prompt = %q, want mention of 'pinned to'", got)
	}
	abs, _ := filepath.Abs(srcPath)
	if !strings.Contains(got, abs) {
		t.Errorf("pinned prompt = %q, want mention of source %q", got, abs)
	}
}

func TestUninstallPinnedTTYProceeds(t *testing.T) {
	originalIsTerminal := isTerminalFunc
	defer func() { isTerminalFunc = originalIsTerminal }()
	isTerminalFunc = func(io.Reader) bool { return true }

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom-lib"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	if _, err := Run(context.Background(), Options{
		Pin: srcPath, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}

	// Accept the prompt; verify removal.
	if err := Uninstall(context.Background(), Options{
		GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir,
		In:      strings.NewReader("y\n"),
		Out:     io.Discard,
	}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "libds4.dylib")); !os.IsNotExist(err) {
		t.Errorf("library not removed: stat err=%v", err)
	}
}

func TestUninstallPinnedForceSkipsPrompt(t *testing.T) {
	originalIsTerminal := isTerminalFunc
	defer func() { isTerminalFunc = originalIsTerminal }()
	isTerminalFunc = func(io.Reader) bool { return false }

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom-lib"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	if _, err := Run(context.Background(), Options{
		Pin: srcPath, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall(context.Background(), Options{
		GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir,
		Force:   true,
		Out:     io.Discard,
	}); err != nil {
		t.Fatalf("uninstall --force: %v", err)
	}
}

func TestValidateReportsPinnedKind(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "libds4.dylib")
	if err := os.WriteFile(srcPath, []byte("custom-lib"), 0o600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	if err := os.Chmod(destDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{
		Pin: srcPath, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}

	// Stub the loader so Validate doesn't try a real dlopen.
	originalLoad := loadLibraryFunc
	defer func() { loadLibraryFunc = originalLoad }()
	loadLibraryFunc = func(path string) (*ds4api.Library, error) {
		return &ds4api.Library{}, nil
	}

	// Make the lib non-world-writable (test temp dirs already satisfy this on most
	// systems, but be explicit).
	if err := os.Chmod(filepath.Join(destDir, "libds4.dylib"), 0o600); err != nil {
		t.Fatal(err)
	}

	var outBuf bytes.Buffer
	err := Validate(context.Background(), Options{
		GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir,
		Out:     &outBuf,
	})
	if err != nil {
		t.Fatalf("validate: %v\noutput: %s", err, outBuf.String())
	}
	out := outBuf.String()
	if !strings.Contains(out, "Kind:") || !strings.Contains(out, "pinned") {
		t.Errorf("expected 'Kind:' and 'pinned' in output, got: %q", out)
	}
	if !strings.Contains(out, srcPath) {
		t.Errorf("expected source path %q in output, got: %q", srcPath, out)
	}
}

func TestRunPinOverUnmanagedNonTTYRequiresForce(t *testing.T) {
	originalIsTerminal := isTerminalFunc
	defer func() { isTerminalFunc = originalIsTerminal }()
	isTerminalFunc = func(io.Reader) bool { return false }

	destDir := t.TempDir()
	// Plant an unmanaged file (no metadata).
	libPath := filepath.Join(destDir, "libds4.dylib")
	if err := os.WriteFile(libPath, []byte("unmanaged"), 0o600); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "libds4.dylib")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), Options{
		Pin: src, GOOS: "darwin", GOARCH: "arm64",
		DestDir: destDir, Out: io.Discard,
	})
	if err == nil {
		t.Fatal("want error for pin-over-unmanaged on non-TTY without --force, got nil")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %q, want mention of --force", err)
	}
	got, _ := os.ReadFile(libPath)
	if string(got) != "unmanaged" {
		t.Errorf("file modified despite refusal: %q", got)
	}
}
