# ds4go Binding Spec

## Goals

`ds4go` exposes ds4 through two Go packages:

- `github.com/NimbleMarkets/ds4go`, package `ds4`, is the Go-native runtime layer.
- `github.com/NimbleMarkets/ds4go/ds4api`, package `ds4api`, is the strict binding layer over `ds4.h`.

Both packages are pure Go:

- no cgo,
- no compiler required during `go build`,
- runtime loading of `libds4`,
- idiomatic Go ownership for engines, sessions, token vectors, callbacks, snapshots, and errors.

## Loader Policy

The root `ds4.DefaultLibraryPath` loader policy checks:

1. `DS4_LIB`
2. `$DS4_DIR/lib/libds4.*` when `DS4_DIR` is set, otherwise `~/.ds4/lib/libds4.*`
3. `libds4.*` in the executable directory
4. `libds4.*` in a `lib/` directory next to the executable

When none exists the result is "not found" rather than the bare platform
name: dyld and Windows `LoadLibrary` search the working directory for a leaf
name, which is the planting vector the search order exists to avoid.

The low-level `ds4api.Load("")` policy does not use `DS4_DIR`. It checks:

1. `DS4_LIB`
2. `libds4.*` in the executable directory
3. `libds4.*` in a `lib/` directory next to the executable

and likewise returns "not found" rather than a bare name; `ds4api.Load`
refuses a bare name outright.

The current working directory and repository root are intentionally not searched
to avoid loading an attacker-planted shared library.

## Memory Ownership

`Engine`, `Session`, and owned `Tokens` install finalizers but should still be closed or freed explicitly. Token text returned by `ds4_token_text` is copied into Go memory and released with the platform C runtime `free`.

Session snapshots are copied into Go memory and the ds4 snapshot is freed immediately.

## Callback Ownership

Go callbacks are registered in package-level maps and passed to ds4 as integer user data. One-shot generation callbacks are removed when the call returns. Session progress callbacks remain registered until replaced or until the session closes.
