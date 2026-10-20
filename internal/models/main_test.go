package models

import (
	"os"
	"testing"
)

// TestMain makes the free-space check hermetic. Download tests stage tiny
// payloads but the check sizes a download from the catalog (tens to hundreds
// of GiB) whenever the mock server sends no X-Linked-Size, so on a CI runner
// with a small /tmp they failed with "not enough free space". Tests that
// exercise the real syscall wiring call availableBytes directly, and the
// walk-up test restores it with withAvailableBytes.
func TestMain(m *testing.M) {
	availableBytesFunc = func(string) (uint64, error) { return 1 << 60, nil }
	os.Exit(m.Run())
}
