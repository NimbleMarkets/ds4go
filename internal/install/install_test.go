package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
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

func TestParseChecksum(t *testing.T) {
	sum, ok := parseChecksum("abc123  libds4-v0.1.0-darwin-arm64-metal.tar.gz\n", "libds4-v0.1.0-darwin-arm64-metal.tar.gz")
	if !ok {
		t.Fatal("parseChecksum did not find asset")
	}
	if sum != "abc123" {
		t.Fatalf("checksum = %q, want abc123", sum)
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
