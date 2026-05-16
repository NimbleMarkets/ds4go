# Models

`ds4` targets the DeepSeek V4 Flash GGUF layout. It is not a generic GGUF runner.

Typical local layout:

```text
models/
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