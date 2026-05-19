# Benchmarks

`ds4go` does not benchmark a bundled model because the shared library and hardware backend are user supplied. Measure the same ds4 library you run in production.

Suggested command:

```sh
time go run ./cmd/ds4go prompt \
  --model ./ds4flash.gguf \
  --lib "$DS4_DIR/lib/libds4.dylib" \
  --ctx 32768 \
  --tokens 256 \
  -p "Explain Redis streams in one paragraph."
```

For comparable results, record:

| Field | Value |
| --- | --- |
| Machine | |
| OS | |
| ds4 commit | |
| libds4 backend | Metal / CUDA / CPU |
| Model quant | q2 / q4 / other |
| Context size | |
| Prompt tokens | |
| Generated tokens | |
| Prefill tokens/sec | |
| Generation tokens/sec | |

The wrapper is in-process and does not add HTTP, JSON, or subprocess overhead. Most timing differences should come from `ds4`, the backend, model quantization, context size, and prompt length.
