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
