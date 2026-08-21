# `ds4go` CHANGELOG

NOTE: This currently needs a patched `ds4` to make a shared library and route logging and aborts; we also embed the `.metal` files.   See https://github.com/NimbleMarkets/ds4/tree/nm-shared

## Unreleased

 * Add [`workspacetool` package](./workspacetool/README.md) providing local workspace tools for `ds4go.ToolLoop`: `read`, `more`, `list`, `search`, plus opt-in `write`/`edit` and shell job tools (`bash`, `bash_status`, `bash_stop`). Conservative by default: paths are confined to the workspace root, symlink traversal is rejected, and writes and shell require explicit `AllowWrite`/`AllowShell` opt-in, with an optional `Confirm` callback for application-level approval.
 * **GLM 5.2 support**: bind libds4's GLM model-family, stop-token, and reasoning-effort helpers; select prompting, stop detection, assistant/tool replay, and tool syntax from the loaded engine; and add parsing, rendering, canonicalization, and incremental streaming for GLM's `<tool_call>` markup alongside DeepSeek DSML.
 * **GLM tool-call correctness**: restore JSON scalar and container types from registered tool schemas because GLM `<arg_value>` markup carries values as strings; if trailing prose invalidates a streamed GLM tool-call block, consistently degrade the whole completion to raw content without publishing tool events.
 * **GLM model catalog and MTP policy**: add `glm-iq2xxs`, `glm-q2`, and `glm-q4` catalog entries from `antirez/glm-5.2-gguf`. Curated GLM models suppress an external `MTPPath` because their optional next-token predictor is embedded in the base GGUF; DeepSeek models retain their separate MTP support model.
 * **Engine context and placement hints**: expose `EngineOptions.ContextSize`, `PlacementCtxHint`, `PlacementSessionCountHint`, and `ShareSessionPrefillWorkspace`, and map CLI/server `--ctx` to the engine context size and placement-context hint.
 * **Model download safety**: estimate required temporary and final storage and refuse downloads that cannot fit on the destination volume. Download progress now uses the real terminal width in both installer and model TUI paths.
 * **Upstream ABI synchronization**: add `task ds4:sync` / `scripts/check-ds4-sync.sh` to compile the real upstream header and detect stale Go snapshots of byte-offset-sensitive C structs.
 * **DSML parsing hardening** (ported from `ds4` upstream): a bare `<｜DSML｜invoke>` with no `<｜DSML｜tool_calls>` wrapper is now accepted as an implicit single-call block; stray DSML markers in plain assistant output are reported as malformed so the tool loop asks the model to retry; and `invoke`/`parameter` openers require a tag delimiter, so `<｜DSML｜invokeX` no longer false-matches `invoke`.
 * **Structure-aware greedy sampling**: tool turns now sample DSML grammar greedily (argmax) while keeping the configured sampling for parameter values, improving tool-call reliability. It is a no-op at temperature <= 0, so speculative decoding is unaffected. Exposed as `GenerateOptions.SampleControl` and `StreamDecoder.WantsGreedySampling`.

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
