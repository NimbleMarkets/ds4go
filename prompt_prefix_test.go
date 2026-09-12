package ds4

import (
	"os"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"path/filepath"
	"strings"
	"testing"
)

// Cases ported from upstream tests/test_prompt_prefix.c.
func TestParsePromptPrefixMultiline(t *testing.T) {
	input := "USER: first line\nsecond line\n USER: this stays in the message\nASSISTANT:\tfirst answer\nsecond answer\n"
	turns, err := ParsePromptPrefix([]byte(input))
	if err != nil {
		t.Fatalf("ParsePromptPrefix: %v", err)
	}
	want := []PromptPrefixTurn{
		{Role: "user", Content: "first line\nsecond line\n USER: this stays in the message"},
		{Role: "assistant", Content: "first answer\nsecond answer"},
	}
	if len(turns) != 2 || turns[0] != want[0] || turns[1] != want[1] {
		t.Errorf("turns = %+v, want %+v", turns, want)
	}
}

func TestParsePromptPrefixPairsAndBOM(t *testing.T) {
	input := "\xef\xbb\xbfUSER: one\r\nASSISTANT: two\r\nUSER: three\nASSISTANT: four"
	turns, err := ParsePromptPrefix([]byte(input))
	if err != nil {
		t.Fatalf("ParsePromptPrefix: %v", err)
	}
	var got []string
	for _, turn := range turns {
		got = append(got, turn.Content)
	}
	if strings.Join(got, ",") != "one,two,three,four" {
		t.Errorf("contents = %v", got)
	}
}

func TestParsePromptPrefixErrors(t *testing.T) {
	cases := map[string]string{
		"":                                    "empty",
		"preamble\nUSER: one\nASSISTANT: two": "line 1",
		"ASSISTANT: one":                      "expected USER",
		"USER: one\nUSER: two":                "expected ASSISTANT",
		"USER: one":                           "must end with an ASSISTANT",
		"USER:\nASSISTANT: two":               "empty USER",
		"USER: one\nASSISTANT:\n":             "empty ASSISTANT",
		"USER: one\x00\nASSISTANT: two":       "NUL",
	}
	for input, want := range cases {
		turns, err := ParsePromptPrefix([]byte(input))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want it to mention %q", input, err, want)
		}
		if turns != nil {
			t.Errorf("%q: turns = %v on error, want nil", input, turns)
		}
	}
}

func TestLoadPromptPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefix.txt")
	if err := os.WriteFile(path, []byte("USER: loaded\nASSISTANT: yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turns, err := LoadPromptPrefix(path)
	if err != nil {
		t.Fatalf("LoadPromptPrefix: %v", err)
	}
	if len(turns) != 2 || turns[0].Content != "loaded" || turns[1].Content != "yes" {
		t.Errorf("turns = %+v", turns)
	}
	// Errors name the file, as upstream's loader does.
	if _, err := LoadPromptPrefix(filepath.Join(t.TempDir(), "missing.txt")); err == nil || !strings.Contains(err.Error(), "missing.txt") {
		t.Errorf("missing file: err = %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.txt")
	_ = os.WriteFile(bad, []byte("ASSISTANT: first"), 0o600)
	if _, err := LoadPromptPrefix(bad); err == nil || !strings.Contains(err.Error(), "bad.txt") || !strings.Contains(err.Error(), "expected USER") {
		t.Errorf("bad file: err = %v", err)
	}
}

// AppendPromptPrefix mirrors upstream's inline ds4_prompt_prefix_append: each
// turn goes through ds4_chat_append_message, and a DeepSeek assistant turn is
// closed with EOS (GLM's template closes its own turns).
func TestAppendPromptPrefixRendersTurns(t *testing.T) {
	turns := []PromptPrefixTurn{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}
	for _, glm := range []bool{false, true} {
		lib, ctl := ds4api.NewMockLibraryWithControls()
		ctl.SetGLM(glm)
		eng, err := lib.NewEngine(EngineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := eng.NewTokens(nil)
		if err := AppendPromptPrefix(eng, got, turns); err != nil {
			t.Fatalf("glm=%v: AppendPromptPrefix: %v", glm, err)
		}
		want, _ := eng.NewTokens(nil)
		_ = eng.ChatAppendMessage(want, "user", "hi")
		_ = eng.ChatAppendMessage(want, "assistant", "hello")
		if !glm {
			want.Push(eng.TokenEOS())
		}
		if g, w := got.Slice(), want.Slice(); len(g) != len(w) || !equalInts(g, w) {
			t.Errorf("glm=%v: tokens = %v, want %v", glm, g, w)
		}
		got.Free()
		want.Free()
		eng.Close()
	}
}

func equalInts(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
