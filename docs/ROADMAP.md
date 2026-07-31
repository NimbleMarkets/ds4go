# ds4go Roadmap

Planned work, tracked at a high level.

## Porting tools from ds4

ds4-go currently ships the webtool search/visit tools. Upstream ds4
(`ds4_agent.c`) carries additional built-in agent tools worth porting as
native Go `ToolHandler`s.

### Edit tool (file edits by head/tail anchor)

Port the upstream agent's file-edit tool, which applies an edit by matching a
unique **head anchor** and **tail anchor** in the existing file rather than
line numbers (`agent_edit_find_old_span` in `ds4_agent.c`).

- Reference implementation: ds4 upstream.
- Required behaviors and known pitfalls, from upstream history:
  - `a9f486b` "Fix agent: old tail anchor not found after old head" — strip a
    leading newline/CR from the tail needle so the post-head text matches the
    file's single newline. Port this fix, do not re-introduce the bug.
  - `7624c68` "Add ds4-agent edit regression tests" — port these cases as Go
    table tests for the anchor matcher.
- Surface as a registered `dsml.Tool` / `ToolHandler` so the existing tool loop
  drives it; no new tool-calling machinery required.

### Other candidates

Triage remaining upstream agent tools (e.g. shell/command execution, file read)
against ds4-go's sandboxing and security posture before porting.

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

The catalog carries the three single-file `antirez/GLM-5.2-GGUF` quants. The
Unsloth `UD-Q4_K_XL` build ds4's `download_model.sh` also offers is 11 shards
(~467 GB), which the catalog cannot express: `Model` names one file, and the
downloader fetches one URL. Supporting it needs a multi-file model entry plus
shard-aware download, resume, and verification.
