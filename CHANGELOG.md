# `ds4go` CHANGELOG

NOTE: This currently needs a patched `ds4` to make a shared library and route logging and aborts; we also embed the `.metal` files.   See https://github.com/NimbleMarkets/ds4/tree/nm-shared

## Unreleased

 * **Vision**: images in conversations for DeepSeek Flash Vision-Exp and GLM 5.3 Flash. `ds4api` binds the vision API; `ChatMessage.Parts` carries ordered text and image parts; `BuildChatPromptMultimodal` returns a `Prompt` with image spans; `Generator.GeneratePrompt` and `ToolLoop.Images` sync it; an `ImageEncoder` caches embeddings by image bytes. CLI: `--vision`, `--image`, and `/read` with PNG/JPEG; catalog entries `vision-q2`, `vision-q2-q4`, `vision-mxfp4`, `vision-encoder`, `vision-dspark-support`, `glm53-vision`, with the encoder paired automatically when installed. `workspacetool` gains `view_image`. A catalog vision model auto-loads its installed encoder at startup (about 1 GiB of extra mapped weights); `--vision ""` is the unset default, not a way to switch that off, so remove or rename the encoder GGUF to run such a model text-only. Vision-Exp checkpoints pin their own DSpark drafter (`vision-dspark-support`): `--mtp` resolves to that drafter or to none, never to the 0731 support model, which libds4 rejects against them.
 * **Chat mode prompt rendering**: `ds4go prompt` chat turns now render through the shared tool-aware prompt builder, so Think Max's prefix and GLM's reasoning-effort line appear in chat mode as upstream's CLI emits them, and assistant turns replay through the rendered-chat tokenizer.

## v0.6.0 (2026-09-10)

Requires libds4 **v0.5.20260910** or newer (NimbleMarkets/ds4 `nm-shared`, rebased on upstream ds4 6289c51). The engine options struct changed shape upstream, and older libraries misread it; `ds4go install` picks up the current release.

**Highlights**

 * **GLM 5.2 and GLM 5.3 Flash support**: catalog entries, GLM tool-call markup, stop tokens, reasoning effort, and the embedded MTP draft block (`EngineOptions.GLMMTP`) for speculative decoding without an external support model.
 * **`workspacetool` package**: local read/list/search tools for `ToolLoop`, with opt-in write, edit, and shell jobs confined to a workspace root.
 * **Tool-call parity with upstream ds4**: literal tool bodies (no HTML unescaping), structural markers quoted inside arguments no longer split a call, wrapper-only repair, greedy grammar sampling across speculative blocks, and schema-typed GLM arguments.
 * **Sessions**: rewinds now follow upstream's checkpoint semantics via `Session.RewindSynced`, and the generator discards draft tokens past a stop, a cancel, or a resampled boundary.
 * **CLI**: `ds4go --version`, `ds4go model list --installed|--available|--json`, a download progress line that survives terminal resizes and shows a live rate and ETA, and downloads refused when the volume cannot hold them.
 * **Build**: root module is self-contained for `go get`, `task ds4:sync` catches upstream ABI drift, and dependencies are current (purego 0.11).

<details>
<summary>Full changelog</summary>

 * **`ds4go --version`**: release builds report the tag; `go install` builds report the module version, and plain builds the VCS revision.
 * **`ds4go model list` filters and JSON**: `--installed` and `--available` show one group, `--all` (the default) shows both, and `--json` emits one object with `modelsDir`, `libraryDir`, `default` (null when nothing is active), and the filtered `models` array in the same per-model shape as `model info --json`.
 * **Model download progress reads as live**: the speed is a moving average over the last five seconds instead of the average since start, the downloaded figure shows two decimals from GiB upward, and an ETA from the moving rate is appended. On a 90 GiB file at 10 MiB/s the line used to change every 5-10 seconds; it now changes every frame.
 * **fix: download progress survives terminal resizes.** The installer and model-download progress lines padded each frame to the terminal width and redrew with a bare carriage return; shrinking the window reflowed the padded row onto two rows and left the old frame's head orphaned above every later redraw. Both now draw through a shared `internal/termline` renderer that erases rather than pads, parks the cursor at column 0, and keeps frames under the width, re-read per frame.
 * **build**: the root module is self-contained again (it no longer needs the workspace to resolve lipgloss), CI covers the `cmd` module, and dependencies are updated (purego 0.11, x/sys 0.48, bubbletea 2.0.9, lipgloss 2.0.6).
 * **GLM embedded MTP and speculative boundaries**: expose `EngineOptions.GLMMTP` / `GLMMTPTiming` (upstream `--mtp` / `--mtp-timing` on GLM) and gate speculative decoding on `Engine.MTPDraftTokens() > 1` alone, as upstream's clients do, so GLM's built-in draft block can engage without an external MTP model. Bind `ds4_engine_mtp_exact_sampling` as `Engine.MTPExactSampling`. `Generator.Continue` now discards draft tokens evaluated past a stop token or a mid-block cancel, and under exact sampling at positive temperature re-evaluates the boundary token and resamples when `SampleControl` flips (upstream 5b3cc8b, 930ab73).
 * **Session rewind semantics (upstream 233eeb8)**: `Session.Rewind` no longer leaves a usable checkpoint on DeepSeek (`Argmax`/`Sample` return -1, `Eval` fails until the retained prefix is synced). Add `Session.RewindSynced`, which mirrors upstream's rewind-then-resync protocol, and make `Generator` report a missing checkpoint as an error instead of evaluating -1. The mock library models the invalidation.
 * **GLM 5.3 model catalog**: add `glm53-q2` and `glm53-q4` (GLM 5.3 Flash, `antirez/glm-5.3-flash-gguf`) and `glm53-full-q2` (full GLM 5.3, `antirez/glm-5.3-gguf`) with pinned sizes and SHA256s, mirroring upstream `download_model.sh`. They share GLM 5.2's tool syntax, stop tokens, and embedded-MTP policy.
 * **Tool-call parsing parity with upstream ds4 (d108ae4, fc6414c, 759dd7c)**: tool bodies are no longer HTML-unescaped. DSML parameter values, GLM `<arg_key>`/`<arg_value>` bodies, and tool results escape only their own closing delimiter (plus the already-escaped spelling that would collide with it), and decoding reverses exactly that, so `&amp;`, `&lt;`, and friends in file contents or shell commands survive verbatim. `</think>` and `</tool_call>` quoted inside an argument body no longer end thinking or split the call. `dsml.RepairCompletion` now supplies only a missing `</tool_calls>` wrapper; a truncated parameter or invoke is left raw rather than closed into an executable action.
 * **Upstream ABI sync (ds4 6289c51)**: `ds4_engine_options` gained `vision_path` (exposed as `EngineOptions.VisionPath`), and `ds4_engine_collect_imatrix` gained `min_expert_samples` (exposed as `Engine.CollectIMatrixWithMinExpertSamples`; `CollectIMatrix` passes 0). Both changes silently corrupted the call ABI against a current libds4 without them. Bind `ds4_session_directional_steering_ffn` / `ds4_session_set_directional_steering_ffn` as `Session.DirectionalSteeringFFN` / `SetDirectionalSteeringFFN` for live steering changes; the old `SetDirectionalSteering` bound a symbol upstream never exported and is deprecated.
 * Add [`workspacetool` package](./workspacetool/README.md) providing local workspace tools for `ds4go.ToolLoop`: `read`, `more`, `list`, `search`, plus opt-in `write`/`edit` and shell job tools (`bash`, `bash_status`, `bash_stop`). Conservative by default: paths are confined to the workspace root, symlink traversal is rejected, and writes and shell require explicit `AllowWrite`/`AllowShell` opt-in, with an optional `Confirm` callback for application-level approval.
 * **GLM 5.2 support**: bind libds4's GLM model-family, stop-token, and reasoning-effort helpers; select prompting, stop detection, assistant/tool replay, and tool syntax from the loaded engine; and add parsing, rendering, canonicalization, and incremental streaming for GLM's `<tool_call>` markup alongside DeepSeek DSML.
 * **GLM tool-call correctness**: restore JSON scalar and container types from registered tool schemas because GLM `<arg_value>` markup carries values as strings; if trailing prose invalidates a streamed GLM tool-call block, consistently degrade the whole completion to raw content without publishing tool events.
 * **GLM model catalog and MTP policy**: add `glm-iq2xxs`, `glm-q2`, and `glm-q4` catalog entries from `antirez/glm-5.2-gguf`. Curated GLM models suppress an external `MTPPath` because their optional next-token predictor is embedded in the base GGUF; DeepSeek models retain their separate MTP support model.
 * **Engine context and placement hints**: expose `EngineOptions.ContextSize`, `PlacementCtxHint`, `PlacementSessionCountHint`, and `ShareSessionPrefillWorkspace`, and map CLI/server `--ctx` to the engine context size and placement-context hint.
 * **Model download safety**: estimate required temporary and final storage and refuse downloads that cannot fit on the destination volume. Download progress now uses the real terminal width in both installer and model TUI paths.
 * **Upstream ABI synchronization**: add `task ds4:sync` / `scripts/check-ds4-sync.sh` to compile the real upstream header and detect stale Go snapshots of byte-offset-sensitive C structs.
 * **DSML parsing hardening** (ported from `ds4` upstream): a bare `<｜DSML｜invoke>` with no `<｜DSML｜tool_calls>` wrapper is now accepted as an implicit single-call block; stray DSML markers in plain assistant output are reported as malformed so the tool loop asks the model to retry; and `invoke`/`parameter` openers require a tag delimiter, so `<｜DSML｜invokeX` no longer false-matches `invoke`.
 * **Structure-aware greedy sampling**: tool turns now sample DSML grammar greedily (argmax) while keeping the configured sampling for parameter values, improving tool-call reliability. It is a no-op at temperature <= 0, so speculative decoding is unaffected. Exposed as `GenerateOptions.SampleControl` and `StreamDecoder.WantsGreedySampling`.

</details>

## v0.5.1 (2026-06-10)

 * Add `ds4go install catalog` command
 * Add AMD ROCm support
 * Add `ds4` [SSD streaming support](https://github.com/antirez/ds4#pro-on-128gb-macbooks)
 * Add session cancellation with `session.SetCancel`
 * Tool loops now replay assistant reasoning across turns (preserving KV-cache reuse) and recover tool calls the model starts inside an unclosed `<think>` block.

## v0.5.0 (2026-06-05)

 * **BREAKING** We changed how logging works in our `nm-shared` branch of `ds4`, so there is now `SetStderrFd` rather than `SetLogFunc`.
 * Add [`lsp` package](./lsp/README.md) for Language Server Protocol (LSP) support
 * Add [`webtool` package](./webtool/README.md) for searching and querying web pages using Google Chrome.
 * Add `ds4go web search` and `ds4go web visit` CLI tools
 * Add `ds4.DetectDefaultBackend`
 * Add `ds4.CaptureStderr(io.Writer)` helper 

## v0.4.0 (2026-05-27)

 * **Quick install script**: `curl -fsSL https://nimblemarkets.github.io/ds4go/install.sh | sh` lands the CLI in `/usr/local/bin` with checksum verification
 * Added `ds4go install --pin` to use custom `ds4` dynamic libraries.
 * Rearraged CLI with `ds4go validate` and `ds4go status`
 * **libds4 power throttling**: bind `ds4_engine_power`, `ds4_engine_set_power`, `ds4_session_power`, `ds4_session_set_power`, plus the new `EngineOptions.PowerPercent` field, for runtime GPU duty-cycle control (libds4 >= upstream commit 444afce / f398aa3)
 * **libds4 display progress**: bind `ds4_session_set_display_progress` as `Session.SetDisplayProgress` for UI-only fine-grained prefill progress (libds4 >= upstream commit fc1450d); distinct from `SetProgress`, must not be treated as a durable KV checkpoint boundary
 * **libds4 vocab and logits**: bind `ds4_engine_vocab_size` as `Engine.VocabSize` and `ds4_session_copy_logits` as `Session.CopyLogits`, both present in `ds4.h` since earlier releases but not previously bound
 * **libds4 model identity & inspect**: bind `ds4_engine_model_name` / `ds4_engine_model_id` as `Engine.ModelName` / `Engine.ModelID`, and add the `EngineOptions.InspectOnly` field (libds4 upstream commit 04f151d, DeepSeek V4 PRO support). `ds4_context_memory_estimate` now uses the active model shape, so an `Engine.ContextMemoryEstimate` method is provided alongside the package-level function
 * `ds4go prompt --inspect` now prints a `Model: <name> (id=<id>)` line after the libds4 summary, and passes `InspectOnly=true` so the engine open skips full generation-path prep
 * `ds4go-steer` status bar shows the active model name and id; startup also writes a `Loaded model: …` entry to the steer log file
 * **PRO model catalog**: add `pro-imatrix`, `pro`, and `q2-q4-imatrix` entries to the curated installer catalog, SHA256s pinned from Hugging Face `X-Linked-Etag`
 * **fix**: model downloads were returning HTTP 400 from HF's Xet CDN (`cas-bridge.xethub.hf.co`, which serves PRO and now all files in the repo). The CDN rejects open-ended (`Range: bytes=N-` or missing `Range`) GETs on large objects, and refuses any single Range that spans more than roughly 200 GiB. The downloader now always sends a closed Range and chunks fetches at 16 GiB so the 432 GiB PRO file streams successfully; resume from a `.part` file still works at any byte offset
 * **fix**: `cEngineOptions` was missing the `power_percent` field added in libds4 upstream commit 444afce; loading any libds4 built at or after that commit could silently corrupt the `WarmWeights` and `Quality` flags. The struct now matches `ds4.h` exactly
 * **test**: add gated (`-tags ds4_integration`) `TestRealLibraryPowerRoundTrip` that loads a real libds4 and round-trips a `PowerPercent` value — catches `cEngineOptions` ABI drift that mock-based tests cannot detect

## v0.3.0 (2026-05-20)

 * **DSML tool calling**: end-to-end DSML encoder/decoder/dispatch, with an
   incremental `StreamDecoder` for live tool-call streaming and access to
   the final stream tool arguments
 * **Install lifecycle**: `ds4go install` now writes `ds4go-install.json`
   metadata, detects upgrades/replacements, and supports a new `validate`
   subcommand that checks permissions, SHA256, and dynamic `dlopen`
 * **Uninstall command**: `ds4go uninstall` cleanly removes the library,
   `.sha256` sidecar, and metadata; reuses a new shared TUI confirm dialog
 * **Engine logging**: root logging helpers plus a libds4 log callback
   exposed through `ds4api`, with `SetAbortFunc` for fatal-invariant
   callbacks
 * `ds4api` serializes libds4 calls to avoid concurrent-entry hazards
 * CI now runs the full test suite under `-race`

## v0.2.3 (2026-05-17)

 * feat: add Context for cancelation
 * feat: Access to Engine calls are syncronized by mutex, making access thread-safe

## v0.2.0 (2026-05-17)

 * Developer ergonomic, module and package renaming!
 * Securty hardening
 * TUI love
 * GoReleaser-based releases with Homebrew tap (`brew install nimblemarkets/tap/ds4go`)

## v0.1.0 (2026-05-16)

 * Initial release.
