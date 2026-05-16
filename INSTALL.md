# Installation

## 1. Build libds4

Clone and build ds4 from upstream:

```sh
git clone https://github.com/antirez/ds4
cd ds4
# Follow ds4's current build instructions for Metal, CUDA, or CPU.
```

`ds4-go` expects one of these runtime artifacts:

```text
libds4.dylib  macOS
libds4.so     Linux
libds4.dll    Windows
```

This repository does not compile C, Objective-C, Metal, or CUDA code.

## 2. Put the Library Where Go Can Find It

Any of these work:

```sh
mkdir -p lib
cp /path/to/libds4.dylib ./lib/
```

```sh
export DS4_LIB=/path/to/libds4.dylib
```

```sh
export DS4GO_LIB=/path/to/libds4.so
```

The package searches `DS4_LIB`, `DS4GO_LIB`, the executable directory, `./lib`, and the platform library name.

## 3. Build Go Code

```sh
go build ./...
```

No C compiler is needed for Go builds.
