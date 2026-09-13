package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func statusFor(t *testing.T, list []DownloadStatus, alias string) DownloadStatus {
	t.Helper()
	for _, s := range list {
		if s.Alias == alias {
			return s
		}
	}
	t.Fatalf("no status for %s in %+v", alias, list)
	return DownloadStatus{}
}

// DownloadStatus reports every catalog model with a partial download: how
// far it is, whether a downloader holds its lock (and which PID), whether
// the file has stopped growing, and a sampled rate with an ETA.
func TestDownloadStatusReportsLiveInterruptedAndStalled(t *testing.T) {
	m := testManager(t.TempDir())
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	q2, _ := lookup("q2-imatrix")
	enc, _ := lookup("vision-encoder")
	live := filepath.Join(m.ModelsDir, q2.FileName)
	writeSized(t, live+".part", 1000)
	lock, err := TryLock(live + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	interrupted := filepath.Join(m.ModelsDir, enc.FileName)
	writeSized(t, interrupted+".part", 500)

	// Grow the live part during the sample so a rate is observable.
	go func() {
		time.Sleep(60 * time.Millisecond)
		f, _ := os.OpenFile(live+".part", os.O_APPEND|os.O_WRONLY, 0o600)
		_, _ = f.Write(make([]byte, 4000))
		f.Close()
	}()
	list, err := m.DownloadStatus(250 * time.Millisecond)
	if err != nil {
		t.Fatalf("DownloadStatus: %v", err)
	}
	l := statusFor(t, list, "q2-imatrix")
	if l.State != DownloadRunning || l.PID != os.Getpid() || l.Bytes != 5000 || l.Total <= 0 || l.BytesPerSec <= 0 {
		t.Errorf("live = %+v, want running under this PID with 5000 bytes and a positive rate", l)
	}
	if l.ETA <= 0 {
		t.Errorf("live ETA = %v, want positive", l.ETA)
	}
	i := statusFor(t, list, "vision-encoder")
	if i.State != DownloadInterrupted || i.PID != 0 || i.Bytes != 500 || i.BytesPerSec != 0 {
		t.Errorf("interrupted = %+v, want interrupted with no PID or rate", i)
	}
	for _, s := range list {
		if s.Alias == "vision-q2" {
			t.Errorf("a model with nothing on disk was listed: %+v", s)
		}
	}

	// Lock held but no growth for longer than the stall window: stalled.
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(live+".part", old, old); err != nil {
		t.Fatal(err)
	}
	list, err = m.DownloadStatus(0)
	if err != nil {
		t.Fatal(err)
	}
	if s := statusFor(t, list, "q2-imatrix"); s.State != DownloadStalled || s.PID != os.Getpid() || s.Idle < 9*time.Minute {
		t.Errorf("stalled = %+v, want stalled under this PID with ~10 min idle", s)
	}
}

func TestDownloadStatusCoversSplitParts(t *testing.T) {
	m := testManager(t.TempDir())
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	q4, _ := lookup("v41-q4")
	out := filepath.Join(m.ModelsDir, q4.FileName)
	writeSized(t, out+".part1", 300)
	writeSized(t, out+".part2.part", 200)
	list, err := m.DownloadStatus(0)
	if err != nil {
		t.Fatal(err)
	}
	s := statusFor(t, list, "v41-q4")
	if s.State != DownloadInterrupted || s.Bytes != 500 || s.Total != q4.Parts[0].Bytes+q4.Parts[1].Bytes {
		t.Errorf("split status = %+v", s)
	}
}

func TestDownloadStatusJSONUsesSeconds(t *testing.T) {
	b, err := json.Marshal(DownloadStatus{Alias: "a", State: DownloadRunning, Idle: 1500 * time.Millisecond, ETA: 2 * time.Minute, Bytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"idle_seconds":1.5`, `"eta_seconds":120`, `"state":"downloading"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("json %s lacks %s", b, want)
		}
	}
	if strings.Contains(string(b), `"idle":`) || strings.Contains(string(b), `"eta":`) {
		t.Errorf("json still carries nanosecond durations: %s", b)
	}
}
