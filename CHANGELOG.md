# `ds4go` CHANGELOG

NOTE: This currently needs a patched `ds4` to make a shared library and route logging and aborts; we also embed the `.metal` files.   See https://github.com/neomantra/ds4/tree/nm-shared

## Unreleased

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