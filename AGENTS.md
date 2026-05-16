# `ds4-go` AGENTS.md

This is the first prompt.  However this is the AGENTS.md file and for the initial phases you may edit it however you wish and do not need to keep the initial prompt as is.

We will be using superpowers to build this.  We will include our plans and specs in the repo.  Please ensure that the PII is limited to the developers github identifiers.

You are an expert Go systems engineer. Create a **complete, production-ready pure-Go library** called `ds4-go` that lets Go applications run the **ds4** inference engine (https://github.com/antirez/ds4) using **purego + FFI**, exactly like the Yzma project does for llama.cpp (https://github.com/hybridgroup/yzma).

There is a go.mod with `github.com/NimbleMarkets/ds4-go`.  Our project structure will resemble other NimbleMarkets Golang tools, such as:
 * https://github.com/NimbleMarkets/ntcharts
 * https://github.com/NimbleMarkets/go-booba

**Key requirements (mirror Yzma’s philosophy exactly):**
- Zero CGo
- Pure `go build` (no external compiler at build time)
- Dynamically load a pre-built shared library (`libds4.so`, `libds4.dylib`, or `libds4.dll`) at runtime via `purego`
- Hardware acceleration is handled entirely by the shared library the user provides (Metal, CUDA, CPU — user chooses the right build)
- Thin, idiomatic Go wrapper around the exact C API defined in `ds4.h`
- Same folder layout and patterns as Yzma:
  - `go.mod`
  - `ds4/` → all the Go bindings (imported as `github.com/NimbleMarkets/ds4-go/ds4`)
  - `examples/` → several ready-to-run examples
  - `cmd/ds4-go/` → optional CLI (simple one-shot and chat)
  - `lib/` → placeholder for the shared libraries (document how user places them)
  - `README.md`, `INSTALL.md`, `MODELS.md`, `BENCHMARKS.md`
- Support environment variable `DS4_LIB` (or `DS4GO_LIB`) to point to a custom library path
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
1. Full `ds4` package with every public function from `ds4.h` bound via purego
2. High-level convenience wrappers:
   - `NewEngine(opts EngineOptions) (*Engine, error)`
   - `Engine.NewSession(ctxSize int) (*Session, error)`
   - `Session.Sync(prompt []int, ...)`
   - `Session.Generate(...)` helpers (streaming + non-streaming)
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
5. go.mod with proper module name (`github.com/NimbleMarkets/ds4-go`
6. Makefile with `make` targets for building examples and cleaning

**Important Go style:**
- Use `github.com/ebitengine/purego` for loading
- Use `unsafe` only where absolutely necessary and document it
- All public APIs must be safe and idiomatic
- Add godoc comments on every exported type/function
- Handle the special ds4 features: thinking modes, directional steering, MTP, disk KV cache (via the engine options), DSML tool calling, etc.
 
**Full ds4.h for reference** (use this exact API): can be extracted from  https://github.com/antirez/ds4/blob/main/ds4.h


Now generate the **entire project** as a set of files with clear markdown code blocks (e.g. ```go:disable-run


Start generating!
