package ds4api

import "testing"

// CurrentDefaultLibrary must report the installed default without triggering
// the lazy load that DefaultLibrary performs.
func TestCurrentDefaultLibraryPeeksWithoutLoading(t *testing.T) {
	prev := CurrentDefaultLibrary()
	t.Cleanup(func() { SetDefaultLibrary(prev) })
	SetDefaultLibrary(nil)
	if got := CurrentDefaultLibrary(); got != nil {
		t.Fatalf("CurrentDefaultLibrary with no default = %v, want nil", got)
	}
	lib := NewMockLibrary()
	SetDefaultLibrary(lib)
	if got := CurrentDefaultLibrary(); got != lib {
		t.Errorf("CurrentDefaultLibrary = %v, want the installed mock", got)
	}
}
