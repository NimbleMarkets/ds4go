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

Install the `ds4go` CLI with Homebrew or the Go toolchain:

```sh
# Homebrew (macOS/Linux)
brew install nimblemarkets/tap/ds4go

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

Place the shared library in `~/.ds4/lib/`, next to your executable, or point at
it explicitly (the working directory is not searched, to avoid loading a
planted library):

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

The only addition with no C equivalent is `--lib`, which points at the `libds4` shared library the pure-Go wrapper loads at runtime (empty falls back to `DS4_LIB` or `DS4_DIR/lib`).

## Examples

```sh
go run ./examples/simple --model ./ds4flash.gguf
go run ./examples/chat --model ./ds4flash.gguf
go run ./examples/openai-compatible --model ./ds4flash.gguf --host 127.0.0.1 --port 8000
```

The OpenAI-compatible example exposes `POST /v1/chat/completions` for a minimal local test server.

## API Coverage

Most users should import the root package `ds4` from `github.com/NimbleMarkets/ds4go`. It provides Go-native runtime policy and convenience helpers on top of the raw API.

The strict binding layer lives in package `ds4api`, imported as `github.com/NimbleMarkets/ds4go/ds4api`. It mirrors the public `ds4.h` API: engines, sessions, token vectors, chat prompt rendering, tokenization, logprob helpers, MTP metadata, directional steering options, snapshot/payload save-load, and DS4 context-memory helpers. APIs that take `FILE *` use the package's opaque `ds4api.File` wrapper around a C `FILE*`.

`ds4_log` is exposed as `LogString`, which safely calls it with a fixed `"%s"` format. Arbitrary C varargs are intentionally not surfaced as a Go variadic API.

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
