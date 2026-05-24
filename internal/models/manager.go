package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const stateLockFileName = ".ds4go.state.lock"

// Config is stored at $DS4_DIR/ds4go.json.
type Config struct {
	DefaultModel string           `json:"defaultModel"`
	DS4Dir       string           `json:"ds4Dir"`
	Models       map[string]Model `json:"models"`
}

// Manager manages the local ds4 model directory and config.
type Manager struct {
	DS4Dir      string
	ModelsDir   string
	ConfigPath  string
	HTTPClient  *http.Client
	Out         io.Writer
	ProgressOut io.Writer
}

// NewManager returns a manager rooted at DS4_DIR or ~/.ds4.
func NewManager() *Manager {
	dir := defaultDir()
	return &Manager{
		DS4Dir:      dir,
		ModelsDir:   filepath.Join(dir, "models"),
		ConfigPath:  filepath.Join(dir, ConfigFileName),
		HTTPClient:  defaultHTTPClient(),
		Out:         io.Discard,
		ProgressOut: io.Discard,
	}
}

// DefaultModelPath returns the configured default model path.
func DefaultModelPath() string {
	m := NewManager()
	return filepath.Join(m.ModelsDir, DefaultModelSymlink)
}

// DefaultMTPPath returns the path to the installed MTP companion model,
// or empty string if it is not present.
func DefaultMTPPath() string {
	m := NewManager()
	model, ok := Lookup(MTPAlias)
	if !ok {
		return ""
	}
	p := filepath.Join(m.ModelsDir, model.FileName)
	if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Size() > 0 {
		return p
	}
	return ""
}

// List returns curated models annotated with installed/default state.
func (m *Manager) List() ([]Model, Config, error) {
	cfg, err := m.LoadConfig()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, Config{}, err
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = m.detectDefaultAlias()
	}
	models := Curated()
	for i := range models {
		if saved, ok := cfg.Models[models[i].Alias]; ok {
			if saved.SHA256 != "" {
				models[i].SHA256 = saved.SHA256
			}
		}
		models[i].Installed = m.installed(models[i])
		models[i].Partial, models[i].PartialBytes = m.partial(models[i])
		models[i].Default = models[i].Installed && models[i].Alias == cfg.DefaultModel
	}
	cfg.DS4Dir = m.DS4Dir
	cfg.Models = modelMap(models)
	return models, cfg, nil
}

// LoadConfig reads ds4go.json.
func (m *Manager) LoadConfig() (Config, error) {
	b, err := os.ReadFile(m.ConfigPath)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// SaveConfig writes ds4go.json.
func (m *Manager) SaveConfig(cfg Config) error {
	lock, err := m.lockState()
	if err != nil {
		return err
	}
	defer lock.Close()
	return m.saveConfigLocked(cfg)
}

func (m *Manager) saveConfigLocked(cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(m.ConfigPath), 0o755); err != nil {
		return err
	}
	if cfg.DS4Dir == "" {
		cfg.DS4Dir = m.DS4Dir
	}
	if cfg.Models == nil {
		models, _, err := m.List()
		if err != nil {
			return err
		}
		cfg.Models = modelMap(models)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.ConfigPath, b, 0o644)
}

// Set switches the default model and updates models/ds4flash.gguf.
func (m *Manager) Set(alias string) error {
	lock, err := m.lockState()
	if err != nil {
		return err
	}
	defer lock.Close()
	return m.setLocked(alias)
}

func (m *Manager) setLocked(alias string) error {
	model, ok := lookup(alias)
	if !ok {
		return unknownAlias(alias)
	}
	if model.Optional {
		return fmt.Errorf("%q is optional MTP support and cannot be the default chat model", alias)
	}
	if !m.installed(model) {
		return fmt.Errorf("%s is not installed; run: ds4go model download %s", alias, alias)
	}
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(m.ModelsDir, DefaultModelSymlink)
	target := filepath.Join(m.ModelsDir, model.FileName)
	if err := swapLinkAtomic(target, link); err != nil {
		return fmt.Errorf("update default model: %w", err)
	}

	models, cfg, err := m.List()
	if err != nil {
		return err
	}
	cfg.DefaultModel = alias
	cfg.DS4Dir = m.DS4Dir
	cfg.Models = modelMap(models)
	return m.saveConfigLocked(cfg)
}

// Download downloads a curated model from Hugging Face with resume support.
func (m *Manager) Download(ctx context.Context, alias, token string) (Model, error) {
	model, ok := lookup(alias)
	if !ok {
		return Model{}, unknownAlias(alias)
	}
	if token == "" {
		token = huggingFaceToken()
	}
	if err := os.MkdirAll(m.ModelsDir, 0o755); err != nil {
		return Model{}, err
	}
	out := filepath.Join(m.ModelsDir, model.FileName)
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		if model.SHA256 != "" {
			if meta, err := m.remoteMetadata(ctx, strings.TrimRight(hfRepoBase, "/")+"/"+model.FileName, token); err == nil {
				if err := ensureRemoteHashMatchesPinned(meta.SHA256, model.SHA256, model.FileName); err != nil {
					return Model{}, err
				}
			}
			if err := validateSHA256(out, model.SHA256); err != nil {
				return Model{}, err
			}
			fmt.Fprintf(m.Out, "Already downloaded and verified: %s\n", out)
		} else {
			fmt.Fprintf(m.Out, "Already downloaded: %s\n", out)
		}
		return model, nil
	}
	// Hold an exclusive lock for the duration of the download so a second
	// process cannot race on the same .part file and corrupt it.
	lock, err := TryLock(out + ".lock")
	if err != nil {
		return Model{}, err
	}
	defer lock.Close()

	url := strings.TrimRight(hfRepoBase, "/") + "/" + model.FileName
	sha, err := m.downloadFile(ctx, url, out, token, model.SHA256)
	if err != nil {
		return Model{}, err
	}
	model.SHA256 = sha

	stateLock, err := m.lockState()
	if err != nil {
		return Model{}, err
	}
	defer stateLock.Close()

	// The first inferenceable model becomes the default chat model. Adjunct
	// models (Optional, e.g. mtp) are never eligible, and an existing default
	// is never overridden by a later download.
	if !model.Optional && !m.hasActiveDefaultLocked() {
		if err := m.setLocked(alias); err != nil {
			return Model{}, err
		}
	}
	if sha != "" {
		if models, cfg, err := m.List(); err == nil {
			for i := range models {
				if models[i].Alias == alias {
					models[i].SHA256 = sha
				}
			}
			cfg.Models = modelMap(models)
			_ = m.saveConfigLocked(cfg)
		}
	}
	return model, nil
}

// DownloadDryRun resolves alias and reports what Download would fetch — the
// source URL, destination path, remote size/SHA256, and current local state —
// without downloading anything.
func (m *Manager) DownloadDryRun(ctx context.Context, alias, token string) (Model, error) {
	model, ok := lookup(alias)
	if !ok {
		return Model{}, unknownAlias(alias)
	}
	if token == "" {
		token = huggingFaceToken()
	}
	url := strings.TrimRight(hfRepoBase, "/") + "/" + model.FileName
	out := filepath.Join(m.ModelsDir, model.FileName)

	fmt.Fprintf(m.Out, "Dry run: would download %s\n", model.Alias)
	fmt.Fprintf(m.Out, "  Source:      %s\n", url)
	fmt.Fprintf(m.Out, "  Destination: %s\n", out)
	if model.SHA256 != "" {
		fmt.Fprintf(m.Out, "  Catalog SHA: %s\n", model.SHA256)
	}

	switch {
	case m.installed(model):
		fmt.Fprintln(m.Out, "  Local state: already installed (download would be skipped)")
	default:
		if partial, n := m.partial(model); partial {
			fmt.Fprintf(m.Out, "  Local state: partial file present, %s downloaded (would resume)\n", formatBytes(n))
		} else {
			fmt.Fprintln(m.Out, "  Local state: not present")
		}
	}

	if meta, err := m.remoteMetadata(ctx, url, token); err != nil {
		fmt.Fprintf(m.Out, "  Remote:      metadata unavailable (%v)\n", err)
	} else {
		if meta.Size > 0 {
			fmt.Fprintf(m.Out, "  Remote size: %s\n", formatBytes(meta.Size))
		}
		if meta.SHA256 != "" {
			fmt.Fprintf(m.Out, "  Remote SHA:  %s\n", meta.SHA256)
		}
	}
	return model, nil
}

// Delete removes a curated model's downloaded file and any partial download.
// If the deleted model was the default, the default is cleared.
func (m *Manager) Delete(alias string) error {
	model, ok := lookup(alias)
	if !ok {
		return unknownAlias(alias)
	}
	installed := m.installed(model)
	partial, _ := m.partial(model)
	if !installed && !partial {
		return fmt.Errorf("%s is not installed", alias)
	}

	out := filepath.Join(m.ModelsDir, model.FileName)
	// Hold the download lock so we never delete a file mid-download.
	lock, err := TryLock(out + ".lock")
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return fmt.Errorf("cannot delete %s: a download for it is still in progress — cancel that download first", alias)
		}
		return err
	}
	defer lock.Close()

	stateLock, err := m.lockState()
	if err != nil {
		return err
	}
	defer stateLock.Close()

	if m.installed(model) {
		if err := os.Remove(out); err != nil {
			return err
		}
		fmt.Fprintf(m.Out, "Removed %s\n", out)
	}
	if p, _ := m.partial(model); p {
		if err := os.Remove(out + ".part"); err != nil {
			return err
		}
		fmt.Fprintf(m.Out, "Removed partial download %s\n", out+".part")
	}

	// If the deleted model was the default, drop the link and clear config.
	cfg, err := m.LoadConfig()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if cfg.DefaultModel == alias || m.detectDefaultAlias() == alias {
		_ = os.Remove(filepath.Join(m.ModelsDir, DefaultModelSymlink))
		models, freshCfg, err := m.List()
		if err != nil {
			return err
		}
		freshCfg.DefaultModel = ""
		freshCfg.Models = modelMap(models)
		if err := m.saveConfigLocked(freshCfg); err != nil {
			return err
		}
		fmt.Fprintln(m.Out, "Cleared default model")
	}
	return nil
}

func (m *Manager) lockState() (*FileLock, error) {
	if err := os.MkdirAll(m.DS4Dir, 0o755); err != nil {
		return nil, err
	}
	return LockExclusive(filepath.Join(m.DS4Dir, stateLockFileName))
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func swapLinkAtomic(target, link string) error {
	f, err := os.CreateTemp(filepath.Dir(link), "."+filepath.Base(link)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	defer os.Remove(tmp)

	if err := os.Link(target, tmp); err != nil {
		if err := os.Symlink(target, tmp); err != nil {
			return fmt.Errorf("hard-link failed and symlink fallback failed: %w", err)
		}
	}
	return os.Rename(tmp, link)
}

// hasActiveDefault reports whether an installed model is currently the
// configured default chat model.
func (m *Manager) hasActiveDefault() bool {
	lock, err := m.lockState()
	if err != nil {
		return false
	}
	defer lock.Close()
	return m.hasActiveDefaultLocked()
}

func (m *Manager) hasActiveDefaultLocked() bool {
	list, _, err := m.List()
	if err != nil {
		return false
	}
	for _, model := range list {
		if model.Default {
			return true
		}
	}
	return false
}

func (m *Manager) installed(model Model) bool {
	st, err := os.Stat(filepath.Join(m.ModelsDir, model.FileName))
	return err == nil && !st.IsDir() && st.Size() > 0
}

func (m *Manager) partial(model Model) (bool, int64) {
	st, err := os.Stat(filepath.Join(m.ModelsDir, model.FileName+".part"))
	if err != nil || st.IsDir() || st.Size() <= 0 {
		return false, 0
	}
	return true, st.Size()
}

func (m *Manager) detectDefaultAlias() string {
	link := filepath.Join(m.ModelsDir, DefaultModelSymlink)
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	base := filepath.Base(target)
	for _, model := range Curated() {
		if model.FileName == base {
			return model.Alias
		}
	}
	return ""
}

func (m *Manager) downloadFile(ctx context.Context, url, out, token string, expectedSHA string) (string, error) {
	return m.downloadFileAttempt(ctx, url, out, token, expectedSHA, true)
}

// downloadChunkSize bounds each GET to the largest range the HF Xet CDN will
// satisfy in a single response. Single ranges spanning more than ~200 GiB get
// rejected with HTTP 400, and an open-ended `bytes=N-` request behaves the
// same way for files larger than that cap. 16 GiB keeps us well below the
// ceiling while still amortising redirect/handshake overhead across ~27
// chunks for the 432 GiB Pro model.
const downloadChunkSize int64 = 16 * 1024 * 1024 * 1024

func (m *Manager) downloadFileAttempt(ctx context.Context, url, out, token string, expectedSHA string, allowRestart bool) (string, error) {
	part := out + ".part"
	var start int64
	if st, err := os.Stat(part); err == nil {
		start = st.Size()
		fmt.Fprintf(m.Out, "Resuming partial download: %s (%s)\n", part, formatBytes(start))
	}
	meta, _ := m.remoteMetadata(ctx, url, token)
	if err := ensureRemoteHashMatchesPinned(meta.SHA256, expectedSHA, filepath.Base(out)); err != nil {
		return "", err
	}
	if start > 0 && meta.Size > 0 && start >= meta.Size {
		sha, err := m.promoteCompletePart(part, out, meta, expectedSHA)
		if err != nil {
			if allowRestart && isHashMismatch(err) {
				if qerr := quarantineBadPartial(part, err); qerr != nil {
					return "", qerr
				}
				fmt.Fprintln(m.Out, "Restarting download from byte 0 after hash mismatch")
				return m.downloadFileAttempt(ctx, url, out, token, expectedSHA, false)
			}
			return "", err
		}
		return sha, nil
	}

	flags := os.O_CREATE | os.O_WRONLY
	if start > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	// 0600: model data is written owner-only; the final model keeps this
	// mode after the .part file is renamed into place.
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var pr *progressReader
	if m.ProgressOut != nil {
		pr = newProgressReader(m.ProgressOut, filepath.Base(out), start, meta.Size, http.NoBody)
	}

	first := true
	for {
		end := int64(-1)
		if meta.Size > 0 {
			end = start + downloadChunkSize - 1
			if end >= meta.Size {
				end = meta.Size - 1
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "ds4go model downloader")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if end >= 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
		} else if start > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
		}

		resp, err := m.httpClient().Do(req)
		if err != nil {
			return "", fmt.Errorf("download %s: %w", url, err)
		}

		if start > 0 && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			resp.Body.Close()
			sha, err := m.promoteCompletePart(part, out, meta, expectedSHA)
			if err != nil {
				if allowRestart && isHashMismatch(err) {
					if qerr := quarantineBadPartial(part, err); qerr != nil {
						return "", qerr
					}
					fmt.Fprintln(m.Out, "Restarting download from byte 0 after hash mismatch")
					return m.downloadFileAttempt(ctx, url, out, token, expectedSHA, false)
				}
				return "", fmt.Errorf("download %s: %s; %w", url, resp.Status, err)
			}
			return sha, nil
		}
		if first && start > 0 && resp.StatusCode == http.StatusOK {
			// Server ignored our Range (treated as a full-body GET). Rewind
			// the .part file and stream from byte 0.
			resp.Body.Close()
			if err := f.Truncate(0); err != nil {
				return "", err
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return "", err
			}
			start = 0
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return "", fmt.Errorf("download %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
		}

		if first {
			if expectedSHA == "" {
				expectedSHA = meta.SHA256
			}
			if expectedSHA == "" {
				expectedSHA = sha256FromHeaders(resp.Header)
			} else {
				if err := ensureRemoteHashMatchesPinned(sha256FromHeaders(resp.Header), expectedSHA, filepath.Base(out)); err != nil {
					resp.Body.Close()
					return "", err
				}
			}
			first = false
		}

		var src io.Reader = resp.Body
		if pr != nil {
			pr.SwapReader(resp.Body)
			src = pr
		}
		n, copyErr := io.Copy(f, src)
		resp.Body.Close()
		if copyErr != nil {
			return "", copyErr
		}
		start += n

		// If we don't know the size, one open-ended GET fetched the whole
		// file in a single response, so we're done.
		if meta.Size <= 0 || start >= meta.Size {
			break
		}
	}

	if pr != nil {
		pr.Done(nil)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if expectedSHA != "" {
		if err := validateSHA256(part, expectedSHA); err != nil {
			if allowRestart && isHashMismatch(err) {
				if qerr := quarantineBadPartial(part, err); qerr != nil {
					return "", qerr
				}
				fmt.Fprintln(m.Out, "Restarting download from byte 0 after hash mismatch")
				return m.downloadFileAttempt(ctx, url, out, token, expectedSHA, false)
			}
			return "", err
		}
		fmt.Fprintf(m.Out, "Verified sha256: %s\n", expectedSHA)
	} else {
		fmt.Fprintln(m.Out, "Warning: no Hugging Face SHA256 header found; skipping hash validation")
	}
	return expectedSHA, os.Rename(part, out)
}

type remoteModelMetadata struct {
	Size   int64
	SHA256 string
}

func (m *Manager) remoteMetadata(ctx context.Context, url, token string) (remoteModelMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return remoteModelMetadata{}, err
	}
	req.Header.Set("User-Agent", "ds4go model downloader")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := *m.httpClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return remoteModelMetadata{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 399 {
		return remoteModelMetadata{}, fmt.Errorf("HEAD %s: %s", url, resp.Status)
	}
	size := resp.ContentLength
	if linkedSize := resp.Header.Get("X-Linked-Size"); linkedSize != "" {
		if parsed, err := strconv.ParseInt(linkedSize, 10, 64); err == nil {
			size = parsed
		}
	}
	return remoteModelMetadata{Size: size, SHA256: sha256FromHeaders(resp.Header)}, nil
}

func (m *Manager) httpClient() *http.Client {
	if m.HTTPClient != nil {
		return m.HTTPClient
	}
	return defaultHTTPClient()
}

func defaultHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	tr.ExpectContinueTimeout = 1 * time.Second
	tr.IdleConnTimeout = 90 * time.Second
	// Model files are already content-compressed (GGUF), so transparent gzip
	// adds nothing and only obscures the byte counts our progress reader
	// expects to match Content-Length.
	tr.DisableCompression = true
	return &http.Client{Transport: tr}
}

func (m *Manager) promoteCompletePart(part, out string, meta remoteModelMetadata, expectedSHA string) (string, error) {
	if meta.Size <= 0 {
		return "", fmt.Errorf("remote size is unknown; cannot determine whether partial file is complete")
	}
	st, err := os.Stat(part)
	if err != nil {
		return "", err
	}
	if st.Size() < meta.Size {
		return "", fmt.Errorf("partial file is smaller than remote object: %s / %s", formatBytes(st.Size()), formatBytes(meta.Size))
	}
	if st.Size() > meta.Size {
		return "", fmt.Errorf("partial file is larger than remote object: %s / %s; leaving %s unchanged", formatBytes(st.Size()), formatBytes(meta.Size), part)
	}
	if expectedSHA == "" {
		expectedSHA = meta.SHA256
	}
	if expectedSHA != "" {
		if err := ensureRemoteHashMatchesPinned(meta.SHA256, expectedSHA, filepath.Base(out)); err != nil {
			return "", err
		}
		if err := validateSHA256(part, expectedSHA); err != nil {
			return "", err
		}
		fmt.Fprintf(m.Out, "Verified sha256: %s\n", expectedSHA)
	} else {
		fmt.Fprintln(m.Out, "Warning: remote size matches partial file but no SHA256 header was found")
	}
	fmt.Fprintf(m.Out, "Partial file is already complete; finalizing %s\n", out)
	return expectedSHA, os.Rename(part, out)
}

func ensureRemoteHashMatchesPinned(remoteSHA, pinnedSHA, name string) error {
	if remoteSHA == "" || pinnedSHA == "" {
		return nil
	}
	if !strings.EqualFold(remoteSHA, pinnedSHA) {
		// If we have a mismatch, it might be that we're picking up a Xet hash
		// or a CDN ETag that doesn't match the content SHA256. We'll warn
		// the user but proceed, since we verify the full file hash after
		// the download completes anyway.
		fmt.Fprintf(os.Stderr, "Warning: remote hash for %s (%s) does not match curated catalog (%s); this might be a CDN ETag mismatch. Verification will run after download.\n", name, remoteSHA, pinnedSHA)
		return nil
	}
	return nil
}

var sha256Re = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func sha256FromHeaders(h http.Header) string {
	// Hugging Face LFS/Xet repos provide the content SHA256 in X-Linked-Etag,
	// served only on the resolve URL response (before the CDN redirect). This
	// is the only header we trust as the file's SHA256.
	if value := strings.Trim(h.Get("X-Linked-Etag"), `"`); sha256Re.MatchString(value) {
		return strings.ToLower(value)
	}

	// We deliberately do NOT fall back to the bare ETag. On Xet-backed repos
	// the CDN (cas-bridge.xethub.hf.co) sets ETag to the Xet "reconstruction
	// hash" — a 64-hex string that looks like a SHA256 but is not the file's
	// content hash. Trusting it produced spurious "remote hash does not match
	// curated catalog" warnings on the redirected GET response. When the
	// X-Linked-Etag is absent we report no hash and rely on the pinned
	// catalog SHA256, which is verified against the full file after download.
	return ""
}

func validateSHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return hashMismatchError{Path: path, Got: got, Want: expected}
	}
	return nil
}

type hashMismatchError struct {
	Path string
	Got  string
	Want string
}

func (e hashMismatchError) Error() string {
	return fmt.Sprintf("sha256 mismatch for %s: got %s, want %s", e.Path, e.Got, e.Want)
}

func isHashMismatch(err error) bool {
	var mismatch hashMismatchError
	return errors.As(err, &mismatch)
}

func quarantineBadPartial(path string, err error) error {
	var mismatch hashMismatchError
	if !errors.As(err, &mismatch) {
		return err
	}
	suffix := mismatch.Got
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	dst := strings.TrimSuffix(path, ".part") + ".bad-" + suffix
	if renameErr := os.Rename(path, dst); renameErr != nil {
		return fmt.Errorf("%w; additionally failed to quarantine bad partial: %v", err, renameErr)
	}
	return nil
}

// Lookup returns the curated model for alias, or false if unknown.
func Lookup(alias string) (Model, bool) {
	for _, model := range Curated() {
		if model.Alias == alias {
			return model, true
		}
	}
	return Model{}, false
}

func lookup(alias string) (Model, bool) {
	return Lookup(alias)
}

func defaultDir() string {
	if dir := os.Getenv("DS4_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ds4")
	}
	return ".ds4"
}

func modelMap(models []Model) map[string]Model {
	out := make(map[string]Model, len(models))
	for _, model := range models {
		out[model.Alias] = model
	}
	return out
}

func unknownAlias(alias string) error {
	var aliases []string
	for _, model := range Curated() {
		aliases = append(aliases, model.Alias)
	}
	return fmt.Errorf("unknown model %q; valid aliases: %s", alias, strings.Join(aliases, ", "))
}

func huggingFaceToken() string {
	if token := os.Getenv("HF_TOKEN"); token != "" {
		return token
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".cache", "huggingface", "token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
