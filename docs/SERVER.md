# HTTP server

Start the OpenAI- and Anthropic-compatible HTTP server with `ds4go serve`:

```sh
ds4go serve --model /path/to/model.gguf --ctx 4096
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

## Disk checkpoints

Enable persistent text-prefix reuse with:

```sh
./bin/ds4go serve --model /path/to/model.gguf --kv-disk-dir /path/to/cache \
  --kv-disk-space-mb 4096
```

The server owns a private `ds4go-kv-v1` subdirectory and locks it for its lifetime;
use a different directory for each simultaneously running server. Cache payloads
and metadata contain prompt information. They are private to the local account
and are not encrypted. This format is separate from upstream ds4's cache format.

| Flag | Default | Behavior |
| --- | --- | --- |
| `--kv-disk-space-mb` | 4096 | Payload and metadata budget in MiB; evict least recently used entries |
| `--kv-cache-min-tokens` | 512 | Minimum checkpoint length to save or load |
| `--kv-cache-cold-max-tokens` | 30000 | Only save prefixes of prompts no longer than this; 0 means load only |
| `--kv-cache-boundary-trim-tokens` | 32 | Leave this many prompt-tail tokens out of a new checkpoint |
| `--kv-cache-boundary-align-tokens` | 2048 | Round the saved prefix down to this multiple; 0 disables rounding |
| `--kv-cache-continued-interval-tokens` | 0 | Only 0 is supported; no periodic generation checkpoints |
| `--kv-cache-reject-different-quant` | true | Must remain true; cross-model/quantization reuse is unsupported |

The trimmed, aligned prefix must still meet the minimum length. With defaults,
a prompt needs at least 2080 tokens to produce a checkpoint. The prefix is synced
and saved before generation; the remaining prompt is then synced normally.
Already-resident matching state is preferred over a shorter disk checkpoint.
`disk KV stored` and `disk KV hit` log messages identify successful writes/loads.

Entries are isolated by engine options, runtime environment switches, and the resolved paths, sizes and
modification times of the model, companion models, steering file and runtime.
Changing those identities invalidates reuse. Do not replace weights in place
while preserving their size and modification time. Native payloads are streamed
to disk without allocating an extra Go snapshot; SHA-256 detects payload damage
before native loading. Writes publish atomically. Interrupted writes are cleaned
up at startup; corrupt or rejected entries are removed. Restore failures discard
any partially restored session and fall back to normal prefill.

The disk budget covers published payload and metadata bytes, excluding filesystem
allocation overhead. Oversized checkpoints are skipped; disk write failures are
logged and inference continues. Startup directory/locking errors fail startup.
Cache I/O is serialized and may delay concurrent requests; this is an opt-in
latency/storage tradeoff, not asynchronous background checkpointing.

Image-bearing requests bypass disk caching. Exact DeepSeek tool replay remains
an in-memory map and is not persisted with checkpoints. Reuse requires an exact
token prefix, so histories re-rendered differently after restart may miss the
cache. Clients must always send full history; this is not a Responses store.

## Remaining parity gaps

The server rejects nonempty `previous_response_id`, background Responses,
Responses hosted/built-in tool types, forced tool choices, and multiple raw
completion prompts. Explicit `--trace` fails at startup. Cache tuning flags
require a nonempty `--kv-disk-dir`; unsupported continued-checkpoint and
cross-quantization settings fail before loading the model. There is no persistent
Responses store.

Native mixed prefill/decode scheduling and batched speculative decoding are
not implemented. This is a supported subset of the upstream and vendor APIs;
acceptance of an unknown JSON field does not imply support for it.

Protocol and scheduler tests cover mock inference; local Qwen/Metal smoke tests
cover metadata, JSON/SSE and concurrent requests. Equivalent CUDA/ROCm and
other model-family validation remains pending. See the [roadmap](ROADMAP.md).

## Real-model release check (including Spark)

Run from the repository root on the machine hosting the model and runtime:

```sh
DS4_LIB=/path/to/libds4.so DS4_MODEL=/path/to/model.gguf DS4_BACKEND=cuda \
  go test -tags ds4_integration ./internal/openaiserver \
  -run '^TestRealServerDiskCache$' -count=1 -v -timeout 30m
```

Use `DS4_BACKEND=rocm` or `metal` for those backends. For runtime tuning, set
`DS4_SERVER_TEST_FLAGS` to a JSON array, for example
`'["--ssd-streaming","--ssd-streaming-cache-experts","4GB"]'`.
The default test uses two sessions, a 2048-token context, and no external MTP.
Run again with `DS4_SERVER_TEST_FLAGS='["--batched-session=0"]'` to cover the
single-session path (combine with any required streaming flags).

The test writes native checkpoints to a temporary directory, recreates the
handler with empty resident sessions, requires a disk hit, compares cached and
uncached greedy output, then checks concurrency, cancellation recovery, and a
model-generated integer tool call followed by replay. The temporary cache is
removed afterward. A model that fails to follow the tool prompt fails the test
with its actual output for inspection. Repeat with each model family intended
for the release; save the output along with the model, libds4 revision and GPU.
Local Qwen/Metal passes do not substitute for Spark/CUDA or ROCm validation.
