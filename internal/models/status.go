package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DownloadState classifies a partial download.
type DownloadState string

const (
	// DownloadRunning: a downloader holds the file's lock and the part file
	// has grown recently.
	DownloadRunning DownloadState = "downloading"
	// DownloadStalled: a downloader holds the lock but nothing has been
	// written for longer than downloadStallAfter (it is likely in a retry
	// backoff or waiting on the network).
	DownloadStalled DownloadState = "stalled"
	// DownloadInterrupted: partial data on disk and no downloader; the next
	// `model download` resumes it.
	DownloadInterrupted DownloadState = "interrupted"
)

// downloadStallAfter is how long a locked part file may sit unchanged before
// it is reported as stalled rather than downloading.
const downloadStallAfter = 60 * time.Second

// DownloadStatus is the state of one catalog model's partial download.
type DownloadStatus struct {
	Alias string        `json:"alias"`
	State DownloadState `json:"state"`
	// PID is the downloader holding the lock, or 0.
	PID int `json:"pid,omitempty"`
	// Bytes is on disk so far; Total is the expected size (catalog, or the
	// sum of split parts), 0 when unknown.
	Bytes int64 `json:"bytes"`
	Total int64 `json:"total,omitempty"`
	// Idle is how long ago the part file last grew.
	Idle time.Duration `json:"-"`
	// BytesPerSec is the rate observed over the sampling window, 0 when not
	// sampled or nothing arrived; ETA follows from it and Total.
	BytesPerSec float64       `json:"bytes_per_sec,omitempty"`
	ETA         time.Duration `json:"-"`
}

// MarshalJSON renders the durations in seconds rather than Go's nanosecond
// integers, for shells and scripts reading `model status --json`.
func (s DownloadStatus) MarshalJSON() ([]byte, error) {
	type plain DownloadStatus
	return json.Marshal(struct {
		plain
		IdleSeconds float64 `json:"idle_seconds"`
		ETASeconds  float64 `json:"eta_seconds,omitempty"`
	}{plain(s), s.Idle.Seconds(), s.ETA.Seconds()})
}

// DownloadStatus reports every catalog model with partial data on disk.
// Liveness comes from the download lock (a dead process releases it),
// progress from the part files, stalls from their modification time, and the
// rate from two size samples `sample` apart (0 skips sampling). Nothing is
// read from the downloader itself, so it works for any process that used
// this package to download.
func (m *Manager) DownloadStatus(sample time.Duration) ([]DownloadStatus, error) {
	type snap struct {
		model Model
		bytes int64
		mtime time.Time
	}
	var first []snap
	for _, model := range Curated() {
		partial, bytes := m.partial(model)
		if !partial || m.installed(model) {
			continue
		}
		first = append(first, snap{model: model, bytes: bytes, mtime: m.partialMtime(model)})
	}
	if len(first) == 0 {
		return nil, nil
	}
	if sample > 0 {
		time.Sleep(sample)
	}
	now := time.Now()
	out := make([]DownloadStatus, 0, len(first))
	for _, s := range first {
		st := DownloadStatus{Alias: s.model.Alias, Total: expectedBytes(s.model)}
		bytes := s.bytes
		mtime := s.mtime
		if sample > 0 {
			_, bytes = m.partial(s.model)
			mtime = m.partialMtime(s.model)
			if grown := bytes - s.bytes; grown > 0 {
				st.BytesPerSec = float64(grown) / sample.Seconds()
			}
		}
		st.Bytes = bytes
		if !mtime.IsZero() {
			st.Idle = now.Sub(mtime)
		}
		pid, err := GetLockHolder(filepath.Join(m.ModelsDir, s.model.FileName+".lock"))
		if err == nil && pid != 0 {
			st.PID = pid
			if st.Idle > downloadStallAfter && st.BytesPerSec == 0 {
				st.State = DownloadStalled
			} else {
				st.State = DownloadRunning
			}
		} else {
			st.State = DownloadInterrupted
		}
		if st.BytesPerSec > 0 && st.Total > st.Bytes {
			st.ETA = time.Duration(float64(st.Total-st.Bytes) / st.BytesPerSec * float64(time.Second))
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

// expectedBytes is the size a complete download should have: the exact
// part sizes for a split model, else the catalog's GiB figure.
func expectedBytes(model Model) int64 {
	if len(model.Parts) > 0 {
		var n int64
		for _, p := range model.Parts {
			n += p.Bytes
		}
		return n
	}
	return int64(model.SizeGB * (1 << 30))
}

// partialMtime is the latest modification time across a model's partial
// files (the resume file, split parts, or an interrupted join).
func (m *Manager) partialMtime(model Model) time.Time {
	var latest time.Time
	consider := func(path string) {
		if st, err := os.Stat(path); err == nil && !st.IsDir() && st.ModTime().After(latest) {
			latest = st.ModTime()
		}
	}
	consider(filepath.Join(m.ModelsDir, model.FileName+".part"))
	consider(filepath.Join(m.ModelsDir, model.FileName+".assembling"))
	for _, part := range model.Parts {
		consider(filepath.Join(m.ModelsDir, part.FileName))
		consider(filepath.Join(m.ModelsDir, part.FileName+".part"))
	}
	return latest
}
