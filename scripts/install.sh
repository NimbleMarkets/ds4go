#!/bin/sh
# ds4go quick installer.
# Usage:  curl -fsSL https://nimblemarkets.github.io/ds4go/install.sh | sh
# Env:    DS4GO_VERSION=latest|v0.3.0|0.3.0   (default: latest)
#         INSTALL_DIR=/usr/local/bin           (default: /usr/local/bin)
#         DS4GO_FORCE=1                        (override brew-managed refusal)

set -u

REPO="NimbleMarkets/ds4go"
DS4GO_VERSION="${DS4GO_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
DS4GO_FORCE="${DS4GO_FORCE:-}"

die() { echo "error: $*" >&2; exit 1; }
info() { echo "$*"; }

# Picks the first working HTTP downloader: curl, then wget.
pick_downloader() {
  if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
  elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
  else
    die "need curl or wget on PATH"
  fi
}

# fetch URL OUTFILE
fetch() {
  if [ "$DOWNLOADER" = "curl" ]; then
    curl -fsSL "$1" -o "$2" || die "failed to download $1"
  else
    wget -q -O "$2" "$1" || die "failed to download $1"
  fi
}

# Picks the SHA256 tool available on this system.
pick_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    SHA256="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    SHA256="shasum -a 256"
  else
    die "need sha256sum or shasum -a 256 on PATH (cannot verify download)"
  fi
}

# Computes the SHA256 of FILE and prints just the digest.
sha256_of() {
  $SHA256 "$1" | awk '{print $1}'
}

make_workdir() {
  WORKDIR=$(mktemp -d 2>/dev/null || mktemp -d -t ds4go-install)
  [ -n "$WORKDIR" ] && [ -d "$WORKDIR" ] || die "could not create workdir"
  # shellcheck disable=SC2064  # WORKDIR is fixed at trap-set time, intentional
  trap "rm -rf '$WORKDIR'" EXIT INT TERM
}

# ---- platform detection ------------------------------------------------------

detect_platform() {
  os=$(uname -s)
  arch=$(uname -m)
  case "$os" in
    Linux)  os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS: $os (this script targets Linux and macOS; on Windows use Homebrew or download a .zip from $REPO/releases)" ;;
  esac
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported architecture: $arch (supported: amd64, arm64)" ;;
  esac
  PLATFORM="${os}_${arch}"
}

# ---- version validation ------------------------------------------------------

validate_version() {
  case "$DS4GO_VERSION" in
    latest) return 0 ;;
    v[0-9]*.[0-9]*.[0-9]*|[0-9]*.[0-9]*.[0-9]*) return 0 ;;
    *) die "invalid DS4GO_VERSION: '$DS4GO_VERSION' (expected 'latest' or vX.Y.Z)" ;;
  esac
}

# ---- version resolution ------------------------------------------------------

# Reads the GitHub Releases API for the latest tag, with a redirect-based
# fallback when the unauthenticated API is rate-limited.
resolve_version() {
  case "$DS4GO_VERSION" in
    latest) ;;
    v*) return 0 ;;
    *) DS4GO_VERSION="v$DS4GO_VERSION"; return 0 ;;
  esac

  command -v curl >/dev/null 2>&1 || \
    die "resolving 'latest' requires curl on PATH; install curl or set DS4GO_VERSION=vX.Y.Z explicitly"

  api_url="https://api.github.com/repos/$REPO/releases/latest"
  tag=$(curl -fsSL "$api_url" 2>/dev/null | \
        sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)

  if [ -z "$tag" ]; then
    # Fallback: GET .../releases/latest returns a 302 to /releases/tag/vX.Y.Z
    redirect_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
                   "https://github.com/$REPO/releases/latest" 2>/dev/null)
    tag=$(echo "$redirect_url" | sed -n 's|.*/tag/\(v[^/]*\)$|\1|p')
  fi

  [ -n "$tag" ] || die "could not resolve latest release tag (API rate-limited? set DS4GO_VERSION=vX.Y.Z explicitly)"
  DS4GO_VERSION="$tag"
}

# ---- download & verify -------------------------------------------------------

download_and_verify() {
  archive="ds4go_${DS4GO_VERSION#v}_${PLATFORM}.tar.gz"
  base="https://github.com/$REPO/releases/download/$DS4GO_VERSION"

  info "downloading $archive ..."
  fetch "$base/$archive"        "$WORKDIR/$archive"
  fetch "$base/checksums.txt"   "$WORKDIR/checksums.txt"

  info "verifying SHA256 ..."
  expected=$(grep " $archive\$" "$WORKDIR/checksums.txt" | awk '{print $1}')
  [ -n "$expected" ] || die "no checksum line for $archive in checksums.txt"
  actual=$(sha256_of "$WORKDIR/$archive")
  if [ "$expected" != "$actual" ]; then
    die "checksum mismatch for $archive
  expected: $expected
  actual:   $actual"
  fi

  info "extracting ..."
  ( cd "$WORKDIR" && tar -xzf "$archive" ) || die "tar extraction failed"
  [ -x "$WORKDIR/ds4go" ] || die "extracted archive did not contain a ds4go binary"
}

# ---- main --------------------------------------------------------------------

main() {
  validate_version
  pick_downloader
  pick_sha256
  resolve_version
  detect_platform

  info "ds4go installer"
  info "  version:  $DS4GO_VERSION"
  info "  platform: $PLATFORM"
  info "  target:   $INSTALL_DIR/ds4go"

  make_workdir
  download_and_verify

  info "(download verified; install step not yet implemented)"
  info "extracted binary at $WORKDIR/ds4go"
}

main "$@"
