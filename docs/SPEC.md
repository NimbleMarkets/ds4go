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

`Engine`, `Session`, and owned `Tokens` register runtime cleanup handlers but should still be closed or freed explicitly. Token text returned by `ds4_token_text` is copied into Go memory and released with the platform C runtime `free`.

Session snapshots are copied into Go memory and the ds4 snapshot is freed immediately.

## Callback Ownership

Go callbacks are registered in package-level maps and passed to ds4 as integer user data. One-shot generation callbacks are removed when the call returns. Session progress callbacks remain registered until replaced or until the session closes.

## HTTP and CLI Boundaries

The nested `cmd` module owns the CLI. `internal/openaiserver` owns the shared
HTTP implementation used by `ds4go serve` and `examples/openai-compatible`.
Protocol conversion, sampling policy, replay, and scheduling live above the
strict bindings. See the [server guide](SERVER.md) for supported behavior.

## Batch Decoding and ABI Validation

`ds4api.Engine.EvalBatch` accepts distinct live sessions from the same engine
and int32-representable tokens. It uses the optional `ds4_sessions_eval_batch`
symbol, falling back to sequential evaluation when absent. Calls retain Go
owners and serialize access to libds4. An error can leave some sessions
advanced; callers must discard or re-sync affected sessions before reuse.

The immutable upstream revision is recorded in `.github/ds4-revision`.
`task ds4:sync` compiles the actual C header and compares layouts with the Go
binding snapshot; Go-only layout tests cannot detect a stale snapshot. CI and
release checks validate the pinned header, test and vet both Go modules, run
race tests, and build without CGO. A C compiler is needed for the header probe,
not for normal Go builds.
