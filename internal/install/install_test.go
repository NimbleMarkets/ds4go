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
	if backend2 != "cpu" {
		t.Errorf("defaultBackend(\"linux\", \"amd64\") with ROCm present = %q, want %q", backend2, "cpu")
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
