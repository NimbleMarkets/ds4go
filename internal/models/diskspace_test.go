package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Curated models run to hundreds of GiB, so a full volume must be reported
// before the download starts rather than hours in when a write fails.

// downloadServer serves a payload of the given size with the headers the
// downloader reads, and reports whether any body bytes were requested.
func downloadServer(t *testing.T, payload string, sha string) (*httptest.Server, *bool) {
	t.Helper()
	// The downloader validates against the catalog's pinned hash, so point the
	// entry under test at this payload.
	setCuratedHash(t, "q2-imatrix", sha)
	served := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Linked-Size", strconv.Itoa(len(payload)))
		w.Header().Set("X-Linked-Etag", sha)
		if r.Method == http.MethodHead {
			return
		}
		served = true
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &served
}

// withRepo points the downloader at a test server.
func withRepo(t *testing.T, url string) {
	t.Helper()
	old := hfRepoBase
	hfRepoBase = url
	t.Cleanup(func() { hfRepoBase = old })
}

// withAvailableBytes fakes the free space reported for the models volume.
func withAvailableBytes(t *testing.T, fn func(string) (uint64, error)) {
	t.Helper()
	old := availableBytesFunc
	availableBytesFunc = fn
	t.Cleanup(func() { availableBytesFunc = old })
}

func TestDownloadRefusesWhenDiskIsTooSmall(t *testing.T) {
	m := testManager(t.TempDir())
	payload := strings.Repeat("x", 4096)
	srv, served := downloadServer(t, payload, sha256Hex(payload))
	withRepo(t, srv.URL)
	// Room for the payload but not the reserve.
	withAvailableBytes(t, func(string) (uint64, error) {
		return uint64(len(payload)) + diskSpaceReserve - 1, nil
	})

	_, err := m.Download(context.Background(), "q2-imatrix", "", false)
	if err == nil {
		t.Fatal("Download succeeded on a volume too small to hold the model")
	}
	for _, want := range []string{"not enough free space", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if *served {
		t.Error("download streamed bytes despite failing the space check")
	}
}

func TestDownloadProceedsWhenDiskFits(t *testing.T) {
	m := testManager(t.TempDir())
	payload := strings.Repeat("x", 4096)
	srv, _ := downloadServer(t, payload, sha256Hex(payload))
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) {
		return uint64(len(payload)) + diskSpaceReserve, nil
	})

	if _, err := m.Download(context.Background(), "q2-imatrix", "", false); err != nil {
		t.Fatalf("Download with exactly enough space failed: %v", err)
	}
}

func TestDownloadForceOverridesSpaceCheck(t *testing.T) {
	m := testManager(t.TempDir())
	payload := strings.Repeat("x", 4096)
	srv, _ := downloadServer(t, payload, sha256Hex(payload))
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) { return 1, nil })

	if _, err := m.Download(context.Background(), "q2-imatrix", "", true); err != nil {
		t.Fatalf("Download --force failed on a full volume: %v", err)
	}
}

// An unknown size is not evidence of a problem, and a space check that cannot
// run must not block a legitimate download.
func TestDownloadSkipsCheckWhenSpaceIsUnknown(t *testing.T) {
	m := testManager(t.TempDir())
	payload := strings.Repeat("x", 4096)
	srv, _ := downloadServer(t, payload, sha256Hex(payload))
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) {
		return 0, errors.New("statfs unavailable")
	})

	if _, err := m.Download(context.Background(), "q2-imatrix", "", false); err != nil {
		t.Fatalf("Download failed when free space could not be determined: %v", err)
	}
}

// Resuming needs only the remaining bytes, so a part-file that is nearly
// complete must not be blocked by a volume smaller than the whole model.
func TestRequiredDownloadBytesSubtractsPartial(t *testing.T) {
	const total = 1000
	cases := []struct {
		name    string
		total   int64
		partial int64
		want    uint64
	}{
		{"fresh download", total, 0, total + diskSpaceReserve},
		{"half done", total, 400, 600 + diskSpaceReserve},
		{"nearly done", total, 990, 10 + diskSpaceReserve},
		{"partial larger than total", total, total + 50, diskSpaceReserve},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := requiredDownloadBytes(c.total, c.partial); got != c.want {
				t.Errorf("requiredDownloadBytes(%d, %d) = %d, want %d",
					c.total, c.partial, got, c.want)
			}
		})
	}
}

// The syscall wiring itself must work, not just the injected fake.
func TestAvailableBytesReportsRealVolume(t *testing.T) {
	got, err := availableBytes(t.TempDir())
	if err != nil {
		t.Fatalf("availableBytes: %v", err)
	}
	if got == 0 {
		t.Error("availableBytes reported 0 bytes free on the test volume")
	}
}

func TestAvailableBytesRejectsMissingPath(t *testing.T) {
	if _, err := availableBytes(t.TempDir() + "/does/not/exist"); err == nil {
		t.Error("availableBytes on a missing path returned no error")
	}
}

// The models directory may not exist yet -- dry-run never creates it. The
// volume it would live on is what matters, so the lookup must walk up to the
// nearest existing ancestor rather than reporting "no such file or directory".
func TestAvailableBytesForMissingDirUsesExistingAncestor(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "models", "nested", "deeper")

	got, err := availableBytesFor(missing)
	if err != nil {
		t.Fatalf("availableBytesFor(%q): %v", missing, err)
	}
	if got == 0 {
		t.Error("reported 0 bytes free for a not-yet-created directory")
	}
	want, err := availableBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	// Same volume, so the figures should agree closely; allow drift from other
	// processes writing during the test.
	diff := int64(got) - int64(want)
	if diff < 0 {
		diff = -diff
	}
	if diff > 1<<30 {
		t.Errorf("ancestor lookup = %d, direct lookup = %d: different volumes?", got, want)
	}
}

func TestDryRunReportsSpaceForMissingModelsDir(t *testing.T) {
	m := testManager(filepath.Join(t.TempDir(), "not-created-yet"))
	report := m.diskSpaceReport(m.ModelsDir, 1000, 0)
	if strings.Contains(report, "unknown") {
		t.Errorf("space report for a missing models dir = %q, want a real figure", report)
	}
}

// multiServer serves one payload for every curated file and records the
// order in which files were fetched.
func multiServer(t *testing.T, payload string, aliases ...string) (*httptest.Server, *[]string) {
	t.Helper()
	sha := sha256Hex(payload)
	for _, alias := range aliases {
		setCuratedHash(t, alias, sha)
	}
	var fetched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Linked-Size", strconv.Itoa(len(payload)))
		w.Header().Set("X-Linked-Etag", sha)
		if r.Method == http.MethodHead {
			return
		}
		fetched = append(fetched, filepath.Base(r.URL.Path))
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &fetched
}

func TestDownloadManyRejectsUnknownAliasBeforeAnyDownload(t *testing.T) {
	m := testManager(t.TempDir())
	srv, fetched := multiServer(t, "payload", "q2-imatrix")
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) { return 1 << 60, nil })
	got, err := m.DownloadMany(context.Background(), []string{"q2-imatrix", "nope"}, "", false)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want the unknown alias named", err)
	}
	if len(got) != 0 || len(*fetched) != 0 {
		t.Errorf("downloaded %v (fetched %v) despite an unknown alias in the list", got, *fetched)
	}
}

func TestDownloadManyDownloadsInOrder(t *testing.T) {
	m := testManager(t.TempDir())
	srv, fetched := multiServer(t, "payload", "q2-imatrix", "dspark-support")
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) { return 1 << 60, nil })
	got, err := m.DownloadMany(context.Background(), []string{"dspark-support", "q2-imatrix"}, "", false)
	if err != nil {
		t.Fatalf("DownloadMany: %v", err)
	}
	if len(got) != 2 || got[0].Alias != "dspark-support" || got[1].Alias != "q2-imatrix" {
		t.Errorf("returned %v, want both aliases in the order given", got)
	}
	a, _ := lookup("dspark-support")
	b, _ := lookup("q2-imatrix")
	if len(*fetched) != 2 || (*fetched)[0] != a.FileName || (*fetched)[1] != b.FileName {
		t.Errorf("fetched %v, want %s then %s", *fetched, a.FileName, b.FileName)
	}
	for _, alias := range []string{"dspark-support", "q2-imatrix"} {
		model, _ := lookup(alias)
		if !m.installed(model) {
			t.Errorf("%s not installed", alias)
		}
	}
}

func TestDownloadManyStopsAtFirstFailure(t *testing.T) {
	m := testManager(t.TempDir())
	srv, _ := multiServer(t, "payload", "q2-imatrix")
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) { return 1 << 60, nil })
	// The second file's pinned hash does not match what the server sends.
	setCuratedHash(t, "dspark-support", strings.Repeat("0", 64))
	got, err := m.DownloadMany(context.Background(), []string{"q2-imatrix", "dspark-support", "vision-encoder"}, "", false)
	if err == nil || !strings.Contains(err.Error(), "dspark-support") {
		t.Fatalf("err = %v, want the failing alias named", err)
	}
	if len(got) != 1 || got[0].Alias != "q2-imatrix" {
		t.Errorf("installed before the failure = %v, want just q2-imatrix", got)
	}
	if enc, _ := lookup("vision-encoder"); m.installed(enc) {
		t.Error("download continued past the failure")
	}
}

// One model may fit while the set does not: the combined catalog size is
// checked before the first byte moves, so the run does not die hours in.
func TestDownloadManyChecksCombinedSpaceUpFront(t *testing.T) {
	m := testManager(t.TempDir())
	srv, fetched := multiServer(t, "payload", "q2-imatrix", "dspark-support")
	withRepo(t, srv.URL)
	a, _ := lookup("q2-imatrix")
	b, _ := lookup("dspark-support")
	// Enough for the larger one alone (catalog size), not for both.
	avail := uint64(max(a.SizeGB, b.SizeGB)*(1<<30)) + diskSpaceReserve + 1<<20
	withAvailableBytes(t, func(string) (uint64, error) { return avail, nil })
	got, err := m.DownloadMany(context.Background(), []string{"q2-imatrix", "dspark-support"}, "", false)
	if err == nil {
		t.Fatal("DownloadMany succeeded when the set does not fit")
	}
	for _, want := range []string{"not enough free space", "q2-imatrix", "dspark-support", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if len(got) != 0 || len(*fetched) != 0 {
		t.Errorf("downloaded %v (fetched %v) despite failing the combined space check", got, *fetched)
	}
	// --force skips the combined check like the per-download one.
	if _, err := m.DownloadMany(context.Background(), []string{"q2-imatrix", "dspark-support"}, "", true); err != nil {
		t.Errorf("--force: %v", err)
	}
	// A single alias defers to Download's own remote-size check, which passes.
	m2 := testManager(t.TempDir())
	if _, err := m2.DownloadMany(context.Background(), []string{"q2-imatrix"}, "", false); err != nil {
		t.Errorf("single alias: %v", err)
	}
}

// A dropped connection mid-transfer must not abort a multi-hundred-GiB pull:
// the downloader resumes from the bytes it has, as the upstream script's
// re-run does, instead of surfacing "unexpected EOF".
func TestDownloadResumesAfterConnectionDrop(t *testing.T) {
	m := testManager(t.TempDir())
	payload := strings.Repeat("y", 8192)
	setCuratedHash(t, "q2-imatrix", sha256Hex(payload))
	drops := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Linked-Size", strconv.Itoa(len(payload)))
		w.Header().Set("X-Linked-Etag", sha256Hex(payload))
		if r.Method == http.MethodHead {
			return
		}
		start := 0
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			fmt.Sscanf(rng, "bytes=%d-", &start)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
			w.WriteHeader(http.StatusPartialContent)
		}
		body := payload[start:]
		if drops == 0 {
			// Announce the full body, send half, then kill the connection.
			drops++
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write([]byte(body[:len(body)/2]))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	withRepo(t, srv.URL)
	withAvailableBytes(t, func(string) (uint64, error) { return 1 << 60, nil })
	old := downloadRetryBase
	downloadRetryBase = time.Millisecond
	t.Cleanup(func() { downloadRetryBase = old })
	var out strings.Builder
	m.Out = &out
	if _, err := m.Download(context.Background(), "q2-imatrix", "", false); err != nil {
		t.Fatalf("Download after a connection drop: %v\n%s", err, out.String())
	}
	q2, _ := lookup("q2-imatrix")
	data, err := os.ReadFile(filepath.Join(m.ModelsDir, q2.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != payload {
		t.Errorf("file has %d bytes, want %d", len(data), len(payload))
	}
	if drops != 1 || !strings.Contains(out.String(), "retry") {
		t.Errorf("drops=%d, output lacks a retry note:\n%s", drops, out.String())
	}
}
