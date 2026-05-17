# ds4-go Binding Spec

## Goals

`ds4-go` exposes the public `ds4.h` API through pure Go:

- no cgo,
- no compiler required during `go build`,
- runtime loading of `libds4`,
- idiomatic Go ownership for engines, sessions, token vectors, callbacks, snapshots, and errors.

## Loader Policy

The default loader checks:

1. `DS4_LIB`
2. `$DS4_DIR/lib` when `DS4_DIR` is set, otherwise `~/.ds4/lib`
3. executable directory
4. executable `lib/`
5. current directory
6. current `lib/`
7. platform loader path for `libds4.*`

## Memory Ownership

`Engine`, `Session`, and owned `Tokens` install finalizers but should still be closed or freed explicitly. Token text returned by `ds4_token_text` is copied into Go memory and released with the platform C runtime `free`.

Session snapshots are copied into Go memory and the ds4 snapshot is freed immediately.

## Callback Ownership

Go callbacks are registered in package-level maps and passed to ds4 as integer user data. One-shot generation callbacks are removed when the call returns. Session progress callbacks remain registered until replaced or until the session closes.
