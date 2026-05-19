# ds4go Agent Notes

`ds4go` is a pure-Go wrapper around the ds4 inference engine. It uses
`github.com/ebitengine/purego` to load a user-supplied `libds4` shared library at
runtime, so normal Go builds must remain zero-CGO and must not require a C
compiler.

## Package Boundaries

- `ds4api/` is the strict binding layer over `ds4.h`: FFI loading, 1:1 symbol
  bindings, thin safe wrappers, callback plumbing, and direct helpers that map
  to the C API.
- The module root package, `ds4`, owns Go-native runtime policy and reusable
  conveniences: default paths, friendly diagnostics, generation helpers,
  prompt/session/tool helpers, model management integration, and CLI-facing
  behavior.
- `dsml/` is pure text processing for DeepSeek DSML tool-calling markup. It must
  stay independent of FFI and engine state.
- `cmd/ds4go/`, `examples/`, and `internal/` may use the higher-level root
  package where appropriate.

## Runtime Paths

Shared libraries are not stored in this repository. The supported locations are:

- `DS4_LIB` for an explicit shared-library file.
- `$DS4_DIR/lib`, defaulting to `~/.ds4/lib`, for installed `libds4` builds.
- The executable directory or a `lib/` directory next to the executable.
- The platform loader path for bare `libds4.*` names.

The current working directory and repository root are intentionally not searched
to avoid binary planting.

## Development Rules

- Keep public APIs backward compatible unless a task explicitly says otherwise.
- Keep `unsafe` isolated to binding code and document why it is needed.
- Add godoc comments for exported Go APIs.
- Preserve signal safety: do not wire `signal.NotifyContext` around FFI calls.
  Programmatic `context.Context` cancellation is safe only at Go-side control
  points between tokens.
- Prefer focused tests near the package being changed. For cross-package prompt,
  generation, or tool behavior, add root package tests.
- Before committing or handing off substantial code changes, run:

```sh
go test ./...
go vet ./...
```

Use `go test ./... -race` for changes that touch shared state, callbacks,
streaming, sessions, or prompt/tool orchestration.
