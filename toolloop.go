package ds4

import (
	"context"
	"errors"
	"strings"

	"github.com/NimbleMarkets/ds4go/dsml"
)

// errToolTurnComplete cancels generation once the stream decoder has seen
// everything the turn needs: a closed tool-calls block or the end-of-sentence
// marker. It is interpreted as success, not cancellation.
var errToolTurnComplete = errors.New("ds4go: tool turn complete")

// errThinkToolRecovery cancels generation when a complete tool-calls stanza
// opening appears inside a still-unclosed <think> block. The caller's
// recovery callback force-feeds "</think>\n\n" into the session and
// generation resumes, letting the model restart the call on the executable
// side (upstream ds4's think-tool recovery).
var errThinkToolRecovery = errors.New("ds4go: tool call inside unclosed thinking")

// streamCompletion runs generate, feeding emitted token text through a
// dsml.StreamDecoder, and returns the accumulated completion text. Once the
// decoder reports a closed tool block or an end-of-sentence marker the
// generation context is cancelled: past that point the model can only emit
// whitespace and the end-of-sentence marker, so stopping saves the trailing
// tokens. Further emits are suppressed so tokens already in flight (e.g. an
// accepted speculative batch) cannot degrade the closed block.
func streamCompletion(parent context.Context, thinking bool, onEvent func(dsml.StreamEvent), generate func(ctx context.Context, emit func(string), wantGreedy func() bool) error, thinkRecover func() (string, error)) (string, error) {
	if parent == nil {
		parent = context.Background()
	}

	dec := dsml.NewStreamDecoder(thinking)
	forward := func(events []dsml.StreamEvent) {
		if onEvent == nil {
			return
		}
		for _, ev := range events {
			onEvent(ev)
		}
	}

	var text strings.Builder
	var roundCancel context.CancelCauseFunc
	stopped := false
	emit := func(part string) {
		if stopped {
			return
		}
		text.WriteString(part)
		forward(dec.Write(part))
		if dec.ToolBlockClosed() || dec.Done() {
			stopped = true
			roundCancel(errToolTurnComplete)
			return
		}
		// Recovery keeps recording: tokens still in flight after the cancel
		// are sampled reasoning and belong in the turn.
		if thinkRecover != nil && dec.ToolStanzaInThinking() {
			roundCancel(errThinkToolRecovery)
		}
	}

	for {
		ctx, cancel := context.WithCancelCause(parent)
		roundCancel = cancel
		err := generate(ctx, emit, dec.WantsGreedySampling)
		cause := context.Cause(ctx)
		cancel(nil)
		if err == nil || (errors.Is(cause, errToolTurnComplete) && parent.Err() == nil) {
			break
		}
		if errors.Is(cause, errThinkToolRecovery) && parent.Err() == nil {
			// In-flight tokens may have closed the thinking block on their
			// own; only inject while the stanza is still inside it. An empty
			// injection means there was no budget — resume and let the
			// parse-time fallback deal with the turn.
			if dec.ToolStanzaInThinking() {
				injected, rerr := thinkRecover()
				if rerr != nil {
					return "", rerr
				}
				if injected != "" {
					text.WriteString(injected)
					forward(dec.Write(injected))
				}
			}
			continue
		}
		return "", err
	}
	events, _, _ := dec.Close()
	forward(events)
	return text.String(), nil
}

// ToolLoop drives multi-turn tool calling on top of Generator and ToolRegistry.
type ToolLoop struct {
	// Engine owns token decoding and prompt rendering.
	Engine *Engine
	// Session is the live ds4 session used for generation.
	Session *Session
	// Tools stores the tool schemas, handlers, and replay state.
	Tools *ToolRegistry
	// ThinkMode controls ds4's assistant prefix rendering.
	ThinkMode ThinkMode
	// Thinking tells ParseAssistant whether to require and extract a reasoning block.
	Thinking bool
	// DisableThinkRecovery turns off the live recovery for tool calls started
	// inside an unclosed <think> block. When recovery is active (the default
	// in thinking mode), a complete stanza opening inside thinking force-feeds
	// "</think>\n\n" into the session and generation resumes, letting the
	// model restart the call on the executable side instead of running to the
	// token limit with the call dropped at parse time.
	DisableThinkRecovery bool
	// CompleteFunc overrides the default generator-backed completion path.
	// When nil, Run uses Generator.GenerateTokens and Engine.TokenText.
	CompleteFunc func(prompt *Tokens, opts GenerateOptions) (string, error)
}

// ToolLoopOptions configures one tool loop run.
type ToolLoopOptions struct {
	// System is the system prompt content.
	System string
	// History is the existing chat transcript excluding the generated assistant turn.
	History []ChatMessage
	// Generate controls model generation for each assistant turn.
	Generate GenerateOptions
	// MaxRounds bounds the total number of assistant turns — tool-calling
	// rounds, malformed-tool-call retry turns, and the final answer. Values
	// <= 0 default to 8.
	MaxRounds int
	// OnStreamEvent, when non-nil, receives live stream events (reasoning,
	// content, and tool-call deltas) during each assistant turn. Tool-call
	// events are delivered once the enclosing block is validated. It is not
	// called when CompleteFunc overrides the generation path.
	OnStreamEvent func(dsml.StreamEvent)
}

// ToolLoopResult is the final result of one tool loop run.
type ToolLoopResult struct {
	// History is the full updated transcript, including the final assistant turn.
	History []ChatMessage
	// Assistant is the final assistant message with no further tool calls.
	Assistant ChatMessage
	// ToolRounds is the number of assistant turns that requested tools.
	ToolRounds int
}

// Run executes assistant generation, dispatches requested tools, and continues
// until the assistant returns a plain answer or MaxRounds is reached. A
// cancelled GenerateOptions.Context, a tool handler error, or a call to an
// unregistered tool aborts the run and is returned as an error.
func (l ToolLoop) Run(opts ToolLoopOptions) (ToolLoopResult, error) {
	if l.Engine == nil {
		return ToolLoopResult{}, errors.New("ds4go: nil engine")
	}
	if l.Session == nil {
		return ToolLoopResult{}, errors.New("ds4go: nil session")
	}
	if l.Tools == nil {
		return ToolLoopResult{}, errors.New("ds4go: nil tool registry")
	}
	maxRounds := opts.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 8
	}
	history := append([]ChatMessage(nil), opts.History...)
	ctx := opts.Generate.Context
	if ctx == nil {
		ctx = context.Background()
	}
	toolRounds := 0
	syntaxRetried := false
	for attempts := 0; ; attempts++ {
		if err := ctx.Err(); err != nil {
			return ToolLoopResult{}, err
		}
		prompt, err := l.Tools.BuildPrompt(l.Engine, opts.System, history, l.effectiveThinkMode())
		if err != nil {
			return ToolLoopResult{}, err
		}
		text, err := l.CompleteTurn(prompt, opts.Generate, opts.OnStreamEvent)
		prompt.Free()
		if err != nil {
			return ToolLoopResult{}, err
		}
		assistant, err := l.Tools.ParseAssistant(text, l.Thinking)
		if err != nil {
			return ToolLoopResult{}, err
		}
		history = append(history, assistant)
		if len(assistant.ToolCalls) == 0 {
			// A turn that degraded to content because its DSML could not be
			// parsed gets one consecutive retry: the parse failure goes back
			// to the model as a tool error so it can correct its syntax or
			// answer normally. After a second consecutive failure (or with no
			// round budget left) the raw text is returned as the answer.
			if assistant.MalformedReason != "" && !syntaxRetried && attempts < maxRounds-1 {
				syntaxRetried = true
				history = append(history, ChatMessage{
					Role:    "tool",
					Content: dsml.ToolSyntaxErrorMessage(assistant.MalformedReason),
				})
				continue
			}
			return ToolLoopResult{
				History:    history,
				Assistant:  assistant,
				ToolRounds: toolRounds,
			}, nil
		}
		syntaxRetried = false
		if attempts >= maxRounds-1 {
			return ToolLoopResult{}, errors.New("ds4go: tool loop exceeded maximum rounds")
		}
		results, err := l.Tools.ExecuteToolCalls(ctx, assistant.ToolCalls)
		if err != nil {
			return ToolLoopResult{}, err
		}
		history = append(history, results...)
		toolRounds++
	}
}

// CompleteTurn runs one streaming assistant turn: generation is fed through
// a dsml.StreamDecoder, stops early once the tool block closes or the
// end-of-sentence marker appears, applies live think-tool recovery (unless
// DisableThinkRecovery), and forwards live stream events to onEvent (which
// may be nil). It returns the accumulated completion text. CompleteFunc,
// when set, overrides the generation path exactly as in Run.
//
// This is the single-turn building block behind Run, exported for hosts
// that drive their own tool loop (custom execution, per-round UI events)
// but want the library's stream-driven recovery behaviors.
func (l ToolLoop) CompleteTurn(prompt *Tokens, opts GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error) {
	return l.complete(prompt, opts, onEvent)
}

func (l ToolLoop) effectiveThinkMode() ThinkMode {
	if l.ThinkMode == 0 && !l.Thinking {
		return ThinkNone
	}
	if l.ThinkMode == 0 {
		return ThinkHigh
	}
	return l.ThinkMode
}

func (l ToolLoop) complete(prompt *Tokens, opts GenerateOptions, onEvent func(dsml.StreamEvent)) (string, error) {
	if l.CompleteFunc != nil {
		return l.CompleteFunc(prompt, opts)
	}
	// remaining is the turn's token budget shared across the initial
	// generation, any think-recovery injection, and resumed generation,
	// mirroring Generator.Continue's MaxTokens default.
	remaining := opts.MaxTokens
	if remaining <= 0 {
		remaining = 128
	}
	started := false
	gen := func(ctx context.Context, emit func(string), wantGreedy func() bool) error {
		if remaining <= 0 {
			return nil
		}
		generate := opts
		generate.Context = ctx
		generate.MaxTokens = remaining
		generate.SampleControl = func() bool {
			dsmlGreedy := wantGreedy()
			callerGreedy := opts.SampleControl != nil && opts.SampleControl()
			return dsmlGreedy || callerGreedy
		}
		generate.OnToken = func(token int) {
			remaining--
			if part, err := l.Engine.TokenText(token); err == nil {
				emit(part)
			}
			if opts.OnToken != nil {
				opts.OnToken(token)
			}
		}
		g := Generator{Engine: l.Engine, Session: l.Session}
		if !started {
			started = true
			_, err := g.GenerateTokens(prompt, generate)
			return err
		}
		// Resumption after a think-recovery injection continues from the
		// session's current logits; re-syncing the prompt would rewind the
		// tokens generated so far.
		_, err := g.Continue(generate)
		return err
	}

	var thinkRecover func() (string, error)
	if l.Thinking && !l.DisableThinkRecovery {
		thinkRecover = func() (string, error) {
			const inject = "</think>\n\n"
			toks, err := l.Engine.TokenizeRenderedChat(inject)
			if err != nil {
				return "", err
			}
			defer toks.Free()
			ids := toks.Slice()
			room := l.Session.Ctx() - l.Session.Pos()
			if len(ids) == 0 || len(ids) >= remaining || len(ids) >= room {
				// Not enough budget to recover; leave the stream as generated
				// and let the parse-time fallback deal with it.
				return "", nil
			}
			for _, id := range ids {
				if err := l.Session.Eval(id); err != nil {
					return "", err
				}
			}
			remaining -= len(ids)
			return inject, nil
		}
	}
	return streamCompletion(opts.Context, l.Thinking, onEvent, gen, thinkRecover)
}
