# HTTP server

`ds4go serve` is currently unreleased. Build the current checkout; published
binaries may not yet include it. From the repository root:

```sh
CGO_ENABLED=0 go build -o bin/ds4go ./cmd/ds4go
./bin/ds4go serve --model /path/to/model.gguf --ctx 4096
```

Install a compatible libds4 and a model first; see [installation](../INSTALL.md)
and the [model catalog](../MODELS.md). `--lib` or `DS4_LIB` selects an explicit
library. Installed model aliases also work, for example `-m qwen38-q2`.
The same server is available through `go run ./examples/openai-compatible`.

Defaults are `--host 127.0.0.1 --port 8000 --ctx 32768 --tokens 393216`.
`--tokens` is both the default output limit and the server's per-request ceiling;
remaining context space also limits generation. `--cors` enables browser CORS
headers. Run `serve --help` for model and runtime flags.

## Endpoints and examples

| Endpoint | Supported input |
| --- | --- |
| `GET /v1/models`, `GET /v1/models/{id}` | Loaded-model metadata and compatibility aliases |
| `POST /v1/chat/completions` | Chat messages, reasoning, function tools |
| `POST /v1/completions` | Raw prompt string or a one-element string array |
| `POST /v1/responses` | Input string or message, reasoning, function-call and function-output items |
| `POST /v1/messages` | Anthropic text, thinking, tool-use, tool-result and image blocks |

These examples assume a loaded Qwen model. The request's `model` alias does
not load or switch models: every alias describes the one engine loaded at
startup. Query `/v1/models` for the loaded family's aliases.

```sh
curl http://127.0.0.1:8000/v1/models

curl -N http://127.0.0.1:8000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen3.8-flash-next-chat","messages":[{"role":"user","content":"Hello"}],"temperature":0,"max_tokens":64,"stream":true}'

curl http://127.0.0.1:8000/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen3.8-flash-next-chat","input":"Say hello.","max_output_tokens":64}'

curl http://127.0.0.1:8000/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen3.8-flash-next-chat","messages":[{"role":"user","content":"Hello"}],"max_tokens":64}'

curl http://127.0.0.1:8000/v1/completions \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"The capital of France is","max_tokens":16,"temperature":0}'
```

All inference endpoints support JSON responses and `stream: true`. Chat and
Completions stream data chunks followed by `[DONE]`. Responses and Messages
use named SSE lifecycle events. Completed responses include usage and finish
or stop reasons. Function-call IDs remain stable; function arguments are emitted
as complete validated calls, rather than arbitrary partial JSON fragments.

Discovery reports context and completion limits, input/output modalities, and
implemented request parameters, with `owned_by: "ds4go"`. Image input is
advertised only when a vision encoder is actually loaded.

## Request controls

Sampling defaults are `temperature: 1`, `top_k: 0`, `top_p: 1`, and `min_p: 0.05`.
Temperature and top-k must be nonnegative; top-p and min-p must be in [0,1].
Explicit zero values are preserved. An omitted seed is randomized; `seed: 0`
is deterministic. `ignore_eos` requires explicit `temperature: 0`.

Chat accepts `max_tokens` or `max_completion_tokens`; Responses uses
`max_output_tokens`; Anthropic Messages requires positive `max_tokens`.
`stop` accepts a nonempty string or an array of nonempty strings. Messages uses
`stop_sequences`. Stop strings are withheld even across token boundaries.

Qwen and GLM `-chat`, `-no-think`, and `-nothink` aliases disable thinking.
`chat_template_kwargs.enable_thinking` overrides the alias; reasoning effort
`none` disables thinking. Responses accepts `reasoning.effort`. Messages maps
`thinking.type` enabled/adaptive to high effort and disabled to none.

## Tools, replay, and images

Clients execute tools and send the results back with conversation history.
Tool choice supports `auto` and `none` (Messages uses objects with those `type`
values). GLM/Qwen argument conversion uses the supplied JSON schema.

Send the full history on every request:

- Chat: retain assistant `reasoning_content`, tool calls and tool result messages.
- Responses: append prior output items and `function_call_output` items to `input`.
- Messages: retain assistant thinking/tool-use blocks and user tool-result blocks.

A bounded in-memory map preserves exact DeepSeek tool markup by call ID across
requests. Configure `--tool-memory-max-ids` or disable it with
`--disable-exact-dsml-tool-replay`. It is not durable conversation storage.

Images must be inline PNG/JPEG data in the protocol's image representation;
remote URLs and file references are unsupported. Only user and tool messages
may carry images. The server accepts at most 256 images per request and a
64 MiB request body. See [vision support](../README.md) for engine setup.

## Sessions and batching

`--batched-session 0` uses one resident session and retains optional MTP/DSpark
generation when configured for the engine. Set `--batched-session N` to keep
up to N resident sessions and group ready decode tokens through `Engine.EvalBatch`.
Batched mode currently uses ordinary target decoding.

libds4 chooses native batching or sequential evaluation according to support;
older libraries without the batch symbol use the Go sequential fallback.
Text prefill yields between 128-token chunks. Image prompts use whole multimodal
syncs to preserve image boundaries. Queued requests are cancellable; failed or
cancelled sessions are discarded. Cancellation takes effect at safe Go-side
control points, not by interrupting an active native call.

Successful sessions can reuse matching prompt prefixes, but requests have no
session affinity. Clients must still supply history.

## Remaining parity gaps

The server rejects nonempty `previous_response_id`, background Responses,
Responses hosted/built-in tool types, forced tool choices, and multiple raw
completion prompts. Nonempty `--kv-disk-dir` and `--trace` fail at startup.
Other disk-cache tuning flags are retained for argument compatibility but do
not enable caching. There is no persistent Responses store.

Native mixed prefill/decode scheduling and batched speculative decoding are
not implemented. This is a supported subset of the upstream and vendor APIs;
acceptance of an unknown JSON field does not imply support for it.

Protocol and scheduler tests cover mock inference; local Qwen/Metal smoke tests
cover metadata, JSON/SSE and concurrent requests. Equivalent CUDA/ROCm and
other model-family validation remains pending. See the [roadmap](ROADMAP.md).
