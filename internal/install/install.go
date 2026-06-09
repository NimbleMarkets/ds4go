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
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/models"
	"github.com/charmbracelet/x/term"
)

const (
	// DefaultRepo is the GitHub repository used for prebuilt libds4 binaries.
	DefaultRepo = "NimbleMarkets/ds4"

	defaultUserAgent = "ds4go installer"

	maxReleaseAssetBytes = 2 << 30
)

const (
	// MetadataFileName is the name of the JSON metadata file written
	// alongside libds4 to record what is installed.
	MetadataFileName = "ds4go-install.json"

	// KindRelease marks metadata for a prebuilt asset downloaded from
	// GitHub. An empty Kind is treated as release for back-compat with
	// metadata written before pinning existed.
	KindRelease = "release"

	// KindPinned marks metadata for a developer-supplied libds4 copied in
	// via `install --pin`.
	KindPinned = "pinned"
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
	Pin          string // local libds4 file to copy in and mark pinned
	Asset        string
	Token        string
	Force        bool
	DryRun       bool
	SkipChecksum bool
	Out          io.Writer
	ProgressOut  io.Writer
	HTTPClient   *http.Client
	In           io.Reader
	Confirm      func(prompt string, defaultYes bool) (bool, error)
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

// CatalogResult describes the libds4 release assets available for installation.
type CatalogResult struct {
	Repo    string         `json:"repo"`
	Version string         `json:"version"`
	Assets  []CatalogAsset `json:"assets"`
}

// CatalogAsset describes one installable libds4 release asset.
type CatalogAsset struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Digest   string `json:"digest,omitempty"`
	GOOS     string `json:"goos,omitempty"`
	GOARCH   string `json:"goarch,omitempty"`
	Backend  string `json:"backend,omitempty"`
	Archive  string `json:"archive,omitempty"`
	Parsed   bool   `json:"parsed"`
	Selected bool   `json:"selected"`
}

// InstallMetadata holds information about the installed libds4.
//
// Kind discriminates between the supported install sources:
//   - "release" (or "" for back-compat with metadata written by older ds4go
//     versions): a prebuilt asset downloaded from GitHub. Repo/Version/
//     AssetName/AssetURL/Digest are populated.
//   - "pinned": a developer-supplied file copied in via `install --pin`.
//     Source holds the absolute path the file was copied from.
type InstallMetadata struct {
	Kind        string    `json:"kind,omitempty"`
	Repo        string    `json:"repo,omitempty"`
	Version     string    `json:"version,omitempty"`
	AssetName   string    `json:"asset_name,omitempty"`
	AssetURL    string    `json:"asset_url,omitempty"`
	Source      string    `json:"source,omitempty"`
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
	if opts.Pin != "" {
		return runPin(ctx, opts)
	}
	// If the dest already holds a pinned libds4, refuse a release install
	// unless --force. This check runs before any network resolution so
	// callers don't pay for the GitHub API call just to be told the pin
	// blocks them.
	if meta, ok, _ := readInstallMetadata(opts.DestDir); ok && meta.Kind == KindPinned {
		libPath := filepath.Join(opts.DestDir, libraryFileName(opts.GOOS))
		if !opts.Force {
			return nil, fmt.Errorf("libds4 at %s is pinned to %s; run 'ds4go uninstall' first or pass --force", libPath, meta.Source)
		}
		fmt.Fprintf(opts.Out, "replacing pinned libds4 (source was %s)\n", meta.Source)
	}
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
			meta, isManaged, _ := readInstallMetadata(opts.DestDir)

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
				var confirmed bool
				if opts.Confirm != nil {
					confirmed, err = opts.Confirm(fmt.Sprintf("Replace %s?", res.Library), false)
				} else {
					confirmed, err = defaultConfirm(fmt.Sprintf("Replace %s?", res.Library), false, opts.In, opts.Out)
				}
				if err != nil {
					return nil, fmt.Errorf("read prompt response: %w", err)
				}
				if !confirmed {
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
			Kind:        KindRelease,
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
		metaPath := filepath.Join(opts.DestDir, MetadataFileName)
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

// Catalog fetches the selected GitHub release and lists available libds4 assets.
// Empty GOOS, GOARCH, and Backend fields mean no filter. Backend "auto" is also
// treated as no filter because the catalog is informational.
func Catalog(ctx context.Context, opts Options) (*CatalogResult, error) {
	opts = normalizeCatalog(opts)
	rel, err := fetchRelease(ctx, opts)
	if err != nil {
		return nil, err
	}
	opts.Version = rel.TagName
	res := &CatalogResult{
		Repo:    opts.Repo,
		Version: rel.TagName,
	}
	for _, a := range rel.Assets {
		ca := catalogAssetFromReleaseAsset(a)
		if !ca.Parsed {
			continue
		}
		if !catalogAssetMatches(ca, opts) {
			continue
		}
		ca.Selected = catalogAssetSelected(ca, opts)
		res.Assets = append(res.Assets, ca)
	}
	slices.SortFunc(res.Assets, func(a, b CatalogAsset) int {
		if a.GOOS != b.GOOS {
			return strings.Compare(a.GOOS, b.GOOS)
		}
		if a.GOARCH != b.GOARCH {
			return strings.Compare(a.GOARCH, b.GOARCH)
		}
		if a.Backend != b.Backend {
			return strings.Compare(a.Backend, b.Backend)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return res, nil
}

// PrintCatalog writes a human-readable install catalog.
func PrintCatalog(out io.Writer, catalog *CatalogResult) {
	if out == nil {
		out = io.Discard
	}
	if catalog == nil {
		return
	}
	style := defaultNimbleStyle()
	fmt.Fprintf(out, "%s %s\n", style.Label("Repo:"), catalog.Repo)
	fmt.Fprintf(out, "%s %s\n\n", style.Label("Release:"), catalog.Version)
	if len(catalog.Assets) == 0 {
		fmt.Fprintln(out, style.Label("No libds4 assets found."))
		return
	}
	w := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
		style.Header("Backend"),
		style.Header("OS"),
		style.Header("Arch"),
		style.Header("Match"),
		style.Header("Asset"),
	)
	for _, a := range catalog.Assets {
		selected := ""
		if a.Selected {
			selected = style.Selected("*")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			a.Backend,
			a.GOOS,
			a.GOARCH,
			selected,
			style.Asset(a.Name),
		)
	}
	w.Flush()
}

// runPin installs a developer-supplied libds4 by copying opts.Pin into
// opts.DestDir, regenerating the .sha256 sidecar, and writing pinned
// metadata. Pin is mutually exclusive with --version, --url, and --asset.
func runPin(ctx context.Context, opts Options) (*Result, error) {
	if opts.URL != "" || opts.Asset != "" || (opts.Version != "" && opts.Version != "latest") {
		return nil, fmt.Errorf("--pin is mutually exclusive with --url, --version, and --asset")
	}

	srcInfo, err := os.Stat(opts.Pin)
	if err != nil {
		return nil, fmt.Errorf("stat pin source: %w", err)
	}
	if !srcInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("pin source %s is not a regular file", opts.Pin)
	}

	absSrc, err := filepath.Abs(opts.Pin)
	if err != nil {
		return nil, fmt.Errorf("resolve pin source: %w", err)
	}

	libPath := filepath.Join(opts.DestDir, libraryFileName(opts.GOOS))
	res := &Result{
		Backend: opts.Backend,
		GOOS:    opts.GOOS,
		GOARCH:  opts.GOARCH,
		Library: libPath,
		DryRun:  opts.DryRun,
	}
	if opts.DryRun {
		fmt.Fprintf(opts.Out, "would pin %s -> %s\n", absSrc, libPath)
		return res, nil
	}

	overwrite, err := decidePinOverwrite(opts, libPath)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(opts.DestDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", opts.DestDir, err)
	}

	if err := copyFile(absSrc, libPath, overwrite); err != nil {
		return nil, err
	}
	if err := writeLibraryChecksum(libPath); err != nil {
		fmt.Fprintf(opts.Out, "warning: could not record library checksum: %v\n", err)
	}

	libSHA, err := fileSHA256(libPath)
	if err != nil {
		fmt.Fprintf(opts.Out, "warning: could not read installed library for metadata: %v\n", err)
	} else {
		meta := InstallMetadata{
			Kind:        KindPinned,
			Source:      absSrc,
			Backend:     opts.Backend,
			GOOS:        opts.GOOS,
			GOARCH:      opts.GOARCH,
			SHA256:      libSHA,
			InstalledAt: time.Now(),
		}
		metaBytes, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			fmt.Fprintf(opts.Out, "warning: could not marshal install metadata: %v\n", err)
		} else {
			if err := os.WriteFile(filepath.Join(opts.DestDir, MetadataFileName), metaBytes, 0o600); err != nil {
				fmt.Fprintf(opts.Out, "warning: could not write install metadata: %v\n", err)
			}
		}
	}

	fmt.Fprintf(opts.Out, "pinned %s -> %s\n", absSrc, libPath)
	return res, nil
}

// decidePinOverwrite returns true when the existing dest should be replaced.
// Behavior depends on what is currently at libPath; see the design doc's
// behavior matrix.
func decidePinOverwrite(opts Options, libPath string) (bool, error) {
	if _, err := os.Stat(libPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil // no existing file, no overwrite needed
		}
		return false, fmt.Errorf("stat dest: %w", err)
	}
	// File exists. Pin-over-pin requires --force regardless of TTY; other
	// existing states use TTY-prompt-or-`--force`, mirroring today's UX.
	meta, hasMeta, _ := readInstallMetadata(opts.DestDir)
	if hasMeta && meta.Kind == KindPinned {
		if opts.Force {
			return true, nil
		}
		return false, fmt.Errorf("libds4 at %s is pinned to %s; run 'ds4go uninstall' first or pass --force", libPath, meta.Source)
	}
	if opts.Force {
		return true, nil
	}
	if isTerminalFunc(opts.In) {
		var confirmed bool
		var err error
		if opts.Confirm != nil {
			confirmed, err = opts.Confirm(fmt.Sprintf("Replace %s with pinned file?", libPath), false)
		} else {
			confirmed, err = defaultConfirm(fmt.Sprintf("Replace %s with pinned file?", libPath), false, opts.In, opts.Out)
		}
		if err != nil {
			return false, fmt.Errorf("read prompt response: %w", err)
		}
		if !confirmed {
			return false, fmt.Errorf("install cancelled")
		}
		fmt.Fprintln(opts.Out)
		return true, nil
	}
	return false, fmt.Errorf("%s already exists; pass --force to replace it", libPath)
}

// copyFile copies src to dst with 0o600 permissions. If overwrite is true the
// existing dst is truncated; otherwise dst must not exist.
func copyFile(src, dst string, overwrite bool) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open pin source: %w", err)
	}
	defer in.Close()
	return writeFile(dst, in, overwrite)
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

// readInstallMetadata reads ds4go-install.json from destDir.
//
// Returns (zero, false, nil) when the file does not exist (no install
// recorded). Returns (zero, false, err) when the file exists but cannot be
// parsed. Returns (meta, true, nil) on success.
func readInstallMetadata(destDir string) (InstallMetadata, bool, error) {
	var meta InstallMetadata
	data, err := os.ReadFile(filepath.Join(destDir, MetadataFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return InstallMetadata{}, false, nil
		}
		return InstallMetadata{}, false, fmt.Errorf("read install metadata: %w", err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return InstallMetadata{}, false, fmt.Errorf("parse install metadata: %w", err)
	}
	return meta, true, nil
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
		if opts.Pin != "" {
			opts.Backend = "custom"
		} else {
			opts.Backend = defaultBackend(opts.GOOS, opts.GOARCH)
		}
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

func normalizeCatalog(opts Options) Options {
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	if opts.Version == "" {
		opts.Version = "latest"
	}
	opts.Backend = strings.ToLower(strings.TrimSpace(opts.Backend))
	opts.GOOS = strings.ToLower(strings.TrimSpace(opts.GOOS))
	opts.GOARCH = strings.ToLower(strings.TrimSpace(opts.GOARCH))
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
	if dir := os.Getenv("DS4_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".ds4")
	}
	return ".ds4"
}

func catalogAssetFromReleaseAsset(a asset) CatalogAsset {
	ca := CatalogAsset{
		Name:   a.Name,
		URL:    a.BrowserDownloadURL,
		Digest: a.Digest,
	}
	stem, archive, ok := catalogAssetStem(a.Name)
	if !ok {
		return ca
	}
	parts := strings.Split(stem, "-")
	if len(parts) < 4 {
		return ca
	}
	backend := strings.ToLower(parts[len(parts)-1])
	goarch := normalizeCatalogArch(parts[len(parts)-2])
	goos := normalizeCatalogOS(parts[len(parts)-3])
	if !knownCatalogBackend(backend) || goos == "" || goarch == "" {
		return ca
	}
	ca.GOOS = goos
	ca.GOARCH = goarch
	ca.Backend = backend
	ca.Archive = archive
	ca.Parsed = true
	return ca
}

func catalogAssetStem(name string) (stem, archive string, ok bool) {
	base := filepath.Base(name)
	switch {
	case strings.HasSuffix(base, ".tar.gz"):
		return strings.TrimSuffix(base, ".tar.gz"), "tar.gz", true
	case strings.HasSuffix(base, ".tgz"):
		return strings.TrimSuffix(base, ".tgz"), "tgz", true
	case strings.HasSuffix(base, ".zip"):
		return strings.TrimSuffix(base, ".zip"), "zip", true
	default:
		return "", "", false
	}
}

func normalizeCatalogOS(s string) string {
	switch strings.ToLower(s) {
	case "macos":
		return "darwin"
	case "darwin", "linux", "windows":
		return strings.ToLower(s)
	default:
		return ""
	}
}

func normalizeCatalogArch(s string) string {
	switch strings.ToLower(s) {
	case "x86_64":
		return "amd64"
	case "amd64", "arm64":
		return strings.ToLower(s)
	default:
		return ""
	}
}

func knownCatalogBackend(s string) bool {
	switch s {
	case "metal", "cuda", "rocm", "cpu":
		return true
	default:
		return false
	}
}

func catalogAssetMatches(a CatalogAsset, opts Options) bool {
	if opts.GOOS != "" && a.GOOS != normalizeCatalogOS(opts.GOOS) {
		return false
	}
	if opts.GOARCH != "" && a.GOARCH != normalizeCatalogArch(opts.GOARCH) {
		return false
	}
	if opts.Backend != "" && opts.Backend != "auto" && a.Backend != opts.Backend {
		return false
	}
	return true
}

func catalogAssetSelected(a CatalogAsset, opts Options) bool {
	if opts.GOOS == "" || opts.GOARCH == "" || opts.Backend == "" || opts.Backend == "auto" {
		return false
	}
	selectedOpts := opts
	selectedOpts.GOOS = a.GOOS
	selectedOpts.GOARCH = a.GOARCH
	selectedOpts.Backend = a.Backend
	for _, name := range candidateAssetNames(opts.Version, selectedOpts) {
		if a.Name == name {
			return true
		}
	}
	return false
}

// defaultModelsDir returns the directory of model files whose run-locks should
// be cross-referenced when listing libds4 holders. Models live under
// $DS4_DIR/models (or ~/.ds4/models) regardless of where libds4 itself is
// installed, so this intentionally does not depend on opts.DestDir / --lib.
// (Previously this was derived from --lib via filepath.Join(opts.DestDir,
// "..", "models"), but normalize() always populates DestDir, making the
// "no DestDir → default" fallback unreachable and giving the wrong directory
// for a custom --lib.)
func defaultModelsDir() string {
	return filepath.Join(defaultDir(), "models")
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
			return "rocm"
		}
		return "cpu"
	}
	return "cpu"
}

// Validate checks the installation of libds4 in opts.DestDir.
// It verifies existence, permissions, checksums, and dynamic loading.
// featureGateStatus renders an optional libds4 capability for `ds4go validate`.
// A missing capability is informational, not an error: older libds4 builds load
// fine but do not export the Dlsym-guarded symbol group.
func featureGateStatus(ok bool) string {
	if ok {
		return "✓ supported"
	}
	return "✗ not supported by this build"
}

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
	metaPath := filepath.Join(opts.DestDir, MetadataFileName)
	meta, hasMeta, metaErr := readInstallMetadata(opts.DestDir)
	switch {
	case metaErr != nil:
		fmt.Fprintf(opts.Out, "warning: install metadata file is corrupt: %v\n", metaErr)
	case !hasMeta:
		fmt.Fprintf(opts.Out, "warning: no install metadata file found at %s\n", metaPath)
	default:
		if !strings.EqualFold(localSHA, meta.SHA256) {
			return fmt.Errorf("checksum mismatch in install metadata %s: local is %s, metadata wants %s", metaPath, localSHA, meta.SHA256)
		}
		fmt.Fprintf(opts.Out, "✓ Install metadata matches local checksum\n")
	}

	// 4. Dynamic Loading verification
	if opts.GOOS == "darwin" && runtime.GOOS == "darwin" {
		fi, statErr := os.Stat(libPath)
		if statErr == nil && fi.Size() >= 10240 {
			sigStatus := checkSignatureStatus(libPath)
			if sigStatus == "unsigned" && runtime.GOARCH == "arm64" {
				return fmt.Errorf("refusing to load %s because it is unsigned. macOS on Apple Silicon requires binaries to be signed to load. Please sign it locally with: codesign -s - --force %s", libPath, libPath)
			}
			// We only refuse ad-hoc signed binaries if they are release builds (which are compiled
			// on foreign CI runners and will trigger a SIGKILL on load). Pinned (developer-supplied)
			// or unmanaged binaries are allowed.
			isRelease := hasMeta && meta.Kind != KindPinned
			if isRelease && (sigStatus == "invalid/untrusted" || sigStatus == "ad-hoc (foreign, untrusted)") {
				return fmt.Errorf("refusing to load %s because it is ad-hoc signed by a foreign build system and is untrusted by macOS. Loading this will cause the kernel to terminate the process with SIGKILL. Please sign it locally with: codesign -s - --force %s", libPath, libPath)
			}
		}
	}
	lib, err := loadLibraryFunc(libPath)
	if err != nil {
		return fmt.Errorf("failed to load dynamic library: %w", err)
	}
	fmt.Fprintf(opts.Out, "✓ Dynamically loaded library and registered symbols\n")

	// 4b. Print a short git-style fingerprint so users can eyeball the
	// installed library against a release's published checksum.
	fmt.Fprintf(opts.Out, "\nFingerprint: %s\n", localSHA[:8])
	if runtime.GOOS == "darwin" {
		fmt.Fprintf(opts.Out, "Signature:   %s\n", checkSignatureStatus(libPath))
	}

	// 4c. Report optional feature gates so users can see which capabilities the
	// loaded build exports. These are Dlsym-guarded symbol groups, so older
	// libraries load fine but report the capability as unsupported.
	fmt.Fprintln(opts.Out, "\n[Capabilities]")
	fmt.Fprintf(opts.Out, "  Dynamic steering:      %s\n", featureGateStatus(lib.SupportsDynamicSteering()))
	fmt.Fprintf(opts.Out, "  Distributed inference: %s\n", featureGateStatus(lib.SupportsDistributed()))

	// 5. Active process verification (excluding the current process)
	if holders, err := FindLibraryHolders(libPath); err == nil {
		var otherHolders []ProcessInfo
		myPID := os.Getpid()
		for _, p := range holders {
			if p.PID != myPID {
				otherHolders = append(otherHolders, p)
			}
		}
		if len(otherHolders) > 0 {
			modelHolders, _ := FindDirHolders(defaultModelsDir())

			fmt.Fprintf(opts.Out, "\nwarning: The following other active processes are holding onto the library:\n")
			for _, p := range otherHolders {
				status := "library loaded"
				if files, ok := modelHolders[p.PID]; ok && len(files) > 0 {
					var basenames []string
					for _, f := range files {
						basenames = append(basenames, filepath.Base(f))
					}
					status = fmt.Sprintf("running engine with %s", strings.Join(basenames, ", "))
				}
				fmt.Fprintf(opts.Out, "  - PID %d: %s (%s)\n", p.PID, p.Name, status)
			}
			fmt.Fprintln(opts.Out, "This may prevent updates or uninstallation.")
		}
	}

	// Print metadata info
	if hasMeta {
		fmt.Fprintln(opts.Out, "\n[Metadata]")
		switch meta.Kind {
		case KindPinned:
			fmt.Fprintf(opts.Out, "  Kind:        pinned (source: %s)\n", meta.Source)
		default:
			// KindRelease or legacy ""
			fmt.Fprintf(opts.Out, "  Kind:        release (%s %s)\n", meta.Repo, meta.Version)
		}
		fmt.Fprintf(opts.Out, "  Backend:     %s\n", meta.Backend)
		fmt.Fprintf(opts.Out, "  Installed:   %s\n", meta.InstalledAt.Format("2006-01-02 15:04:05"))
		if meta.Kind != KindPinned {
			fmt.Fprintf(opts.Out, "  Version:     %s\n", meta.Version)
			fmt.Fprintf(opts.Out, "  Source Repo: %s\n", meta.Repo)
		}
	}

	// Make the loaded library the default one for future calls in the CLI lifecycle
	ds4api.SetDefaultLibrary(lib)

	return nil
}

// uninstallPrompt returns the confirmation prompt to show the user when
// uninstalling libds4. Pinned installs get a stronger prompt naming the
// source path so the user knows what they're about to delete.
func uninstallPrompt(destDir string) string {
	if meta, ok, _ := readInstallMetadata(destDir); ok && meta.Kind == KindPinned {
		return fmt.Sprintf("This libds4 is pinned to %s. Really uninstall it from %s?", meta.Source, destDir)
	}
	return fmt.Sprintf("Uninstall libds4 and metadata files from %s?", destDir)
}

// Uninstall removes the installed libds4 shared library, sidecar, and metadata files from opts.DestDir.
func Uninstall(ctx context.Context, opts Options) error {
	opts = normalize(opts)

	libName := libraryFileName(opts.GOOS)
	libPath := filepath.Join(opts.DestDir, libName)
	sidecarPath := libPath + ".sha256"
	metaPath := filepath.Join(opts.DestDir, MetadataFileName)

	// 1. Check if any file exists
	libExists := false
	if _, err := os.Stat(libPath); err == nil {
		libExists = true
	}
	sidecarExists := false
	if _, err := os.Stat(sidecarPath); err == nil {
		sidecarExists = true
	}
	metaExists := false
	if _, err := os.Stat(metaPath); err == nil {
		metaExists = true
	}

	if !libExists && !sidecarExists && !metaExists {
		fmt.Fprintf(opts.Out, "libds4 is not installed in %s\n", opts.DestDir)
		return nil
	}

	// 2. Prompt for confirmation if not forced
	if !opts.Force {
		if isTerminalFunc(opts.In) {
			var confirmed bool
			var err error
			if opts.Confirm != nil {
				confirmed, err = opts.Confirm(uninstallPrompt(opts.DestDir), false)
			} else {
				confirmed, err = defaultConfirm(uninstallPrompt(opts.DestDir), false, opts.In, opts.Out)
			}
			if err != nil {
				return fmt.Errorf("read prompt response: %w", err)
			}
			if !confirmed {
				return fmt.Errorf("uninstall cancelled")
			}
			fmt.Fprintln(opts.Out)
		} else {
			return fmt.Errorf("libds4 files exist in %s; pass --force to uninstall them", opts.DestDir)
		}
	}

	// 3. Perform uninstallation (delete files). Continue on error so a failure
	// on one file doesn't leave the others behind; aggregate and report at end.
	var errs []error
	if libExists {
		if err := os.Remove(libPath); err != nil {
			errs = append(errs, fmt.Errorf("remove library: %w", err))
		}
	}
	if sidecarExists {
		if err := os.Remove(sidecarPath); err != nil {
			errs = append(errs, fmt.Errorf("remove sidecar: %w", err))
		}
	}
	if metaExists {
		if err := os.Remove(metaPath); err != nil {
			errs = append(errs, fmt.Errorf("remove metadata: %w", err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	fmt.Fprintf(opts.Out, "✓ Uninstalled libds4 and metadata files\n")
	return nil
}

// ProcessInfo represents a process holding or using a shared library.
type ProcessInfo struct {
	PID  int
	Name string
}

// FindLibraryHolders finds all processes holding onto the library path.
func FindLibraryHolders(libPath string) ([]ProcessInfo, error) {
	// First, check if the file exists. If it doesn't, there are no holders.
	if _, err := os.Stat(libPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Make sure it's an absolute path
	absPath, err := filepath.Abs(libPath)
	if err != nil {
		absPath = libPath
	}

	switch runtime.GOOS {
	case "darwin":
		return findHoldersLsof(absPath)
	case "linux":
		// Try proc maps first as it's pure-Go and fast.
		holders, err := findHoldersProc(absPath)
		if err == nil {
			return holders, nil
		}
		// Fall back to lsof if proc maps fails
		return findHoldersLsof(absPath)
	case "windows":
		return findHoldersWindows(absPath)
	default:
		// Attempt lsof as a best effort on other POSIX-like systems
		return findHoldersLsof(absPath)
	}
}

func findHoldersLsof(libPath string) ([]ProcessInfo, error) {
	cmd := exec.Command("lsof", "-F", "pfnc", libPath)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// lsof returns non-zero (often 1) if no files are open.
	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			return nil, fmt.Errorf("lsof not found: %w", err)
		}
	}

	procs, err := parseLsofOutput(stdout.Bytes())
	if err != nil {
		return nil, err
	}
	var results []ProcessInfo
	for _, p := range procs {
		results = append(results, ProcessInfo{PID: p.PID, Name: p.Name})
	}
	return results, nil
}

// LsofProcess represents a process and its open files parsed from lsof.
type LsofProcess struct {
	PID   int
	Name  string
	Files []string
}

func parseLsofOutput(data []byte) ([]LsofProcess, error) {
	var results []LsofProcess
	lines := strings.Split(string(data), "\n")
	type Proc struct {
		PID   int
		Name  string
		Files []string
	}
	var current Proc
	current.PID = -1

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		prefix := line[0]
		val := line[1:]

		switch prefix {
		case 'p':
			if current.PID != -1 {
				results = append(results, LsofProcess{PID: current.PID, Name: current.Name, Files: current.Files})
			}
			pid, err := strconv.Atoi(val)
			if err != nil {
				current.PID = -1
			} else {
				current.PID = pid
				current.Name = ""
				current.Files = nil
			}
		case 'c':
			if current.PID != -1 {
				current.Name = val
			}
		case 'n':
			if current.PID != -1 {
				current.Files = append(current.Files, val)
			}
		}
	}
	if current.PID != -1 {
		results = append(results, LsofProcess{PID: current.PID, Name: current.Name, Files: current.Files})
	}
	return results, nil
}

func findHoldersProc(libPath string) ([]ProcessInfo, error) {
	matches, err := filepath.Glob("/proc/[0-9]*/maps")
	if err != nil {
		return nil, err
	}

	var results []ProcessInfo
	for _, match := range matches {
		parts := strings.Split(match, "/")
		if len(parts) < 3 {
			continue
		}
		pidStr := parts[2]
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		content, err := os.ReadFile(match)
		if err != nil {
			continue
		}

		if strings.Contains(string(content), libPath) {
			name := getProcName(pidStr)
			results = append(results, ProcessInfo{PID: pid, Name: name})
		}
	}
	return results, nil
}

func getProcName(pidStr string) string {
	commPath := filepath.Join("/proc", pidStr, "comm")
	if data, err := os.ReadFile(commPath); err == nil {
		return strings.TrimSpace(string(data))
	}
	cmdlinePath := filepath.Join("/proc", pidStr, "cmdline")
	if data, err := os.ReadFile(cmdlinePath); err == nil {
		parts := strings.Split(string(data), "\x00")
		if len(parts) > 0 && parts[0] != "" {
			return filepath.Base(parts[0])
		}
	}
	return "unknown"
}

func findHoldersWindows(libPath string) ([]ProcessInfo, error) {
	filename := filepath.Base(libPath)
	cmd := exec.Command("tasklist", "/m", filename)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tasklist failed: %w", err)
	}

	return parseTasklistOutput(stdout.String(), filename)
}

func parseTasklistOutput(output, filename string) ([]ProcessInfo, error) {
	var results []ProcessInfo
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "=") || strings.HasPrefix(line, "Image Name") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			pidIdx := -1
			for i := 1; i < len(fields); i++ {
				if val, err := strconv.Atoi(fields[i]); err == nil {
					pid = val
					pidIdx = i
					break
				}
			}
			if pidIdx == -1 {
				continue
			}
			name := strings.Join(fields[:pidIdx], " ")
			results = append(results, ProcessInfo{PID: pid, Name: name})
			continue
		}

		results = append(results, ProcessInfo{PID: pid, Name: fields[0]})
	}
	return results, nil
}

// FindDirHolders finds processes holding any files under dirPath.
// It returns a map of PID to a list of file paths they hold under that directory.
func FindDirHolders(dirPath string) (map[int][]string, error) {
	if _, err := os.Stat(dirPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		absPath = dirPath
	}

	results := make(map[int][]string)

	// 1. Scan for native run lock files in dirPath
	if entries, err := os.ReadDir(absPath); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".run.lock") {
				lockPath := filepath.Join(absPath, entry.Name())
				pid, err := models.GetLockHolder(lockPath)
				if err == nil && pid > 0 {
					modelPath := strings.TrimSuffix(lockPath, ".run.lock")
					results[pid] = append(results[pid], modelPath)
				}
			}
		}
	}

	// 2. Query other processes holding files under dirPath using platform-specific fallback (lsof / /proc)
	var fallback map[int][]string
	switch runtime.GOOS {
	case "darwin":
		fallback, _ = findDirHoldersLsof(absPath)
	case "linux":
		fallback, err = findDirHoldersProc(absPath)
		if err != nil {
			fallback, _ = findDirHoldersLsof(absPath)
		}
	default:
		fallback, _ = findDirHoldersLsof(absPath)
	}

	// Merge fallback results into results
	for pid, files := range fallback {
		for _, f := range files {
			if strings.HasSuffix(f, ".run.lock") {
				continue
			}
			results[pid] = append(results[pid], f)
		}
	}

	// Deduplicate and sort lists for each PID
	for pid, files := range results {
		slices.Sort(files)
		results[pid] = slices.Compact(files)
	}

	return results, nil
}

func findDirHoldersLsof(dirPath string) (map[int][]string, error) {
	cmd := exec.Command("lsof", "+D", dirPath, "-F", "pfnc")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// lsof returns 1 if no files are open under the directory.
	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			return nil, fmt.Errorf("lsof not found: %w", err)
		}
	}

	procs, err := parseLsofOutput(stdout.Bytes())
	if err != nil {
		return nil, err
	}

	results := make(map[int][]string)
	for _, p := range procs {
		var filteredFiles []string
		for _, f := range p.Files {
			if strings.HasPrefix(f, dirPath) {
				filteredFiles = append(filteredFiles, f)
			}
		}
		if len(filteredFiles) > 0 {
			slices.Sort(filteredFiles)
			results[p.PID] = slices.Compact(filteredFiles)
		}
	}
	return results, nil
}

func findDirHoldersProc(dirPath string) (map[int][]string, error) {
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		absDir = dirPath
	}

	matches, err := filepath.Glob("/proc/[0-9]*/maps")
	if err != nil {
		return nil, err
	}

	results := make(map[int][]string)
	for _, match := range matches {
		parts := strings.Split(match, "/")
		if len(parts) < 3 {
			continue
		}
		pidStr := parts[2]
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}

		// Read maps
		if content, err := os.ReadFile(match); err == nil {
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				fields := strings.Fields(line)
				if len(fields) < 6 {
					continue
				}
				path := fields[5]
				if strings.HasPrefix(path, absDir) {
					results[pid] = append(results[pid], path)
				}
			}
		}

		// Read fd directory
		fds, err := filepath.Glob(fmt.Sprintf("/proc/%s/fd/*", pidStr))
		if err == nil {
			for _, fd := range fds {
				if target, err := os.Readlink(fd); err == nil {
					if strings.HasPrefix(target, absDir) {
						results[pid] = append(results[pid], target)
					}
				}
			}
		}
	}

	// Deduplicate lists
	for pid, list := range results {
		slices.Sort(list)
		results[pid] = slices.Compact(list)
	}

	return results, nil
}

// Status finds and displays processes holding the library.
func Status(ctx context.Context, opts Options) error {
	opts = normalize(opts)
	libName := libraryFileName(opts.GOOS)
	libPath := filepath.Join(opts.DestDir, libName)

	libHolders, err := FindLibraryHolders(libPath)
	if err != nil {
		return fmt.Errorf("failed to query library holders: %w", err)
	}

	modelHolders, _ := FindDirHolders(defaultModelsDir())

	if len(libHolders) == 0 {
		fmt.Fprintf(opts.Out, "No active processes are holding onto the library at: %s\n", libPath)
		return nil
	}

	fmt.Fprintf(opts.Out, "Processes holding onto %s:\n\n", libPath)
	fmt.Fprintf(opts.Out, "%-10s %-20s %s\n", "PID", "PROCESS NAME", "HOLDING ENGINE (MODEL)")
	fmt.Fprintf(opts.Out, "%-10s %-20s %s\n", "---", "------------", "----------------------")
	for _, p := range libHolders {
		status := "No"
		if files, ok := modelHolders[p.PID]; ok && len(files) > 0 {
			var basenames []string
			for _, f := range files {
				basenames = append(basenames, filepath.Base(f))
			}
			status = fmt.Sprintf("Yes (%s)", strings.Join(basenames, ", "))
		}
		fmt.Fprintf(opts.Out, "%-10d %-20s %s\n", p.PID, p.Name, status)
	}
	return nil
}

func isForeignBinary(path string) bool {
	cmd := exec.Command("xattr", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	outStr := string(out)
	return strings.Contains(outStr, "com.apple.provenance") || strings.Contains(outStr, "com.apple.quarantine")
}

func checkSignatureStatus(path string) string {
	if runtime.GOOS != "darwin" {
		return "not applicable (non-macOS)"
	}
	codesignPath := "codesign"
	if _, err := exec.LookPath(codesignPath); err != nil {
		if _, statErr := os.Stat("/usr/bin/codesign"); statErr == nil {
			codesignPath = "/usr/bin/codesign"
		} else {
			return "not verified (codesign tool not found)"
		}
	}

	cmd := exec.Command(codesignPath, "-d", "-vv", path)
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if err != nil {
		if strings.Contains(outStr, "code object is not signed at all") {
			return "unsigned"
		}
		return "invalid/untrusted"
	}
	if strings.Contains(outStr, "Signature=adhoc") {
		if isForeignBinary(path) {
			return "ad-hoc (foreign, untrusted)"
		}
		return "ad-hoc (signed locally)"
	}
	for _, line := range strings.Split(outStr, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Authority=") {
			auth := strings.TrimPrefix(line, "Authority=")
			return fmt.Sprintf("verified (%s)", auth)
		}
	}
	return "verified (official Apple Developer ID)"
}

func defaultConfirm(prompt string, defaultYes bool, in io.Reader, out io.Writer) (bool, error) {
	if defaultYes {
		fmt.Fprintf(out, "%s [Y/n]: ", prompt)
	} else {
		fmt.Fprintf(out, "%s [y/N]: ", prompt)
	}
	var input string
	if _, err := fmt.Fscanln(in, &input); err != nil {
		if err.Error() == "unexpected newline" {
			return defaultYes, nil
		}
		return false, err
	}
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "y" || input == "yes" {
		return true, nil
	}
	if input == "n" || input == "no" {
		return false, nil
	}
	return defaultYes, nil
}
