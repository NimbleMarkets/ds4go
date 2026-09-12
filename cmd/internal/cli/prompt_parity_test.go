package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

func mockCLIEngine(t *testing.T) (*ds4.Engine, *ds4api.MockControls) {
	t.Helper()
	lib, ctl := ds4api.NewMockLibraryWithControls()
	eng, err := lib.NewEngine(ds4.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng, ctl
}

func sameTokens(a, b *ds4.Tokens) bool {
	x, y := a.Slice(), b.Slice()
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --raw tokenizes the prompt text with no chat template (upstream build_prompt).
func TestEncodePromptRawSkipsChatTemplate(t *testing.T) {
	eng, _ := mockCLIEngine(t)
	cfg := &cliopts.CLIConfig{RawPrompt: true, System: "sys"}
	got, err := encodePrompt(eng, cfg, "plain words here")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Free()
	want, _ := eng.TokenizeText("plain words here")
	defer want.Free()
	if !sameTokens(got, want) {
		t.Errorf("raw prompt tokens = %v, want the bare tokenization %v", got.Slice(), want.Slice())
	}
}

func TestEncodePromptDefaultMatchesEncodeChatPrompt(t *testing.T) {
	eng, _ := mockCLIEngine(t)
	cfg := &cliopts.CLIConfig{System: "sys"}
	got, err := encodePrompt(eng, cfg, "question")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Free()
	want, _ := eng.EncodeChatPrompt("sys", "question", cfg.ThinkMode())
	defer want.Free()
	if !sameTokens(got, want) {
		t.Errorf("tokens = %v, want %v", got.Slice(), want.Slice())
	}
}

// With --prefix-file the prompt is assembled like upstream build_chat_prompt:
// begin, system, prefix turns, the live user turn, then the assistant prefix.
func TestEncodePromptWithPrefixFile(t *testing.T) {
	eng, _ := mockCLIEngine(t)
	prefix := writeTemp(t, "prefix.txt", "USER: earlier\nASSISTANT: reply\n")
	cfg := &cliopts.CLIConfig{System: "sys", PrefixFile: prefix}
	got, err := encodePrompt(eng, cfg, "now")
	if err != nil {
		t.Fatal(err)
	}
	defer got.Free()

	want, _ := eng.NewTokens(nil)
	defer want.Free()
	_ = eng.ChatBegin(want)
	_ = eng.ChatAppendMessage(want, "system", "sys")
	turns, _ := ds4.LoadPromptPrefix(prefix)
	_ = ds4.AppendPromptPrefix(eng, want, turns)
	_ = eng.ChatAppendMessage(want, "user", "now")
	_ = eng.ChatAppendAssistantPrefix(want, cfg.ThinkMode())
	if !sameTokens(got, want) {
		t.Errorf("tokens = %v, want %v", got.Slice(), want.Slice())
	}
	// ThinkMax adds the max-effort prefix right after begin, as ds4 does.
	cfg.ThinkMax = true
	got2, err := encodePrompt(eng, cfg, "now")
	if err != nil {
		t.Fatal(err)
	}
	defer got2.Free()
	want2, _ := eng.NewTokens(nil)
	defer want2.Free()
	_ = eng.ChatBegin(want2)
	_ = eng.ChatAppendThinkPrefix(want2, ds4.ThinkMax)
	_ = eng.ChatAppendMessage(want2, "system", "sys")
	_ = ds4.AppendPromptPrefix(eng, want2, turns)
	_ = eng.ChatAppendMessage(want2, "user", "now")
	_ = eng.ChatAppendAssistantPrefix(want2, ds4.ThinkMax)
	if !sameTokens(got2, want2) {
		t.Errorf("think-max tokens = %v, want %v", got2.Slice(), want2.Slice())
	}
}

func TestValidatePromptFlagsMirrorsUpstream(t *testing.T) {
	cases := []struct {
		name string
		cfg  cliopts.CLIConfig
		want string
	}{
		{"prefix+raw", cliopts.CLIConfig{PrefixFile: "p", RawPrompt: true}, "--prefix-file cannot be combined with --raw-prompt"},
		{"dump-tokens+prefix", cliopts.CLIConfig{PrefixFile: "p", DumpTokens: true}, "--dump-tokens does not support --prefix-file"},
		{"perplexity+prompt", cliopts.CLIConfig{PerplexityFile: "t", Prompt: "hi"}, "--perplexity-file does not use -p/--prompt-file"},
		{"perplexity+prompt-file", cliopts.CLIConfig{PerplexityFile: "t", PromptFile: "f"}, "--perplexity-file does not use -p/--prompt-file"},
		{"negative consistency", cliopts.CLIConfig{DecodeConsistency: -1}, "--decode-consistency"},
		{"negative imatrix", cliopts.CLIConfig{IMatrixMinExpertSamples: -1}, "--imatrix-min-expert-samples must not be negative"},
	}
	for _, tc := range cases {
		err := validatePromptFlags(&tc.cfg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if err := validatePromptFlags(&cliopts.CLIConfig{PrefixFile: "p", Prompt: "hi", DecodeConsistency: 4}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestIMatrixMinExpertSamplesReachesTheEngine(t *testing.T) {
	eng, ctl := mockCLIEngine(t)
	cfg := &cliopts.CLIConfig{IMatrixDataset: "d.txt", IMatrixOut: "o.dat", Ctx: 4096, IMatrixMaxPrompts: 2, IMatrixMaxTokens: 300, IMatrixMinExpertSamples: 7}
	if err := collectIMatrix(eng, cfg); err != nil {
		t.Fatal(err)
	}
	call := ctl.LastIMatrixCall()
	if call == nil || call.MinExpertSamples != 7 || call.MaxPrompts != 2 || call.MaxTokens != 300 || call.Output != "o.dat" {
		t.Errorf("imatrix call = %+v", call)
	}
}

// --dump-logits writes upstream's JSON shape: metadata, the argmax token, and
// one logit per vocabulary entry.
func TestDumpLogitsWritesFullVocabulary(t *testing.T) {
	eng, _ := mockCLIEngine(t)
	session, err := eng.NewSession(4096)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	prompt, _ := eng.TokenizeText("a b c")
	defer prompt.Free()
	path := filepath.Join(t.TempDir(), "logits.json")
	cfg := &cliopts.CLIConfig{Model: "m.gguf", Ctx: 4096, DumpLogits: path}
	if err := dumpLogits(eng, session, cfg, prompt); err != nil {
		t.Fatalf("dumpLogits: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Source       string `json:"source"`
		Model        string `json:"model"`
		Backend      string `json:"backend"`
		QuantBits    int    `json:"quant_bits"`
		PromptTokens int    `json:"prompt_tokens"`
		Ctx          int    `json:"ctx"`
		Vocab        int    `json:"vocab"`
		ArgmaxToken  struct {
			ID    int    `json:"id"`
			Text  string `json:"text"`
			Bytes []int  `json:"bytes"`
		} `json:"argmax_token"`
		ArgmaxLogit float64    `json:"argmax_logit"`
		Logits      []*float64 `json:"logits"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, data)
	}
	vocab := eng.VocabSize()
	if doc.Source != "ds4" || doc.Model != "m.gguf" || doc.PromptTokens != 3 || doc.Ctx != 4096 || doc.Vocab != vocab || len(doc.Logits) != vocab {
		t.Errorf("header = %+v (logits=%d, vocab=%d)", doc, len(doc.Logits), vocab)
	}
	if doc.ArgmaxToken.ID != session.Argmax() {
		t.Errorf("argmax_token.id = %d, want %d", doc.ArgmaxToken.ID, session.Argmax())
	}
}

func TestRunPerplexityScoresTheText(t *testing.T) {
	eng, _ := mockCLIEngine(t)
	words := make([]string, 40)
	for i := range words {
		words[i] = "w" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	path := writeTemp(t, "text.txt", strings.Join(words, " "))
	cfg := &cliopts.CLIConfig{PerplexityFile: path, Ctx: 4096, Tokens: 50000}
	var out bytes.Buffer
	if err := runPerplexity(eng, cfg, &out); err != nil {
		t.Fatalf("runPerplexity: %v", err)
	}
	// 40 tokens, 32-token prefix, 8 scored at the mock's -0.5 logprob each.
	line := out.String()
	for _, want := range []string{"tokens=40", "scored=8", "nll=4.000000000", "avg_nll=0.500000000", "ppl=1.648721271"} {
		if !strings.Contains(line, want) {
			t.Errorf("output %q lacks %q", line, want)
		}
	}
	// --tokens caps how many are scored, as upstream's n_predict does.
	out.Reset()
	cfg.Tokens = 3
	if err := runPerplexity(eng, cfg, &out); err != nil || !strings.Contains(out.String(), "scored=3") {
		t.Errorf("capped run: err=%v out=%q", err, out.String())
	}
	short := writeTemp(t, "short.txt", "too short")
	if err := runPerplexity(eng, &cliopts.CLIConfig{PerplexityFile: short, Ctx: 4096}, &out); err == nil || !strings.Contains(err.Error(), "more than 32 tokens") {
		t.Errorf("short text: err = %v", err)
	}
}

func TestRunDecodeConsistencyComparesLiveAndFresh(t *testing.T) {
	eng, _ := mockCLIEngine(t)
	prompt, _ := eng.TokenizeText("one two three")
	defer prompt.Free()
	cfg := &cliopts.CLIConfig{Ctx: 4096, DecodeConsistency: 3}
	var out bytes.Buffer
	if err := runDecodeConsistency(eng, cfg, prompt, &out); err != nil {
		t.Fatalf("runDecodeConsistency: %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"decode-consistency prompt_tokens=3 check_after=3", "selected[0]=", "selected[2]=", "compared prefix_tokens=6 vocab=", "live_top:", "fresh_top:"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

// /think [N] mirrors upstream's REPL: bare /think is ThinkHigh; a level needs
// a V4.1 model and 0..100.
func TestParseThinkCommand(t *testing.T) {
	eng, ctl := mockCLIEngine(t)
	if mode, err := parseThinkCommand(eng, ""); err != nil || mode != ds4.ThinkHigh {
		t.Errorf("bare /think = (%d, %v), want ThinkHigh", mode, err)
	}
	if _, err := parseThinkCommand(eng, "25"); err == nil {
		t.Error("/think 25 accepted on a non-V4.1 model")
	}
	ctl.SetDeepSeek41(true)
	if mode, err := parseThinkCommand(eng, "25"); err != nil || mode != ds4.ThinkLevel(25) {
		t.Errorf("/think 25 on V4.1 = (%d, %v), want ThinkLevel(25)", mode, err)
	}
	if _, err := parseThinkCommand(eng, "101"); err == nil {
		t.Error("/think 101 accepted")
	}
}
