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

// streamCompletion runs generate, feeding emitted token text through a
// dsml.StreamDecoder, and returns the accumulated completion text. Once the
// decoder reports a closed tool block or an end-of-sentence marker the
// generation context is cancelled: past that point the model can only emit
// whitespace and the end-of-sentence marker, so stopping saves the trailing
// tokens. Further emits are suppressed so tokens already in flight (e.g. an
// accepted speculative batch) cannot degrade the closed block.
func streamCompletion(parent context.Context, thinking bool, onEvent func(dsml.StreamEvent), generate func(context.Context, func(string)) error) (string, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)

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
	stopped := false
	emit := func(part string) {
		if stopped {
			return
		}
		text.WriteString(part)
		forward(dec.Write(part))
		if dec.ToolBlockClosed() || dec.Done() {
			stopped = true
			cancel(errToolTurnComplete)
		}
	}

	err := generate(ctx, emit)
	if err != nil && (!errors.Is(context.Cause(ctx), errToolTurnComplete) || parent.Err() != nil) {
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
		text, err := l.complete(prompt, opts.Generate, opts.OnStreamEvent)
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
	return streamCompletion(opts.Context, l.Thinking, onEvent, func(ctx context.Context, emit func(string)) error {
		generate := opts
		generate.Context = ctx
		generate.OnToken = func(token int) {
			if part, err := l.Engine.TokenText(token); err == nil {
				emit(part)
			}
			if opts.OnToken != nil {
				opts.OnToken(token)
			}
		}
		_, err := (Generator{Engine: l.Engine, Session: l.Session}).GenerateTokens(prompt, generate)
		return err
	})
}
