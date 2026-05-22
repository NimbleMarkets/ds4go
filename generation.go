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
	// StopOnEOS stops generation when ds4 emits the engine EOS token.
	StopOnEOS bool
	// ExcludeToken asks argmax generation to skip a specific token id.
	ExcludeToken int
	// OnToken streams generated tokens. Returning normally continues generation.
	OnToken ds4api.TokenEmitFunc
	// Context, when non-nil, can be cancelled to stop generation gracefully
	// before the next token is sampled.
	Context context.Context
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
	if err := g.Session.Sync(prompt); err != nil {
		return nil, err
	}
	return g.Continue(opts)
}

// GenerateTokens synchronizes to prompt and generates tokens from the session.
func (g Generator) GenerateTokens(prompt *ds4api.Tokens, opts GenerateOptions) ([]int, error) {
	if g.Session == nil {
		return nil, errors.New("ds4go: nil session")
	}
	if err := g.Session.SyncTokens(prompt); err != nil {
		return nil, err
	}
	return g.Continue(opts)
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
	eos := -1
	if opts.StopOnEOS && g.Engine != nil {
		eos = g.Engine.TokenEOS()
	}
	var rng = opts.Seed
	out := make([]int, 0, maxTokens)

	// Speculative decoding via MTP is only available in plain argmax mode.
	useSpec := g.Engine != nil && g.Engine.HasMTP() && g.Engine.MTPDraftTokens() > 1 &&
		opts.Temperature <= 0 && opts.ExcludeToken == 0

	i := 0
	for i < maxTokens {
		if opts.Context != nil {
			select {
			case <-opts.Context.Done():
				return out, opts.Context.Err()
			default:
			}
		}
		var token int
		if opts.Temperature > 0 {
			token = g.Session.Sample(opts.Temperature, opts.TopK, opts.TopP, opts.MinP, &rng)
		} else if opts.ExcludeToken != 0 {
			token = g.Session.ArgmaxExcluding(opts.ExcludeToken)
		} else {
			token = g.Session.Argmax()
		}
		if opts.StopOnEOS && token == eos {
			break
		}
		if useSpec {
			draft := maxTokens - i
			if draft > g.Engine.MTPDraftTokens() {
				draft = g.Engine.MTPDraftTokens()
			}
			accepted, err := g.Session.EvalSpeculativeArgmax(token, draft, eos)
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
					if opts.StopOnEOS && t == eos {
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
