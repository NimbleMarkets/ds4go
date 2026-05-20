// Package install downloads prebuilt libds4 release assets for the ds4go CLI.
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
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/charmbracelet/x/term"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	// DefaultRepo is the GitHub repository used for prebuilt libds4 binaries.
	DefaultRepo = "NimbleMarkets/ds4"

	defaultUserAgent = "ds4go installer"

	maxReleaseAssetBytes = 2 << 30
)

// Options configures a libds4 installation.
type Options struct {
	Repo         string
	Version      string
	Backend      string
	GOOS         string
	GOARCH       string
	DestDir      string
	URL          string
	Asset        string
	Token        string
	Force        bool
	DryRun       bool
	SkipChecksum bool
	Out          io.Writer
	ProgressOut  io.Writer
	HTTPClient   *http.Client
	In           io.Reader
}

// Result describes the installed release asset.
type Result struct {
	Repo       string
	Version    string
	Backend    string
	GOOS       string
	GOARCH     string
	AssetName  string
	AssetURL   string
	Library    string
	ChecksumOK bool
	DryRun     bool
}

// InstallMetadata holds information about the installed prebuilt release.
type InstallMetadata struct {
	Repo        string    `json:"repo"`
	Version     string    `json:"version"`
	AssetName   string    `json:"asset_name"`
	AssetURL    string    `json:"asset_url"`
	Backend     string    `json:"backend"`
	GOOS        string    `json:"goos"`
	GOARCH      string    `json:"goarch"`
	Digest      string    `json:"digest,omitempty"`
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installed_at"`
}

var isTerminalFunc = func(r io.Reader) bool {
	if file, ok := r.(*os.File); ok {
		return term.IsTerminal(file.Fd())
	}
	return false
}

var loadLibraryFunc = ds4api.Load

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	// Digest is the SHA256 GitHub computes server-side for the asset, in
	// "sha256:<hex>" form. Served over the API channel (not the CDN).
	Digest string `json:"digest"`
}

// Run downloads, verifies when possible, and extracts a prebuilt libds4 asset.
func Run(ctx context.Context, opts Options) (*Result, error) {
	opts = normalize(opts)
	a, version, err := resolveAsset(ctx, opts)
	if err != nil {
		return nil, err
	}
	assetName, assetURL := a.Name, a.BrowserDownloadURL

	res := &Result{
		Repo:      opts.Repo,
		Version:   version,
		Backend:   opts.Backend,
		GOOS:      opts.GOOS,
		GOARCH:    opts.GOARCH,
		AssetName: assetName,
		AssetURL:  assetURL,
		Library:   filepath.Join(opts.DestDir, libraryFileName(opts.GOOS)),
		DryRun:    opts.DryRun,
	}
	if opts.DryRun {
		fmt.Fprintf(opts.Out, "would download %s\n", assetURL)
		fmt.Fprintf(opts.Out, "would install %s\n", res.Library)
		return res, nil
	}

	overwrite := opts.Force
	if _, err := os.Stat(res.Library); err == nil {
		if !opts.Force {
			currentSHA, shaErr := fileSHA256(res.Library)
			metaPath := filepath.Join(opts.DestDir, "ds4go-install.json")
			metaData, metaReadErr := os.ReadFile(metaPath)

			var isManaged bool
			var meta InstallMetadata
			if metaReadErr == nil {
				if json.Unmarshal(metaData, &meta) == nil {
					isManaged = true
				}
			}

			if isManaged && shaErr == nil {
				// Compare all relevant fields to see if already installed and matches checksum
				if meta.Repo == opts.Repo &&
					meta.Version == version &&
					meta.AssetName == assetName &&
					meta.Backend == opts.Backend &&
					meta.GOOS == opts.GOOS &&
					meta.GOARCH == opts.GOARCH &&
					currentSHA == meta.SHA256 {
					fmt.Fprintf(opts.Out, "%s is already installed\n", res.Library)
					return res, nil
				}
				fmt.Fprintf(opts.Out, "A different version of libds4 is currently installed: version %s (%s) -> %s (%s)\n", meta.Version, meta.Backend, version, opts.Backend)
			} else {
				fmt.Fprintf(opts.Out, "An unmanaged library already exists at %s\n", res.Library)
			}

			if isTerminalFunc(opts.In) {
				p := tea.NewProgram(
					confirmPrompt{message: fmt.Sprintf("Replace %s?", res.Library)},
					tea.WithInput(opts.In),
					tea.WithOutput(opts.Out),
				)
				resModel, err := p.Run()
				if err != nil {
					return nil, fmt.Errorf("read prompt response: %w", err)
				}
				model, ok := resModel.(confirmPrompt)
				if !ok || model.cancel || !model.value {
					return nil, fmt.Errorf("install cancelled")
				}
				fmt.Fprintln(opts.Out)
				overwrite = true
			} else {
				return nil, fmt.Errorf("%s already exists; pass --force to replace it", res.Library)
			}
		}
	}

	if err := os.MkdirAll(opts.DestDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", opts.DestDir, err)
	}

	data, err := download(ctx, opts, assetURL)
	if err != nil {
		return nil, err
	}
	if !opts.SkipChecksum && opts.URL == "" {
		ok, err := verifyAssetDigest(opts, assetName, a.Digest, data)
		if err != nil {
			return nil, err
		}
		res.ChecksumOK = ok
	}
	if err := extractLibrary(assetName, data, opts.DestDir, opts.GOOS, overwrite); err != nil {
		return nil, err
	}
	// Record the installed library's checksum so the loader can detect later
	// tampering or corruption.
	if err := writeLibraryChecksum(res.Library); err != nil {
		fmt.Fprintf(opts.Out, "warning: could not record library checksum: %v\n", err)
	}

	// Record metadata
	libSHA, err := fileSHA256(res.Library)
	if err != nil {
		fmt.Fprintf(opts.Out, "warning: could not read installed library for metadata: %v\n", err)
	} else {
		meta := InstallMetadata{
			Repo:        opts.Repo,
			Version:     version,
			AssetName:   assetName,
			AssetURL:    assetURL,
			Backend:     opts.Backend,
			GOOS:        opts.GOOS,
			GOARCH:      opts.GOARCH,
			Digest:      a.Digest,
			SHA256:      libSHA,
			InstalledAt: time.Now(),
		}
		metaPath := filepath.Join(opts.DestDir, "ds4go-install.json")
		metaBytes, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			fmt.Fprintf(opts.Out, "warning: could not marshal install metadata: %v\n", err)
		} else {
			if err := os.WriteFile(metaPath, metaBytes, 0o600); err != nil {
				fmt.Fprintf(opts.Out, "warning: could not write install metadata: %v\n", err)
			}
		}
	}

	fmt.Fprintf(opts.Out, "installed %s\n", res.Library)
	return res, nil
}

// writeLibraryChecksum writes a "<libPath>.sha256" sidecar with the SHA256 of
// the installed library, which ds4api verifies before loading it.
func writeLibraryChecksum(libPath string) error {
	sum, err := fileSHA256(libPath)
	if err != nil {
		return err
	}
	return os.WriteFile(libPath+".sha256", []byte(sum+"\n"), 0o600)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func normalize(opts Options) Options {
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	if opts.Version == "" {
		opts.Version = "latest"
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.Backend == "" || opts.Backend == "auto" {
		opts.Backend = defaultBackend(opts.GOOS, opts.GOARCH)
	}
	if opts.DestDir == "" {
		opts.DestDir = filepath.Join(defaultDir(), "lib")
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 5 * time.Minute}
	}
	if opts.In == nil {
		opts.In = os.Stdin
	}
	return opts
}

func defaultDir() string {
	return ds4.DefaultDir()
}

func resolveAsset(ctx context.Context, opts Options) (asset, string, error) {
	if opts.URL != "" {
		name := filepath.Base(strings.Split(opts.URL, "?")[0])
		if name == "" || name == "." || name == "/" {
			name = opts.Asset
		}
		return asset{Name: name, BrowserDownloadURL: opts.URL}, opts.Version, nil
	}

	rel, err := fetchRelease(ctx, opts)
	if err != nil {
		return asset{}, "", err
	}
	version := rel.TagName
	if opts.Asset != "" {
		for _, a := range rel.Assets {
			if a.Name == opts.Asset {
				return a, version, nil
			}
		}
		return asset{}, "", fmt.Errorf("asset %q not found in %s release %s", opts.Asset, opts.Repo, version)
	}

	expected := candidateAssetNames(version, opts)
	for _, want := range expected {
		for _, a := range rel.Assets {
			if a.Name == want {
				return a, version, nil
			}
		}
	}

	var names []string
	for _, a := range rel.Assets {
		names = append(names, a.Name)
	}
	slices.Sort(names)
	return asset{}, "", fmt.Errorf("no libds4 asset for %s/%s/%s in %s release %s; tried %s; available assets: %s",
		opts.GOOS, opts.GOARCH, opts.Backend, opts.Repo, version, strings.Join(expected, ", "), strings.Join(names, ", "))
}

func fetchRelease(ctx context.Context, opts Options) (*release, error) {
	var endpoint string
	if opts.Version == "latest" {
		endpoint = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", opts.Repo)
	} else {
		endpoint = fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", opts.Repo, opts.Version)
	}
	var rel release
	if err := getJSON(ctx, opts, endpoint, &rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		rel.TagName = opts.Version
	}
	return &rel, nil
}

func candidateAssetNames(version string, opts Options) []string {
	archiveExt := ".tar.gz"
	if opts.GOOS == "windows" {
		archiveExt = ".zip"
	}

	var names []string
	for _, osName := range osAssetNames(opts.GOOS) {
		for _, archName := range archAssetNames(opts.GOARCH) {
			stem := "libds4-" + strings.Join([]string{version, osName, archName, opts.Backend}, "-")
			names = append(names,
				stem+archiveExt,
				strings.TrimPrefix(stem, "lib")+archiveExt,
				fmt.Sprintf("libds4-%s-%s-%s%s", osName, archName, opts.Backend, archiveExt),
			)
		}
	}
	return dedupe(names)
}

func osAssetNames(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"darwin", "macos"}
	default:
		return []string{goos}
	}
}

func archAssetNames(goarch string) []string {
	switch goarch {
	case "amd64":
		return []string{"amd64", "x86_64"}
	default:
		return []string{goarch}
	}
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func getJSON(ctx context.Context, opts Options, url string, dst any) error {
	req, err := newRequest(ctx, opts, url)
	if err != nil {
		return err
	}
	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func download(ctx context.Context, opts Options, url string) ([]byte, error) {
	req, err := newRequest(ctx, opts, url)
	if err != nil {
		return nil, err
	}
	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	if resp.ContentLength > maxReleaseAssetBytes {
		return nil, fmt.Errorf("download %s: asset is %s, exceeds limit %s", url, formatBytes(resp.ContentLength), formatBytes(maxReleaseAssetBytes))
	}

	body := resp.Body
	var progress *downloadProgress
	if opts.ProgressOut != nil {
		progress = newDownloadProgress(opts.ProgressOut, filepath.Base(req.URL.Path), resp.ContentLength)
		body = progress.Wrap(body)
	}
	data, err := readAllLimited(body, maxReleaseAssetBytes)
	if progress != nil {
		progress.Done(err)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return data, nil
}

func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	n, err := io.CopyN(&buf, r, limit+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if n > limit {
		return nil, fmt.Errorf("asset exceeds limit %s", formatBytes(limit))
	}
	return buf.Bytes(), nil
}

func newRequest(ctx context.Context, opts Options, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}
	return req, nil
}

// verifyAssetDigest compares the downloaded asset to the SHA256 digest GitHub
// computes for the release asset and serves over its API — a channel separate
// from the CDN-fronted download. A mismatch is fatal; a release too old to
// carry a digest produces a warning.
func verifyAssetDigest(opts Options, assetName, apiDigest string, data []byte) (bool, error) {
	if apiDigest == "" {
		fmt.Fprintf(opts.Out, "warning: GitHub reported no digest for %s; skipping digest check\n", assetName)
		return false, nil
	}
	want := strings.TrimPrefix(apiDigest, "sha256:")
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return false, fmt.Errorf("sha256 mismatch for %s: downloaded %s, GitHub API reports %s", assetName, got, want)
	}
	fmt.Fprintf(opts.Out, "verified sha256 against the GitHub API digest for %s\n", assetName)
	return true, nil
}

func extractLibrary(assetName string, data []byte, destDir, goos string, force bool) error {
	libName := libraryFileName(goos)
	switch {
	case strings.HasSuffix(assetName, ".zip"):
		return extractZipLibrary(data, destDir, libName, force)
	case strings.HasSuffix(assetName, ".tar.gz"), strings.HasSuffix(assetName, ".tgz"):
		return extractTarGzLibrary(data, destDir, libName, force)
	default:
		if filepath.Base(assetName) == libName {
			return writeFile(filepath.Join(destDir, libName), bytes.NewReader(data), force)
		}
		return fmt.Errorf("unsupported archive %q; expected .tar.gz, .tgz, .zip, or %s", assetName, libName)
	}
}

func extractTarGzLibrary(data []byte, destDir, libName string, force bool) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open tar.gz: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar.gz: %w", err)
		}
		if !archivePathIsSafe(h.Name) {
			return fmt.Errorf("refusing archive with unsafe member path %q", h.Name)
		}
		if filepath.Base(h.Name) != libName || h.FileInfo().IsDir() {
			continue
		}
		return writeFile(filepath.Join(destDir, libName), tr, force)
	}
	return fmt.Errorf("%s not found in archive", libName)
}

func extractZipLibrary(data []byte, destDir, libName string, force bool) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		if !archivePathIsSafe(f.Name) {
			return fmt.Errorf("refusing archive with unsafe member path %q", f.Name)
		}
		if filepath.Base(f.Name) != libName || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open %s in zip: %w", f.Name, err)
		}
		defer rc.Close()
		return writeFile(filepath.Join(destDir, libName), rc, force)
	}
	return fmt.Errorf("%s not found in archive", libName)
}

func writeFile(path string, src io.Reader, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	// 0600: the library is loaded by the installing user only; keeping it
	// owner-writable also prevents third-party tampering before it is loaded.
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// archivePathIsSafe reports whether an archive member name is free of path
// traversal — not absolute and with no ".." component. libds4 archives are
// always extracted to a fixed filename, but a member that escapes the archive
// root signals a tampered or hostile archive, so extraction is refused.
func archivePathIsSafe(name string) bool {
	if name == "" || filepath.IsAbs(name) ||
		strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return false
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return true
}

func libraryFileName(goos string) string {
	switch goos {
	case "darwin":
		return "libds4.dylib"
	case "windows":
		return "libds4.dll"
	default:
		return "libds4.so"
	}
}

var isCUDAPresentFunc = func() bool {
	if _, err := os.Stat("/dev/nvidiactl"); err == nil {
		return true
	}
	if _, err := os.Stat("/dev/nvidia0"); err == nil {
		return true
	}
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return true
	}
	return false
}

var isROCmPresentFunc = func() bool {
	if _, err := os.Stat("/dev/kfd"); err == nil {
		return true
	}
	if _, err := os.Stat("/opt/rocm"); err == nil {
		return true
	}
	return false
}

func defaultBackend(goos, goarch string) string {
	if goos == "darwin" && goarch == "arm64" {
		return "metal"
	}
	if goos == "linux" {
		if isCUDAPresentFunc() {
			return "cuda"
		}
		if isROCmPresentFunc() {
			// Once ROCm is supported upstream, we will return "rocm" here.
			// For now, fall back to "cpu".
			// return "rocm"
		}
		return "cpu"
	}
	return "cpu"
}

// Validate checks the installation of libds4 in opts.DestDir.
// It verifies existence, permissions, checksums, and dynamic loading.
func Validate(ctx context.Context, opts Options) error {
	opts = normalize(opts)

	libName := libraryFileName(opts.GOOS)
	libPath := filepath.Join(opts.DestDir, libName)

	fmt.Fprintf(opts.Out, "Checking library at: %s\n", libPath)

	// 1. Check file existence
	fi, err := os.Stat(libPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("shared library not found at %s. Please run 'ds4go install' first", libPath)
		}
		return fmt.Errorf("stat library: %w", err)
	}

	if !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to load %s: not a regular file", libPath)
	}
	fmt.Fprintf(opts.Out, "✓ Shared library file exists\n")

	// 2. File permissions check (POSIX only)
	if opts.GOOS != "windows" {
		perm := fi.Mode().Perm()
		if perm&0o022 != 0 {
			return fmt.Errorf("refusing to load %s: writable by group/other (%#o); run: chmod go-w %s", libPath, perm, libPath)
		}
		dir := filepath.Dir(libPath)
		if di, err := os.Stat(dir); err == nil {
			if dperm := di.Mode().Perm(); dperm&0o022 != 0 {
				return fmt.Errorf("refusing to load %s: directory %q is writable by group/other (%#o); run: chmod go-w %s", libPath, dir, dperm, dir)
			}
		}
	}
	fmt.Fprintf(opts.Out, "✓ File permissions are secure\n")

	// 3. Compute local SHA256
	localSHA, err := fileSHA256(libPath)
	if err != nil {
		return fmt.Errorf("calculate local sha256: %w", err)
	}

	// Verify sidecar checksum file
	sidecarPath := libPath + ".sha256"
	if scData, err := os.ReadFile(sidecarPath); err == nil {
		wantSHA := strings.TrimSpace(string(scData))
		if !strings.EqualFold(localSHA, wantSHA) {
			return fmt.Errorf("checksum mismatch in sidecar %s: local is %s, sidecar wants %s", sidecarPath, localSHA, wantSHA)
		}
		fmt.Fprintf(opts.Out, "✓ Sidecar checksum file matches\n")
	} else {
		fmt.Fprintf(opts.Out, "warning: no checksum sidecar found at %s.sha256\n", libPath)
	}

	// Verify metadata file
	metaPath := filepath.Join(opts.DestDir, "ds4go-install.json")
	var meta InstallMetadata
	var hasMeta bool
	if mData, err := os.ReadFile(metaPath); err == nil {
		if err := json.Unmarshal(mData, &meta); err == nil {
			hasMeta = true
			if !strings.EqualFold(localSHA, meta.SHA256) {
				return fmt.Errorf("checksum mismatch in install metadata %s: local is %s, metadata wants %s", metaPath, localSHA, meta.SHA256)
			}
			fmt.Fprintf(opts.Out, "✓ Install metadata matches local checksum\n")
		} else {
			fmt.Fprintf(opts.Out, "warning: install metadata file is corrupt: %v\n", err)
		}
	} else {
		fmt.Fprintf(opts.Out, "warning: no install metadata file found at %s\n", metaPath)
	}

	// 4. Dynamic Loading verification
	lib, err := loadLibraryFunc(libPath)
	if err != nil {
		return fmt.Errorf("failed to load dynamic library: %w", err)
	}
	fmt.Fprintf(opts.Out, "✓ Dynamically loaded library and registered symbols\n")

	// Print metadata info
	if hasMeta {
		fmt.Fprintln(opts.Out, "\n[Metadata]")
		fmt.Fprintf(opts.Out, "  Version:     %s\n", meta.Version)
		fmt.Fprintf(opts.Out, "  Backend:     %s\n", meta.Backend)
		fmt.Fprintf(opts.Out, "  Installed:   %s\n", meta.InstalledAt.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(opts.Out, "  Source Repo: %s\n", meta.Repo)
	}

	// Make the loaded library the default one for future calls in the CLI lifecycle
	ds4api.SetDefaultLibrary(lib)

	return nil
}

type confirmPrompt struct {
	message string
	cancel  bool
	value   bool
}

func (m confirmPrompt) Init() tea.Cmd {
	return nil
}

func (m confirmPrompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.cancel = true
			return m, tea.Quit
		case "y", "Y":
			m.value = true
			return m, tea.Quit
		case "n", "N", "enter":
			m.value = false
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmPrompt) View() tea.View {
	boldGreen := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#39FFB6"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#7D8590"))

	prompt := boldGreen.Render("? ") + m.message + muted.Render(" [y/N] ")
	return tea.NewView(prompt)
}
