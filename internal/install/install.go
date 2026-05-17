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
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/NimbleMarkets/ds4go"
)

const (
	// DefaultRepo is the GitHub repository used for prebuilt libds4 binaries.
	DefaultRepo = "NimbleMarkets/ds4"

	defaultUserAgent = "ds4go installer"
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

	if !opts.Force {
		if st, err := os.Stat(res.Library); err == nil && !st.IsDir() {
			return nil, fmt.Errorf("%s already exists; pass --force to replace it", res.Library)
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
	if err := extractLibrary(assetName, data, opts.DestDir, opts.GOOS, opts.Force); err != nil {
		return nil, err
	}
	// Record the installed library's checksum so the loader can detect later
	// tampering or corruption.
	if err := writeLibraryChecksum(res.Library); err != nil {
		fmt.Fprintf(opts.Out, "warning: could not record library checksum: %v\n", err)
	}
	fmt.Fprintf(opts.Out, "installed %s\n", res.Library)
	return res, nil
}

// writeLibraryChecksum writes a "<libPath>.sha256" sidecar with the SHA256 of
// the installed library, which ds4api verifies before loading it.
func writeLibraryChecksum(libPath string) error {
	f, err := os.Open(libPath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return os.WriteFile(libPath+".sha256", []byte(sum+"\n"), 0o600)
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

	body := resp.Body
	var progress *downloadProgress
	if opts.ProgressOut != nil {
		progress = newDownloadProgress(opts.ProgressOut, filepath.Base(req.URL.Path), resp.ContentLength)
		body = progress.Wrap(body)
	}
	data, err := io.ReadAll(body)
	if progress != nil {
		progress.Done(err)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return data, nil
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

func defaultBackend(goos, goarch string) string {
	if goos == "darwin" && goarch == "arm64" {
		return "metal"
	}
	return "cpu"
}
