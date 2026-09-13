package ds4api

import "testing"

// Library.ThinkModeEnabled and Engine.ThinkModeEnabled bind
// ds4_think_mode_enabled on that library, so they answer with no default
// library set; the package-level ThinkModeEnabled keeps consulting the
// default for compatibility.
func TestThinkModeEnabledBindsToTheLibrary(t *testing.T) {
	defaultMu.Lock()
	prev := defaultLib
	defaultMu.Unlock()
	SetDefaultLibrary(nil)
	t.Cleanup(func() { SetDefaultLibrary(prev) })
	t.Setenv("DS4_LIB", "")
	lib := NewMockLibrary()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	for _, tc := range []struct {
		mode ThinkMode
		want bool
	}{
		{ThinkNone, false},
		{ThinkHigh, true},
		{ThinkMax, true},
		{ThinkLevel(0), false},
		{ThinkLevel(1), true},
		{ThinkLevel(100), true},
	} {
		if got := lib.ThinkModeEnabled(tc.mode); got != tc.want {
			t.Errorf("Library.ThinkModeEnabled(%d) = %v, want %v", tc.mode, got, tc.want)
		}
		if got := eng.ThinkModeEnabled(tc.mode); got != tc.want {
			t.Errorf("Engine.ThinkModeEnabled(%d) = %v, want %v", tc.mode, got, tc.want)
		}
	}
	if ThinkModeEnabled(ThinkHigh) {
		t.Error("package ThinkModeEnabled answered true with no default library; the control is broken")
	}
}
