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
- `cmd/ds4go/` and `cmd/internal/` (CLI/TUI components: `cli`, `tui`) live in the nested `cmd` module.
- `internal/` (core library-internal packages: `install`, `models`, `cliopts`) live in the root module.
- `examples/` and root packages may use the library packages where appropriate.

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
- **macOS Code Signing**: macOS on Apple Silicon (arm64) requires all binaries to be signed. Foreign ad-hoc signed libraries (built on remote CI runners) will trigger a kernel `SIGKILL` on load. The validator and installer must verify code signature status and refuse loading invalid or foreign ad-hoc signed libraries, directing users to sign locally via `codesign -s - --force <libPath>`.
- **Stderr Logging Redirection**: Logging is redirected via process-global file descriptors using `SetStderr`, `SetStderrFd`, `DiscardLogs`, and `CaptureStderr`. Do not use or reintroduce callback-based logging (`SetLogFunc`). Redirection is not supported on Windows.
- **Backend Detection**: Use `DetectDefaultBackend(libPath)` to query preferred backends from the `ds4go-install.json` metadata sidecar file or fall back to system capability checks (e.g. checking `/dev/nvidiactl` or `nvidia-smi` on Linux).
- Before committing or handing off substantial code changes, run:

```sh
go test ./... ./cmd/...
go vet ./... ./cmd/...
```

Use `go test ./... ./cmd/... -race` for changes that touch shared state, callbacks,
streaming, sessions, or prompt/tool orchestration.

