# ds4go

<p>
    <a href="https://github.com/NimbleMarkets/ds4go/tags"><img src="https://img.shields.io/github/tag/NimbleMarkets/ds4go.svg" alt="Latest Release"></a>
    <a href="https://pkg.go.dev/github.com/NimbleMarkets/ds4go?tab=doc"><img src="https://pkg.go.dev/badge/github.com/NimbleMarkets/ds4go?utm_source=godoc" alt="GoDoc"></a>
    <a href="https://github.com/NimbleMarkets/ds4go/blob/main/CODE_OF_CONDUCT.md"><img src="https://img.shields.io/badge/Contributor%20Covenant-2.1-4baaaa.svg"  alt="Code Of Conduct"></a>
</p>


`ds4go` is a zero-CGO Go wrapper for the [`ds4` inference engine](https://github.com/antirez/ds4). Applications using `ds4go` loads a pre-built `libds4` shared library at runtime with [`github.com/ebitengine/purego`](https://github.com/ebitengine/purego).  The shared library owns hardware acceleration. Use a Metal, CUDA, or CPU build of ds4 that matches your machine and model.

[`ds4`](https://github.com/antirez/ds4) itself is an inference engine focused on the [*DeepSeek v4 Flash* model](https://huggingface.co/deepseek-ai/DeepSeek-V4-Flash) targeting machines with 96G or more of GPU-accessible RAM.  

We try to maintain parity with the upstream `ds4` library, wrapping its C API.  We build slightly-opinionated tools to facilitate using `ds4`.

## Motivation

`C` is a wonderful language for low-level, high-performance, portable code; a clean C API can be wrapped and used by other laguages.    `Golang` is a wonderful language for systems and tools development, and generally more friendly for developers, esepecially when creating networked applications.  LLMs are great at programming both.   We take the high-performance `C` engine of `ds4` and allow Golang to directly utilize it, simplifying local LLM application development.

## Install

Install the `ds4go` CLI with the quick-install script, Homebrew, or the Go toolchain:

```sh
# Quick install script (Linux/macOS)
curl -fsSL https://nimblemarkets.github.io/ds4go/install.sh | sh

# Homebrew (macOS/Linux)
brew install --cask nimblemarkets/tap/ds4go

# or with the Go toolchain
go install github.com/NimbleMarkets/ds4go/cmd/ds4go@latest
```

To use ds4go as a library:

```sh
go get github.com/NimbleMarkets/ds4go
```

Once the CLI is installed, fetch a prebuilt native `libds4` from GitHub Releases:

```sh
ds4go install --backend auto
```

The installer downloads from `github.com/NimbleMarkets/ds4` by default. Use
`--repo`, `--version`, `--backend`, or `--url` to select a fork, release, build,
or direct archive. It installs into `$DS4_DIR/lib`, defaulting to `~/.ds4/lib`.
`--backend auto` selects `metal` on macOS arm64, `cuda` on Linux, and `cpu` elsewhere.
If the library is already installed and up-to-date, the installer exits successfully
without re-downloading. If a different version is present, it will prompt to replace it
(or require `--force` in non-interactive environments).

`DS4_DIR` is the ds4 home directory used by ds4go tooling:

```text
$DS4_DIR/lib/      native shared libraries
$DS4_DIR/models/   GGUF model files
```

Manage curated DeepSeek V4 Flash models with:

```sh
ds4go model list
ds4go model download q2-imatrix
ds4go model set q2-imatrix
```

The default model path for commands and examples is
`$DS4_DIR/models/ds4flash.gguf`.

Place the shared library in `~/.ds4/lib/`, `$DS4_DIR/lib/`, next to your
executable, or in a `lib/` directory next to your executable. You can also point
at it explicitly. The current working directory and the repository root are not
searched, to avoid loading a planted library:

```sh
export DS4_LIB=/absolute/path/to/libds4.dylib
# or
export DS4_DIR=/opt/ds4
```

Platform defaults are:

| Platform | Library |
| --- | --- |
| macOS | `libds4.dylib` |
| Linux | `libds4.so` |
| Windows | `libds4.dll` |

## Usage

```go
import ds4 "github.com/NimbleMarkets/ds4go"

engine, err := ds4.NewEngine(ds4.EngineOptions{
    ModelPath: "/models/ds4flash.gguf",
    Backend:   ds4.BackendMetal,
})
if err != nil {
    panic(err)
}
defer engine.Close()

session, err := engine.NewSession(32768)
if err != nil {
    panic(err)
}
defer session.Close()

prompt, err := engine.EncodeChatPrompt("", "Explain Redis streams briefly.", ds4.ThinkHigh)
if err != nil {
    panic(err)
}
defer prompt.Free()

_, err = ds4.Generator{Engine: engine, Session: session}.GenerateTokens(prompt, ds4.GenerateOptions{
    MaxTokens: 128,
    StopOnEOS: true,
    OnToken: func(token int) {
        text, _ := engine.TokenText(token)
        fmt.Print(text)
    },
})
```

## CLI

```sh
go run ./cmd/ds4go prompt --model ./ds4flash.gguf -p "Explain Redis streams in one paragraph."
go run ./cmd/ds4go prompt --model ./ds4flash.gguf
```

`cmd/ds4go prompt` and the examples accept the **same arguments as the upstream `ds4` C programs**, parsed with [`pflag`](https://github.com/spf13/pflag) so options take the `--option` form. `cmd/ds4go prompt`, `examples/simple`, and `examples/chat` mirror the `ds4` CLI (`ds4_cli.c`); `examples/openai-compatible` mirrors `ds4-server` (`ds4_server.c`). Run any of them with `--help` for the full list.

The only addition with no C equivalent is `--lib`, which points at the `libds4` shared library the pure-Go wrapper loads at runtime. When empty, ds4go searches `DS4_LIB`, `$DS4_DIR/lib` (or `~/.ds4/lib`), executable-local paths, and then the platform loader path.

```text
$ ds4go help cheat
ds4go — command cheat sheet

  ├── completion      Generate the autocompletion script for the specified shell
  │   ├── bash        Generate the autocompletion script for bash
  │   ├── fish        Generate the autocompletion script for fish
  │   ├── powershell  Generate the autocompletion script for powershell
  │   └── zsh         Generate the autocompletion script for zsh
  │
  ├── install  Download a prebuilt libds4 shared library
  │
  ├── model         Browse, download, and manage curated ds4 models
  │   ├── delete    Delete a downloaded model from disk
  │   ├── download  Download a curated model from Hugging Face
  │   ├── info      Show details for a curated model
  │   ├── list      List installed and available models
  │   └── set       Set the default chat model
  │
  ├── prompt  Run prompt or interactive chat inference
  │
  ├── status  Find processes holding or using the libds4 shared library
  │
  ├── uninstall  Uninstall the installed libds4 shared library
  │
  ├── validate  Validate the installed libds4 shared library
  │
  └── web         Test browser-backed web tools
      ├── search  Execute Google search and print Markdown links
      └── visit   Visit a web page and print extracted Markdown

Run 'ds4go help <command>' for detailed usage.
```

## Examples

```sh
go run ./examples/simple --model ./ds4flash.gguf
go run ./examples/chat --model ./ds4flash.gguf
go run ./examples/toolloop --mock
go run ./examples/toolloop --model ./ds4flash.gguf --nothink --tokens 512
go run ./examples/openai-compatible --model ./ds4flash.gguf --host 127.0.0.1 --port 8000
```

The `toolloop` example registers a Go `add` tool and exercises DSML tool-call parsing, tool dispatch, tool-result rendering, and exact replay. Use `--mock` for a no-model smoke test. The OpenAI-compatible example exposes `POST /v1/chat/completions` for a minimal local test server.

## API Coverage

Most users should import the root package `ds4` from `github.com/NimbleMarkets/ds4go`. It provides Go-native runtime policy and convenience helpers on top of the raw API.

The strict binding layer lives in package `ds4api`, imported as `github.com/NimbleMarkets/ds4go/ds4api`. It mirrors the public `ds4.h` API: engines, sessions, token vectors, chat prompt rendering, tokenization, logprob helpers, MTP metadata, directional steering options, snapshot/payload save-load, and DS4 context-memory helpers. APIs that take `FILE *` use the package's opaque `ds4api.File` wrapper around a C `FILE*`.

`ds4_log` is exposed as `LogString`, which safely calls it with a fixed `"%s"` format. Arbitrary C varargs are intentionally not surfaced as a Go variadic API. `SetStderr`/`SetStderrFd` redirect libds4's diagnostic stream to a file or descriptor (see below). `SetAbortFunc` exposes libds4's fatal-invariant hook, which fires immediately before libds4 aborts the process.

## Native stderr

`libds4` writes its diagnostics — including Metal/CUDA backend messages — to its
own `stderr` stream. ds4go redirects that stream to a file or descriptor with
`SetStderr`, `SetStderrFd`, and `DiscardLogs`:

```go
f, _ := os.Create("ds4.log")
err := ds4.SetStderr(f)   // redirect libds4 diagnostics to f
err = ds4.DiscardLogs()   // or send them to the null device
err = ds4.SetStderr(nil)  // restore the native stderr
```

libds4 dups the descriptor internally and writes unbuffered, so you may close
your file once it is no longer the active target. The redirect target is
process-global inside libds4, not per engine, so install it once during startup,
before `NewEngine`. It is targeted at libds4's own output — not a process-wide
`dup2` — so anything other libraries write directly to file descriptor 2 is
unaffected. Diagnostics are redirected as plain text; libds4 uses log levels only
to colorize TTY output, so no per-message level is surfaced to Go.

To capture diagnostics into an in-process `io.Writer` — a TUI log overlay, a ring
buffer, or an `slog` adapter — use `CaptureStderr`, which bridges the descriptor
redirect to a writer with an internal pipe and pump goroutine:

```go
cap, err := ds4.CaptureStderr(myWriter) // libds4 diagnostics stream into myWriter
defer cap.Close()                       // restore native stderr and drain on exit
```

Redirection is **not supported on Windows**: `os.File.Fd` returns a Win32
`HANDLE`, which libds4's CRT-based `ds4_set_stderr_fd` cannot accept, so these
calls return `ErrStderrUnsupportedOnWindows` there.

For CLI use, you can also redirect stderr with your shell:

```sh
ds4go prompt ... 2>ds4.log
ds4go prompt ... 2>/dev/null
```

## Fatal abort hook

Recent `libds4` builds expose `ds4_abort_set`, and ds4go wraps it as
`SetAbortFunc`. This is a last-chance fatal-invariant hook: libds4 calls it
after logging the fatal message at `LogError` and immediately before native
`abort()`.

```go
err := ds4.SetAbortFunc(func(msg string) {
    crashReporter.Record("libds4 fatal invariant", msg)
})
```

Returning from the callback does not recover the engine. The native library
still calls `abort()` because the invariant is already broken. Use the hook for
crash telemetry, flushing logs, or deliberate process termination. Do not call
back into ds4go/libds4 from the callback; it can run from native worker threads
while an FFI call is active.

## Signal Safety

**Do not use `signal.NotifyContext` around C FFI calls.** `SIGINT` (Ctrl+C) can be delivered to any OS thread, including C worker threads inside `libds4` (Metal, CUDA, or CPU). When that happens the C runtime aborts and the process segfaults.

Safe cancellation is **programmatic only** — pass a `context.Context` to `GenerateOptions.Context` and cancel it from Go code. The generator checks `ctx.Done()` between tokens, so cancellation never interrupts an active FFI call:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

_, err = ds4.Generator{Engine: engine, Session: session}.GenerateTokens(prompt, ds4.GenerateOptions{
    MaxTokens: 128,
    Context:   ctx,
    OnToken: func(token int) {
        text, _ := engine.TokenText(token)
        fmt.Print(text)
    },
})
```

This is exactly how `examples/openai-compatible` handles client disconnects — it wires `r.Context()` into generation so the engine stops cleanly when the HTTP connection drops.

## Notes

Bindings are generated by hand against the public ds4 header at [`https://github.com/antirez/ds4/blob/main/ds4.h`](https://github.com/antirez/ds4/blob/main/ds4.h).

Inference runs in-process. The Golang wrapper adds FFI calls but does not proxy tokens through a server or copy model weights. Prefill, generation, Metal/CUDA/CPU execution, MTP, KV reuse, and disk KV payload serialization are all handled by the loaded `ds4` shared library.

## Open Collaboration

We welcome contributions and feedback.  Please adhere to our [Code of Conduct](./CODE_OF_CONDUCT.md) when engaging our community.

 * [GitHub Issues](https://github.com/NimbleMarkets/ds4go/issues)
 * [GitHub Pull Requests](https://github.com/NimbleMarkets/ds4go/pulls)

## Acknowledgements

Thanks to [@antirez](https://github.com/antirez) for his work on [`ds4`](https://github.com/antirez/ds4) and for his local-LLM advocacy.  Thanks to [DeepSeek](https://www.deepseek.com/) for their public contributions.

## License

Released under the [MIT License](https://en.wikipedia.org/wiki/MIT_License), see [LICENSE.txt](./LICENSE.txt).

Copyright (c) 2026 [Neomantra Corp](https://www.neomantra.com).   

----
Made with :heart: and :fire: by the team behind [Nimble.Markets](https://nimble.markets).
