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
| `q2-q4-imatrix` | 128-192 GB RAM | 98 GB | Mixed q2/q4 imatrix: q2 routed experts, last 6 layers q4 |
| `q4-imatrix` | >=256 GB RAM | 153 GB | Higher quality |
| `pro-q2-imatrix` | >=512 GB RAM | 430 GB | DeepSeek V4 Pro q2 imatrix quant |
| `pro-q4-layers00-30` | Distributed, 2 hosts | 426 GB | Pro Q4 split: coordinator half (`--layers 0:30`) |
| `pro-q4-layers31-output` | Distributed, 2 hosts | 412 GB | Pro Q4 split: worker half (`--layers 31:output`) |
| `mtp` | Optional | 3.6 GB | Speculative decoding companion model |

The `pro-q4-layers*` halves form a single DeepSeek V4 Pro Q4 model split across two
hosts; download both (≈838 GB total). Each entry carries its `distributedRole`
(`coordinator`/`worker`) and `layerRange` (in upstream `--layers` form, `0:30` and
`31:output`) as catalog data so external tooling can construct the run directly.
ds4go manages download, listing, and file availability for these halves but does
not drive the distributed run itself; they are not auto-linked as the active
`ds4flash.gguf` and cannot be set as the default chat model.

Typical local layout:

```text
~/.ds4/models/
  ds4flash.gguf
  DeepSeek-V4-Flash-MTP-Q4K-Q8_0-F32.gguf
```

When the MTP model is installed it is discovered automatically; you can also
refer to it explicitly:

```go
engine, err := ds4.NewEngine(ds4.EngineOptions{
    ModelPath:      ds4.DefaultModelPath(),
    MTPPath:        ds4.DefaultMTPPath(),
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
