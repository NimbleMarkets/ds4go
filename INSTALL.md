# Installation


## Build Go Code

```sh
# Taskfile
task build

# Go commands
go mod tidy
go build ./...
```

No C compiler is needed for Go builds.  Runtime requires `ds4`, read on...

## Install the `ds4go` CLI

Install the prebuilt `ds4go` CLI with Homebrew, or build it with the Go
toolchain:

```sh
# Homebrew (macOS/Linux)
brew install nimblemarkets/tap/ds4go

# or with the Go toolchain
go install github.com/NimbleMarkets/ds4go/cmd/ds4go@latest
```

Homebrew releases are published from the [NimbleMarkets tap][tap] by
[GoReleaser](https://goreleaser.com) on each tagged release.

[tap]: https://github.com/NimbleMarkets/homebrew-tap

## Quick Install of `ds4` from GitHub Releases

`ds4go` can install prebuilt `libds4` shared libraries published by the
NimbleMarkets ds4 fork:

```sh
ds4go install --backend auto
```

By default this installs the shared library into:

```text
~/.ds4/lib/libds4.dylib  macOS
~/.ds4/lib/libds4.so     Linux
~/.ds4/lib/libds4.dll    Windows
```

Set `DS4_DIR` to use a different ds4 home:

```sh
export DS4_DIR=/opt/ds4
ds4go install --backend auto
```

The tooling treats this directory as:

```text
$DS4_DIR/lib/      native shared libraries
$DS4_DIR/models/   GGUF model files
```

`--backend auto` selects `metal` on macOS arm64 and `cpu` elsewhere. Select a
specific build when needed:

```sh
ds4go install --backend metal
ds4go install --backend cuda
ds4go install --backend cpu
```

The installer downloads from `github.com/NimbleMarkets/ds4` by default. You can
point it at another fork or a specific release:

```sh
ds4go install --repo neomantra/ds4 --version v0.1.0 --backend metal
ds4go install --url https://github.com/NimbleMarkets/ds4/releases/download/v0.1.0/libds4-v0.1.0-darwin-arm64-metal.tar.gz
ds4go install --lib ./lib --backend metal
```

Expected release asset names are:

```text
libds4-VERSION-macos-arm64-metal.tar.gz
libds4-VERSION-linux-x86_64-cpu.tar.gz
libds4-VERSION-linux-x86_64-cuda.tar.gz
libds4-VERSION-windows-amd64-cpu.zip
libds4-VERSION-windows-amd64-cuda.zip
```

The installer also accepts Go-style platform aliases such as `darwin` for
`macos` and `amd64` for `x86_64`.

If a release contains `checksums.txt` or `SHA256SUMS`, the installer verifies
the downloaded archive before extraction. Use `--skip-checksum` only for local
testing or trusted private artifacts.

## Manual Install from `ds4` repository

### 1. Build libds4

Clone and build ds4 from upstream:

```sh
git clone https://github.com/antirez/ds4
cd ds4
# Follow ds4's current build instructions for Metal, CUDA, or CPU.
```

`ds4go` expects one of these runtime artifacts:

```text
libds4.dylib  macOS
libds4.so     Linux
libds4.dll    Windows
```

This repository does not compile C, Objective-C, Metal, or CUDA code.

### 2. Put the Library Where Go Can Find It

Any of these work:

```sh
mkdir -p ~/.ds4/lib ~/.ds4/models
cp /path/to/libds4.dylib ~/.ds4/lib/
```

```sh
export DS4_LIB=/path/to/libds4.dylib
```

```sh
export DS4_DIR=/opt/ds4
mkdir -p "$DS4_DIR/lib" "$DS4_DIR/models"
cp /path/to/libds4.so "$DS4_DIR/lib/"
```

The package searches `DS4_LIB`, `$DS4_DIR/lib` when `DS4_DIR` is set, otherwise
`~/.ds4/lib`, then the executable directory, `./lib`, and the platform library
name.
