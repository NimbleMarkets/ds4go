package ds4

import (
	"testing"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// Whether a prompt replays assistant reasoning is decided by the engine that
// renders it, not by the process-wide default library: a caller who loaded
// libds4 explicitly and never set a default must still get reasoning back in
// the replayed turn under a thinking mode, and V4.1 levels follow the same
// rule with level 0 meaning off.
func TestBuildChatPromptReplaysReasoningWithoutADefaultLibrary(t *testing.T) {
	defaultLibraryMu.Lock()
	prev := defaultLibrary
	defaultLibraryMu.Unlock()
	SetDefaultLibrary(nil)
	t.Cleanup(func() { SetDefaultLibrary(prev) })
	t.Setenv("DS4_LIB", "")
	eng := mockEngine(t)
	defer eng.Close()
	// The mock tokenizes by whitespace, so the reasoning is padded to land
	// in a token of its own inside the rendered assistant turn.
	history := []ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "a", ReasoningContent: " REPLAYEDREASONING "},
	}
	ids := renderedTokensRole(t, eng, "", "REPLAYEDREASONING")
	marker := ids[len(ids)-1]
	has := func(think ThinkMode) bool {
		t.Helper()
		toks, err := BuildChatPrompt(eng, "", nil, history, think)
		if err != nil {
			t.Fatal(err)
		}
		defer toks.Free()
		for _, id := range toks.Slice() {
			if id == marker {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		think ThinkMode
		want  bool
	}{
		{ThinkHigh, true},
		{ThinkMax, true},
		{ThinkLevel(7), true},
		{ThinkNone, false},
		{ThinkLevel(0), false},
	} {
		if got := has(tc.think); got != tc.want {
			t.Errorf("think %d: reasoning replayed = %v, want %v", tc.think, got, tc.want)
		}
	}
	if _, err := ds4api.DefaultLibrary(); err == nil {
		t.Fatal("control: a default library resolved, so the test did not exercise the explicit-library path")
	}
}
