package ds4

import (
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// The ds4api default is the one authoritative default library. A library
// installed through the low-level setter (as internal/install does after a
// validation load) must be visible to the root package's callback helpers;
// with a second root-owned copy they could diverge.
func TestLowLevelDefaultIsVisibleToRootCallbacks(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	ds4api.SetDefaultLibrary(lib)
	t.Cleanup(func() { ds4api.SetDefaultLibrary(nil) })

	got, err := defaultCallbackLibrary(false)
	if err != nil {
		t.Fatalf("defaultCallbackLibrary: %v", err)
	}
	if got != lib {
		t.Errorf("defaultCallbackLibrary = %v, want the ds4api default", got)
	}
}

// With no default installed and load=false, the helper stays a no-op and
// must not lazily load a library.
func TestCallbackLibraryWithoutDefaultIsNil(t *testing.T) {
	ds4api.SetDefaultLibrary(nil)
	got, err := defaultCallbackLibrary(false)
	if err != nil {
		t.Fatalf("defaultCallbackLibrary: %v", err)
	}
	if got != nil {
		t.Errorf("defaultCallbackLibrary with no default = %v, want nil", got)
	}
}
