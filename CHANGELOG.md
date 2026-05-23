# `ds4go` CHANGELOG

NOTE: This currently needs a patched `ds4` to make a shared library and route logging and aborts; we also embed the `.metal` files.   See https://github.com/neomantra/ds4/tree/nm-shared

## Unreleased

 * **Quick install script**: `curl -fsSL https://nimblemarkets.github.io/ds4go/install.sh | sh` lands the CLI in `/usr/local/bin` with checksum verification
 * Added `ds4go install --pin` to use custom `ds4` dynamic libraries.
 * Rearraged CLI with `ds4go validate` and `ds4go status`

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