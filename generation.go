package ds4

import (
	"context"
	"errors"
	"strings"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// ErrContextFull is returned when a session has no room left in its context
// window. When generation is capped by the remaining room, it is returned
// alongside the tokens produced before the limit was reached.
var ErrContextFull = errors.New("ds4go: session context full")

// GenerateOptions controls Go-native session generation helpers.
type GenerateOptions struct {
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens int
	// Temperature controls sampling. Values <= 0 use argmax.
	Temperature float32
	// TopK limits sampling to the best k tokens when Temperature > 0.
	TopK int
	// TopP applies nucleus sampling when Temperature > 0.
	TopP float32
	// MinP applies minimum probability sampling when Temperature > 0.
	MinP float32
	// Seed seeds ds4's sampler. A zero seed is valid and deterministic.
	Seed uint64
	// StopOnEOS stops generation when ds4 emits a generation stop token. For
	// DeepSeek shapes that is the engine EOS token; GLM DSA shapes also stop on
	// the system, user, assistant, and observation role tokens.
	StopOnEOS bool
	// ThinkMode refines stop detection when StopOnEOS is set: with thinking
	// disabled, a <think> or </think> marker is a protocol control token rather
	// than assistant content and ends the completion. It does not otherwise
	// affect generation; prompt rendering takes its own think mode.
	ThinkMode ThinkMode
	// ExcludeToken asks argmax generation to skip a specific token id.
	ExcludeToken int
	// OnToken streams generated tokens. Returning normally continues generation.
	OnToken ds4api.TokenEmitFunc
	// Context, when non-nil, can be cancelled to stop generation gracefully
	// before the next token is sampled.
	Context context.Context
	// SampleControl, when non-nil, is consulted before each token is sampled.
	// Returning true forces argmax (greedy) sampling for that token regardless
	// of Temperature; configured Temperature>0 sampling applies otherwise. It is
	// a no-op when Temperature<=0 (already argmax). Speculative decoding honours
	// it too: a token it forces greedy is verified by the argmax speculative
	// path, so markup structure is never sampled through.
	SampleControl func() bool
}

// Generator binds a ds4 engine and session for Go-native generation helpers.
type Generator struct {
	Engine  *ds4api.Engine
	Session *ds4api.Session
}

// Generate synchronizes to prompt and generates tokens from the session.
func (g Generator) Generate(prompt []int, opts GenerateOptions) ([]int, error) {
	if g.Session == nil {
		return nil, errors.New("ds4go: nil session")
	}
	if err := syncWithContext(func() error { return g.Session.Sync(prompt) }, func(fn ds4api.CancelFunc) error {
		return g.Session.SyncWithCancel(prompt, fn)
	}, opts.Context); err != nil {
		return nil, err
	}
	return g.Continue(opts)
}

// GenerateTokens synchronizes to prompt and generates tokens from the session.
func (g Generator) GenerateTokens(prompt *ds4api.Tokens, opts GenerateOptions) ([]int, error) {
	if g.Session == nil {
		return nil, errors.New("ds4go: nil session")
	}
	if err := syncWithContext(func() error { return g.Session.SyncTokens(prompt) }, func(fn ds4api.CancelFunc) error {
		return g.Session.SyncTokensWithCancel(prompt, fn)
	}, opts.Context); err != nil {
		return nil, err
	}
	return g.Continue(opts)
}

func syncWithContext(sync func() error, syncCancel func(ds4api.CancelFunc) error, ctx context.Context) error {
	if ctx == nil {
		return sync()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := syncCancel(func() bool { return ctx.Err() != nil })
	if errors.Is(err, ds4api.ErrSessionSyncInterrupted) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	if errors.Is(err, ds4api.ErrCancelNotSupported) {
		return sync()
	}
	return err
}

// shouldSampleGreedy reports whether the next token must be argmax-sampled
// rather than drawn at the configured temperature: either Temperature is
// already non-positive, or SampleControl requests greedy for this token.
func shouldSampleGreedy(opts GenerateOptions) bool {
	if opts.Temperature <= 0 {
		return true
	}
	return opts.SampleControl != nil && opts.SampleControl()
}

// Continue generates tokens from the current session logits.
func (g Generator) Continue(opts GenerateOptions) ([]int, error) {
	if g.Session == nil {
		return nil, errors.New("ds4go: nil session")
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 128
	}
	room := g.Session.Ctx() - g.Session.Pos()
	var capped bool
	if room <= 1 {
		return nil, ErrContextFull
	} else if maxTokens > room-1 {
		maxTokens = room - 1
		capped = true
	}
	// Stop detection goes through the engine predicate rather than an EOS
	// comparison: GLM DSA also ends a turn on the role tokens, and with
	// thinking disabled on a stray thinking marker. eos is still needed
	// separately for the speculative path, which takes a single token id.
	eos := -1
	isStop := func(int) bool { return false }
	if opts.StopOnEOS && g.Engine != nil {
		eos = g.Engine.TokenEOS()
		isStop = func(token int) bool {
			return g.Engine.TokenIsStopForThinkMode(token, opts.ThinkMode)
		}
	}
	var rng = opts.Seed
	out := make([]int, 0, maxTokens)

	// Speculative decoding via MTP. At positive temperature it needs the sampled
	// entry point: verifying a sampled run with the argmax one would quietly
	// ignore the sampling parameters, so an older libds4 falls back to ordinary
	// token-at-a-time decoding instead.
	useSpec := g.Engine != nil && g.Engine.HasMTP() && g.Engine.MTPDraftTokens() > 1 &&
		opts.ExcludeToken == 0 &&
		(opts.Temperature <= 0 || g.Engine.SupportsSampledSpeculative())

	i := 0
	for i < maxTokens {
		if opts.Context != nil {
			select {
			case <-opts.Context.Done():
				return out, opts.Context.Err()
			default:
			}
		}
		// Consulted once per token: SampleControl drives both how this token is
		// drawn and how a speculative block verifies it.
		greedy := shouldSampleGreedy(opts)
		var token int
		if !greedy {
			token = g.Session.Sample(opts.Temperature, opts.TopK, opts.TopP, opts.MinP, &rng)
		} else if opts.ExcludeToken != 0 {
			token = g.Session.ArgmaxExcluding(opts.ExcludeToken)
		} else {
			token = g.Session.Argmax()
		}
		if isStop(token) {
			break
		}
		if useSpec {
			draft := maxTokens - i
			if draft > g.Engine.MTPDraftTokens() {
				draft = g.Engine.MTPDraftTokens()
			}
			var accepted []int
			var err error
			if greedy {
				accepted, err = g.Session.EvalSpeculativeArgmax(token, draft, eos)
			} else {
				accepted, err = g.Session.EvalSpeculative(token, draft, eos, ds4api.SpeculativeOptions{
					Temperature: opts.Temperature,
					TopK:        opts.TopK,
					TopP:        opts.TopP,
					MinP:        opts.MinP,
					RNG:         &rng,
				})
			}
			if err != nil {
				return out, err
			}
			if len(accepted) == 0 {
				// Speculative decoding rejected everything; fall back to
				// evaluating the argmax token normally.
				out = append(out, token)
				if opts.OnToken != nil {
					opts.OnToken(token)
				}
				if err := g.Session.Eval(token); err != nil {
					return out, err
				}
				i++
			} else {
				for _, t := range accepted {
					if isStop(t) {
						return out, nil
					}
					out = append(out, t)
					if opts.OnToken != nil {
						opts.OnToken(t)
					}
					i++
				}
			}
		} else {
			out = append(out, token)
			if opts.OnToken != nil {
				opts.OnToken(token)
			}
			if err := g.Session.Eval(token); err != nil {
				return out, err
			}
			i++
		}
	}
	if capped && i >= maxTokens {
		return out, ErrContextFull
	}
	return out, nil
}

// GenerateString tokenizes prompt, generates, and decodes the generated text.
func (g Generator) GenerateString(prompt string, opts GenerateOptions) (string, error) {
	if g.Engine == nil {
		return "", errors.New("ds4go: nil engine")
	}
	tokens, err := g.Engine.TokenizeText(prompt)
	if err != nil {
		return "", err
	}
	defer tokens.Free()
	generated, err := g.GenerateTokens(tokens, opts)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, token := range generated {
		part, err := g.Engine.TokenText(token)
		if err != nil {
			return text.String(), err
		}
		text.WriteString(part)
	}
	return text.String(), nil
}
