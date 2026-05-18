package ds4

import (
	"context"
	"errors"
	"strings"
)

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
	// rounds plus the final answer. Values <= 0 default to 8.
	MaxRounds int
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
	for rounds := 0; ; rounds++ {
		if err := ctx.Err(); err != nil {
			return ToolLoopResult{}, err
		}
		prompt, err := l.Tools.BuildPrompt(l.Engine, opts.System, history, l.effectiveThinkMode())
		if err != nil {
			return ToolLoopResult{}, err
		}
		text, err := l.complete(prompt, opts.Generate)
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
			return ToolLoopResult{
				History:    history,
				Assistant:  assistant,
				ToolRounds: rounds,
			}, nil
		}
		if rounds >= maxRounds-1 {
			return ToolLoopResult{}, errors.New("ds4go: tool loop exceeded maximum rounds")
		}
		results, err := l.Tools.ExecuteToolCalls(ctx, assistant.ToolCalls)
		if err != nil {
			return ToolLoopResult{}, err
		}
		history = append(history, results...)
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

func (l ToolLoop) complete(prompt *Tokens, opts GenerateOptions) (string, error) {
	if l.CompleteFunc != nil {
		return l.CompleteFunc(prompt, opts)
	}
	var text strings.Builder
	generate := opts
	generate.OnToken = func(token int) {
		if part, err := l.Engine.TokenText(token); err == nil {
			text.WriteString(part)
		}
		if opts.OnToken != nil {
			opts.OnToken(token)
		}
	}
	_, err := (Generator{Engine: l.Engine, Session: l.Session}).GenerateTokens(prompt, generate)
	if err != nil {
		return "", err
	}
	return text.String(), nil
}
