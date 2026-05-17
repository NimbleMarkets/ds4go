# `ds4go` CHANGELOG

NOTE: This currently needs a patched `ds4` to make a shared library; we also embed the `.metal` files.   See https://github.com/neomantra/ds4/tree/nm-shared

## v0.2.0 (2026-05-17)

 * Module and package renaming!
 * TUI love
 * GoReleaser-based releases with Homebrew tap (`brew install nimblemarkets/tap/ds4go`)
 * `model download` takes an exclusive lock to prevent concurrent downloads colliding
 * `model delete` removes a downloaded model (with confirmation; `-y` to skip)

## v0.1.0 (2026-05-16)

 * Initial release.