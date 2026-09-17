package ds4api

import (
	"strings"
	"testing"
)

// ThinkLow and ThinkMedium are upstream's DS4_THINK_LOW and DS4_THINK_MEDIUM,
// appended after MAX so the older constants keep their numbers. They are
// named modes (no level), enabled like HIGH, and each family renders them
// its own way: GLM as its High line, V4.1 as no effort line at all, Qwen as
// the low instruction or nothing for medium.
func TestThinkLowAndMediumMirrorUpstream(t *testing.T) {
	if ThinkLow != 3 || ThinkMedium != 4 {
		t.Fatalf("ThinkLow, ThinkMedium = %d, %d, want 3, 4 (appended after ThinkMax)", ThinkLow, ThinkMedium)
	}
	lib, ctl := NewMockLibraryWithControls()
	eng, err := lib.NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	for _, mode := range []ThinkMode{ThinkLow, ThinkMedium} {
		if mode.Level() != -1 {
			t.Errorf("%d.Level() = %d, want -1 for a named mode", mode, mode.Level())
		}
		if !lib.ThinkModeEnabled(mode) {
			t.Errorf("ThinkModeEnabled(%d) = false, want true", mode)
		}
		if got := eng.GLMReasoningEffortText(mode); got != "Reasoning Effort: High" {
			t.Errorf("GLM effort for %d = %q, want the High line", mode, got)
		}
		if got := eng.DeepSeek41ReasoningEffortText(mode); got != "" {
			t.Errorf("V4.1 effort for %d = %q, want none", mode, got)
		}
	}
	if got := lib.raw.ds4ThinkModeName(ThinkLow); got != "low" {
		t.Errorf("name of ThinkLow = %q, want low", got)
	}
	if got := lib.raw.ds4ThinkModeName(ThinkMedium); got != "medium" {
		t.Errorf("name of ThinkMedium = %q, want medium", got)
	}

	// Qwen3.8 Flash Next: optional bindings, like the V4.1 group.
	if !lib.SupportsQwen4() {
		t.Fatal("mock library does not report SupportsQwen4")
	}
	if eng.IsQwen4() {
		t.Fatal("fresh mock engine reports Qwen4")
	}
	ctl.SetQwen4(true)
	if !eng.IsQwen4() {
		t.Fatal("IsQwen4() = false after SetQwen4(true)")
	}
	for _, tc := range []struct {
		mode ThinkMode
		want string
	}{
		{ThinkHigh, "Reasoning effort is set to xhigh."},
		{ThinkMax, "Reasoning effort is set to xhigh."},
		{ThinkLow, "Reasoning effort is set to low."},
		{ThinkMedium, ""},
		{ThinkNone, ""},
	} {
		got := eng.Qwen4ReasoningEffortText(tc.mode)
		if tc.want == "" && got != "" || tc.want != "" && !strings.HasPrefix(got, tc.want) {
			t.Errorf("Qwen4ReasoningEffortText(%d) = %q, want prefix %q", tc.mode, got, tc.want)
		}
	}
}
