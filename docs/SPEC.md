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

The root `ds4` loader checks:

1. `DS4_LIB`
2. `$DS4_DIR/lib` when `DS4_DIR` is set, otherwise `~/.ds4/lib`
3. executable directory
4. executable `lib/`
5. current directory
6. current `lib/`
7. platform loader path for `libds4.*`

The low-level `ds4api.Load("")` only applies raw shared-library loading policy:
`DS4_LIB`, local executable/current-directory paths, and the platform loader path.

## Memory Ownership

`Engine`, `Session`, and owned `Tokens` install finalizers but should still be closed or freed explicitly. Token text returned by `ds4_token_text` is copied into Go memory and released with the platform C runtime `free`.

Session snapshots are copied into Go memory and the ds4 snapshot is freed immediately.

## Callback Ownership

Go callbacks are registered in package-level maps and passed to ds4 as integer user data. One-shot generation callbacks are removed when the call returns. Session progress callbacks remain registered until replaced or until the session closes.
