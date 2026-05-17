# `ds4go` AGENTS.md

This is the first prompt.  However this is the AGENTS.md file and for the initial phases you may edit it however you wish and do not need to keep the initial prompt as is.

We will be using superpowers to build this.  We will include our plans and specs in the repo.  Please ensure that the PII is limited to the developers github identifiers.

You are an expert Go systems engineer. Create a **complete, production-ready pure-Go library** called `ds4go` that lets Go applications run the **ds4** inference engine (https://github.com/antirez/ds4) using **purego + FFI**, exactly like the Yzma project does for llama.cpp (https://github.com/hybridgroup/yzma).

There is a go.mod with `github.com/NimbleMarkets/ds4go`.  Our project structure will resemble other NimbleMarkets Golang tools, such as:
 * https://github.com/NimbleMarkets/ntcharts
 * https://github.com/NimbleMarkets/go-booba

**Key requirements (mirror Yzma’s philosophy exactly):**
- Zero CGo
- Pure `go build` (no external compiler at build time)
- Dynamically load a pre-built shared library (`libds4.so`, `libds4.dylib`, or `libds4.dll`) at runtime via `purego`
- Hardware acceleration is handled entirely by the shared library the user provides (Metal, CUDA, CPU — user chooses the right build)
- Thin, idiomatic Go wrapper around the exact C API defined in `ds4.h`
- Keep `ds4api/` purely focused on the ds4 wrapper: FFI loading, 1:1 bindings,
  thin safe wrappers, and direct helpers that map to `ds4.h`. Do not put
  CLI UX, diagnostics enrichment, model management, download/install flows, or
  product-facing convenience behavior in `ds4api/`.
- Put higher-level runtime helpers in the module root package, named `ds4`.
  This package is where reusable behavior built on top of `ds4api/` belongs, such as
  default path policy, friendlier error diagnostics, process/lock context,
  profile/session helpers, generation loops, and other non-binding extensions.
- Same folder layout and patterns as Yzma:
  - `go.mod`
  - `ds4api/` → all the Go bindings (imported as `github.com/NimbleMarkets/ds4go/ds4api`)
  - root package `ds4` → Go-native runtime conveniences built on top of `ds4api/`
  - `examples/` → several ready-to-run examples
  - `cmd/ds4go/` → optional CLI (simple one-shot and chat)
  - `lib/` → placeholder for the shared libraries (document how user places them)
  - `README.md`, `INSTALL.md`, `MODELS.md`, `BENCHMARKS.md`
- Support environment variable `DS4_LIB` to point to a custom library path and
  `DS4_DIR` for the ds4 home directory
- Auto-detect platform at runtime and pick the correct extension (.so / .dylib / .dll)

**The user will handle building the shared library from ds4** — you do **not** need to generate any C/Metal/CUDA code or Makefiles. Just assume the following files exist after the user runs their build:
- `libds4.so` (Linux)
- `libds4.dylib` (macOS)
- `libds4.dll` (Windows)

**Use the exact public API from ds4.h** (I will paste the full header below for reference). Create Go types and functions that map 1:1 to the C API with clean, safe Go idioms:
- `Engine` (wraps `ds4_engine`)
- `Session` (wraps `ds4_session`)
- `EngineOptions` (wraps `ds4_engine_options`)
- `Tokens` (wraps `ds4_tokens` with nice methods)
- Proper error handling (return `error` instead of char* err buffers where possible)
- Callbacks converted to Go func types (`TokenEmitFunc`, `GenerationDoneFunc`, `ProgressFunc`)
- All enums as Go consts or iota types
- Safe memory management (no leaks, proper freeing)

**Include in the generated project:**
1. Full `ds4api` package with every public function from `ds4.h` bound via purego
2. High-level convenience wrappers in the root `ds4` package:
   - `NewEngine(opts EngineOptions) (*Engine, error)`
   - `Load(path string) (*Library, error)`
   - `Generator.Generate(...)` helpers (streaming + non-streaming)
   - Chat helpers that mirror `ds4_encode_chat_prompt`, `ds4_chat_append_*`, etc.
3. Full examples:
   - `examples/simple` – load model and generate one response
   - `examples/chat` – interactive REPL
   - `examples/openai-compatible` – tiny server using the same engine (optional but nice)
4. Comprehensive README.md with:
   - Installation steps
   - How to build the shared lib from ds4 (brief)
   - How to place `libds4.*` files
   - Usage examples
   - Performance notes (in-process = zero overhead)
5. go.mod with proper module name (`github.com/NimbleMarkets/ds4go`
6. Makefile with `make` targets for building examples and cleaning

**Signal safety (critical):**
- Never wire `signal.NotifyContext` around C FFI calls. `SIGINT` can land on C worker threads (Metal/CUDA) and crash the process.
- Programmatic `context.Context` cancellation is safe because it only affects Go-side loop control between tokens. Pass `Context` via `GenerateOptions.Context`.
- The `examples/openai-compatible` server wires `r.Context()` for safe client-disconnect cancellation.

**Important Go style:**
- Use `github.com/ebitengine/purego` for loading
- Use `unsafe` only where absolutely necessary and document it
- All public APIs must be safe and idiomatic
- Add godoc comments on every exported type/function
- Handle the special ds4 features: thinking modes, directional steering, MTP, disk KV cache (via the engine options), DSML tool calling, etc.
 
**Full ds4.h for reference** (use this exact API): can be extracted from  https://github.com/antirez/ds4/blob/main/ds4.h


Now generate the **entire project** as a set of files with clear markdown code blocks (e.g. ```go:disable-run


Start generating!
