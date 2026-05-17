package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
