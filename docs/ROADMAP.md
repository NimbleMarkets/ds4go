# ds4go Roadmap

Planned work, tracked at a high level.

## HTTP server parity

`ds4go serve` now shares model discovery, Chat Completions, raw Completions,
Responses, and Anthropic Messages with the HTTP example, including JSON/SSE,
sampling controls, reasoning/tool replay, and bounded resident-session batching.
See the [server guide](SERVER.md) for the supported subset.

Remaining work:

- Continued-generation disk checkpoints, persistent exact tool-replay metadata,
  image checkpoints, and session trace files. Text-prefix disk caching is implemented.
- Stateful Responses (`previous_response_id`) and background execution.
- Forced tool choice and hosted tools.
- Native mixed prefill/decode scheduling and speculative decoding in batched mode.
- Real-model validation beyond the local Qwen/Metal HTTP smoke tests, including
  CUDA and ROCm concurrency and model-family tool replay.

## Agent tools

[workspacetool](../workspacetool/README.md) already provides file reads,
directory listing, search, exact edits, whole-file writes, and opt-in shell
jobs alongside webtool search/visit. Head/tail anchor editing and its newline
regressions are covered by tests. Future upstream tool additions should be
reviewed against these existing packages before adding new handlers.

## GLM 5.2 support

ds4go parses, renders, and streams GLM DSA tool markup (`dsml.SyntaxGLM`,
selected per engine by `ds4.ToolSyntax`). Remaining work:

### Verify the tools prompt against a real GLM model

The GLM tools section is ported from ds4's `agent_glm_tools_prompt_intro` /
`agent_glm_tools_prompt_after_schemas`, but has only been exercised against the
mock library. It still needs a run against a real GLM 5.2 GGUF to confirm:

- the `<tools>` JSON-schema block tokenizes and is honored as ds4 renders it;
- the model emits `<tool_call>` markup our grammar accepts, end to end through
  `ToolLoop`;
- tool results returned under the `"tool"` role land as
  `<|observation|><tool_response>` in the prompt, and multi-turn replay of
  assistant tool calls keeps the session KV prefix reusable.

ds4's version of the prompt then lists usage rules for its own fixed tool set
(`read`/`more`/`edit`/`bash`). Those are deliberately omitted because ds4go's
tool set is caller-defined; revisit if real runs show the model needs more
grounding than the schemas provide.

### Sharded GGUF models

The catalog carries the three single-file `antirez/GLM-5.2-GGUF` quants.
The downloader now supports multi-part entries (`Model.Parts`) and standard
GGUF shard merging, used by the V4.1 MXFP4 model. Adding the 11-shard Unsloth
`UD-Q4_K_XL` GLM build still requires a catalog entry with pinned shard and
merged-file checksums, storage estimates, and real-model validation.
