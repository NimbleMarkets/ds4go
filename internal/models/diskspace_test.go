package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
