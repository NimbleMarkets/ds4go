# Models

`ds4` targets the DeepSeek V4 Flash GGUF layout. It is not a generic GGUF runner.

## Model Browser

`ds4go model` manages the curated models published for upstream `ds4`:

```sh
ds4go model list
ds4go model info q2-imatrix
ds4go model download q2-imatrix
ds4go model set q2-imatrix
```

The local layout is:

```text
$DS4_DIR/
  ds4go.json
  models/
    ds4flash.gguf
    DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix.gguf
    DeepSeek-V4-Flash-MTP-Q4K-Q8_0-F32.gguf
```

`ds4flash.gguf` is the active-model symlink or hard link. `ds4go.json` stores
the selected alias and catalog metadata.

Curated aliases:

| Alias | Recommended For | Size | Notes |
| --- | --- | ---: | --- |
| `q2-imatrix` | 96-128 GB RAM | 81.2 GB | Preferred imatrix-tuned default |
| `q4-imatrix` | >=256 GB RAM | 153 GB | Higher quality |
| `q2` | 96-128 GB RAM | 87 GB | Legacy non-imatrix q2 |
| `q4` | >=256 GB RAM | 165 GB | Legacy non-imatrix q4 |
| `mtp` | Optional | 3.6 GB | Speculative decoding companion model |

Typical local layout:

```text
~/.ds4/models/
  ds4flash.gguf
  ds4flash-mtp.gguf
```

Then:

```go
engine, err := ds4.NewEngine(ds4.EngineOptions{
    ModelPath:      "models/ds4flash.gguf",
    MTPPath:        "models/ds4flash-mtp.gguf",
    Backend:        ds4.BackendMetal,
    MTPDraftTokens: 2,
})
```

Use `ThinkMax` only with a context size large enough for ds4's own `ThinkMaxMinContext` policy:

```go
mode := ds4.ThinkModeForContext(ds4.ThinkMax, ctxSize)
```

Directional steering is passed through `EngineOptions`:

```go
ds4.EngineOptions{
    DirectionalSteeringFile: "steering.bin",
    DirectionalSteeringAttn: 1.0,
    DirectionalSteeringFFN:  1.0,
}
```

DSML or tool-calling instructions should be rendered into the system/developer prompt content you pass to ds4's chat helpers. The public `ds4.h` API currently exposes chat template functions, not a separate structured tool schema API.
