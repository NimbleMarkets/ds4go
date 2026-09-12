package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	ds4 "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

// validatePromptFlags applies the flag-combination rules upstream ds4_cli.c
// checks after parsing.
func validatePromptFlags(cfg *cliopts.CLIConfig) error {
	switch {
	case cfg.PrefixFile != "" && cfg.RawPrompt:
		return errors.New("--prefix-file cannot be combined with --raw-prompt")
	case cfg.PrefixFile != "" && cfg.DumpTokens:
		return errors.New("--dump-tokens does not support --prefix-file")
	case cfg.PerplexityFile != "" && (cfg.Prompt != "" || cfg.PromptFile != ""):
		return errors.New("--perplexity-file does not use -p/--prompt-file")
	case cfg.DecodeConsistency < 0:
		return errors.New("--decode-consistency requires a non-negative token count")
	case cfg.IMatrixMinExpertSamples < 0:
		return errors.New("--imatrix-min-expert-samples must not be negative")
	}
	return nil
}

// collectIMatrix runs imatrix collection with the CLI's limits.
func collectIMatrix(engine *ds4.Engine, cfg *cliopts.CLIConfig) error {
	return engine.CollectIMatrixWithMinExpertSamples(cfg.IMatrixDataset, cfg.IMatrixOut, cfg.Ctx,
		cfg.IMatrixMaxPrompts, cfg.IMatrixMaxTokens, cfg.IMatrixMinExpertSamples)
}

// encodePrompt renders the one-shot prompt as upstream build_prompt does:
// --raw tokenizes the text bare, a --prefix-file assembles the chat turn by
// turn with the prefix ahead of the live user message, and otherwise ds4's
// own chat encoder renders it.
func encodePrompt(engine *ds4.Engine, cfg *cliopts.CLIConfig, promptText string) (*ds4.Tokens, error) {
	if cfg.RawPrompt {
		return engine.TokenizeText(promptText)
	}
	think := cfg.ThinkMode()
	if cfg.PrefixFile == "" {
		return engine.EncodeChatPrompt(cfg.System, promptText, think)
	}
	turns, err := ds4.LoadPromptPrefix(cfg.PrefixFile)
	if err != nil {
		return nil, err
	}
	tokens, err := engine.NewTokens(nil)
	if err != nil {
		return nil, err
	}
	steps := []func() error{
		func() error { return engine.ChatBegin(tokens) },
		func() error {
			if think == ds4.ThinkMax && !engine.IsGLMDSA() {
				return engine.ChatAppendMaxEffortPrefix(tokens)
			}
			return nil
		},
		func() error {
			if cfg.System == "" {
				return nil
			}
			return engine.ChatAppendMessage(tokens, "system", cfg.System)
		},
		func() error { return ds4.AppendPromptPrefix(engine, tokens, turns) },
		func() error { return engine.ChatAppendMessage(tokens, "user", promptText) },
		func() error { return engine.ChatAppendAssistantPrefix(tokens, think) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			tokens.Free()
			return nil, err
		}
	}
	return tokens, nil
}

// prefixHistory turns a --prefix-file into the opening chat history.
func prefixHistory(cfg *cliopts.CLIConfig) ([]cliMessage, error) {
	if cfg.PrefixFile == "" {
		return nil, nil
	}
	turns, err := ds4.LoadPromptPrefix(cfg.PrefixFile)
	if err != nil {
		return nil, err
	}
	history := make([]cliMessage, len(turns))
	for i, turn := range turns {
		history[i] = cliMessage{role: turn.Role, content: turn.Content}
	}
	return history, nil
}

// jsonToken is upstream's json_write_token shape.
type jsonToken struct {
	ID    int    `json:"id"`
	Text  string `json:"text"`
	Bytes []int  `json:"bytes"`
}

func tokenJSON(engine *ds4.Engine, token int) jsonToken {
	text, _ := engine.TokenText(token)
	b := make([]int, len(text))
	for i := 0; i < len(text); i++ {
		b[i] = int(text[i])
	}
	return jsonToken{ID: token, Text: text, Bytes: b}
}

// dumpLogits writes the full next-token logits after the prompt as JSON
// (upstream --dump-logits / run_logits_dump). Non-finite logits are null.
func dumpLogits(engine *ds4.Engine, session *ds4.Session, cfg *cliopts.CLIConfig, prompt *ds4.Tokens) error {
	if err := session.SyncTokens(prompt); err != nil {
		return fmt.Errorf("prompt processing failed: %w", err)
	}
	logits, err := session.CopyLogits()
	if err != nil {
		return fmt.Errorf("failed to copy session logits: %w", err)
	}
	vocab := engine.VocabSize()
	if len(logits) != vocab {
		return fmt.Errorf("failed to copy session logits: got %d of %d", len(logits), vocab)
	}
	argmax := session.Argmax()
	out := make([]*float32, len(logits))
	for i := range logits {
		if v := logits[i]; !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) {
			out[i] = &logits[i]
		}
	}
	doc := struct {
		Source       string     `json:"source"`
		Model        string     `json:"model"`
		Backend      string     `json:"backend"`
		QuantBits    int        `json:"quant_bits"`
		PromptTokens int        `json:"prompt_tokens"`
		Ctx          int        `json:"ctx"`
		Vocab        int        `json:"vocab"`
		ArgmaxToken  jsonToken  `json:"argmax_token"`
		ArgmaxLogit  float32    `json:"argmax_logit"`
		Logits       []*float32 `json:"logits"`
	}{
		Source: "ds4", Model: cfg.Model, Backend: ds4api.BackendName(cfg.SelectBackend()),
		QuantBits: engine.RoutedQuantBits(), PromptTokens: prompt.Len(), Ctx: cfg.Ctx, Vocab: vocab,
		ArgmaxToken: tokenJSON(engine, argmax), ArgmaxLogit: logits[argmax], Logits: out,
	}
	f, err := os.Create(cfg.DumpLogits)
	if err != nil {
		return fmt.Errorf("failed to open --dump-logits file: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// perplexityPrefixTokens seeds the graph with real context before scoring
// starts, matching upstream run_perplexity_file.
const perplexityPrefixTokens = 32

// runPerplexity scores the raw text in --perplexity-file with teacher-forced
// negative log-likelihood and writes upstream's summary line to out.
func runPerplexity(engine *ds4.Engine, cfg *cliopts.CLIConfig, out io.Writer) error {
	data, err := os.ReadFile(cfg.PerplexityFile)
	if err != nil {
		return fmt.Errorf("read --perplexity-file: %w", err)
	}
	tokens, err := engine.TokenizeText(string(data))
	if err != nil {
		return err
	}
	defer tokens.Free()
	ids := tokens.Slice()
	if len(ids) <= perplexityPrefixTokens {
		return fmt.Errorf("--perplexity-file needs more than %d tokens", perplexityPrefixTokens)
	}
	scored := len(ids) - perplexityPrefixTokens
	if cfg.Tokens > 0 && scored > cfg.Tokens {
		scored = cfg.Tokens
	}
	if scored > cfg.Ctx-perplexityPrefixTokens {
		scored = cfg.Ctx - perplexityPrefixTokens
	}
	if scored <= 0 {
		return errors.New("context too small for perplexity scoring")
	}
	session, err := engine.NewSession(cfg.Ctx)
	if err != nil {
		return fmt.Errorf("--perplexity-file requires a graph session backend: %w", err)
	}
	defer session.Close()
	if err := session.Sync(ids[:perplexityPrefixTokens]); err != nil {
		return fmt.Errorf("perplexity initial token failed: %w", err)
	}
	nll := 0.0
	for j := 0; j < scored; j++ {
		i := perplexityPrefixTokens + j
		score, err := session.TokenLogprob(ids[i])
		if err != nil {
			return fmt.Errorf("failed to score token %d: %w", i, err)
		}
		nll -= float64(score.Logprob)
		if (j+1)%256 == 0 || j+1 == scored {
			fmt.Fprintf(os.Stderr, "ds4: perplexity scored %d/%d\r", j+1, scored)
		}
		if j+1 < scored {
			if err := session.Eval(ids[i]); err != nil {
				return fmt.Errorf("perplexity decode failed at token %d: %w", i, err)
			}
		}
	}
	fmt.Fprintln(os.Stderr)
	avg := nll / float64(scored)
	_, err = fmt.Fprintf(out, "tokens=%d scored=%d nll=%.9f avg_nll=%.9f ppl=%.9f\n", len(ids), scored, nll, avg, math.Exp(avg))
	return err
}

func writeDiagTop(w io.Writer, engine *ds4.Engine, label string, scores []ds4.TokenScore) {
	fmt.Fprintf(w, "%s:", label)
	for _, s := range scores {
		if s.ID < 0 {
			break
		}
		tok, _ := json.Marshal(tokenJSON(engine, s.ID))
		fmt.Fprintf(w, " %s@%.6g", tok, s.Logit)
	}
	fmt.Fprintln(w)
}

// runDecodeConsistency decodes N greedy tokens on a live session, then
// prefills the same prefix on a fresh session and compares the two logit
// vectors (upstream --decode-consistency). It fails when the top tokens differ.
func runDecodeConsistency(engine *ds4.Engine, cfg *cliopts.CLIConfig, prompt *ds4.Tokens, out io.Writer) error {
	vocab := engine.VocabSize()
	prefix := append([]int(nil), prompt.Slice()...)

	live, err := engine.NewSession(cfg.Ctx)
	if err != nil {
		return fmt.Errorf("--decode-consistency requires a graph session backend: %w", err)
	}
	if err := live.SyncTokens(prompt); err != nil {
		live.Close()
		return fmt.Errorf("prompt processing failed: %w", err)
	}
	fmt.Fprintf(out, "ds4: decode-consistency prompt_tokens=%d check_after=%d\n", prompt.Len(), cfg.DecodeConsistency)
	for i := 0; i < cfg.DecodeConsistency; i++ {
		token := live.Argmax()
		tok, _ := json.Marshal(tokenJSON(engine, token))
		fmt.Fprintf(out, "ds4: decode-consistency selected[%d]={\"token\":%s}\n", i, tok)
		prefix = append(prefix, token)
		if err := live.Eval(token); err != nil {
			live.Close()
			return fmt.Errorf("decode failed during consistency check: %w", err)
		}
		if engine.TokenIsStop(token) {
			break
		}
	}
	liveTop, err := live.TopLogprobs(10)
	if err != nil {
		live.Close()
		return err
	}
	liveLogits, err := live.CopyLogits()
	live.Close()
	if err != nil || len(liveLogits) != vocab {
		return fmt.Errorf("failed to copy live logits: %v", err)
	}

	fresh, err := engine.NewSession(cfg.Ctx)
	if err != nil {
		return fmt.Errorf("failed to create fresh diagnostic session: %w", err)
	}
	defer fresh.Close()
	if err := fresh.Sync(prefix); err != nil {
		return fmt.Errorf("fresh prompt processing failed: %w", err)
	}
	freshTop, err := fresh.TopLogprobs(10)
	if err != nil {
		return err
	}
	freshLogits, err := fresh.CopyLogits()
	if err != nil || len(freshLogits) != vocab {
		return fmt.Errorf("failed to copy fresh logits: %v", err)
	}

	var ss float64
	var maxAbs float32
	maxI := 0
	for i := 0; i < vocab; i++ {
		d := float32(math.Abs(float64(liveLogits[i] - freshLogits[i])))
		if d > maxAbs {
			maxAbs, maxI = d, i
		}
		ss += float64(d) * float64(d)
	}
	fmt.Fprintf(out, "ds4: decode-consistency compared prefix_tokens=%d vocab=%d max_abs=%.9g at token=%d live=%.9g fresh=%.9g rms=%.9g\n",
		len(prefix), vocab, maxAbs, maxI, liveLogits[maxI], freshLogits[maxI], math.Sqrt(ss/float64(vocab)))
	writeDiagTop(out, engine, "ds4: live_top", liveTop)
	writeDiagTop(out, engine, "ds4: fresh_top", freshTop)
	if len(liveTop) == 0 || len(freshTop) == 0 || liveTop[0].ID != freshTop[0].ID {
		return errors.New("decode-consistency: live and fresh top tokens differ")
	}
	return nil
}
