package ds4

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/dsml"
)

func TestToolLoopRun(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	reg := NewToolRegistry()
	if err := reg.RegisterFunc(ToolSchema{
		Name:        "add",
		Description: "Add two numbers",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "5", nil
	}); err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}

	first, err := dsml.RenderToolCalls([]dsml.ToolCall{{
		Name:      "add",
		Arguments: `{"a":2,"b":3}`,
	}})
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}

	callCount := 0
	loop := ToolLoop{
		Engine:   eng,
		Session:  sess,
		Tools:    reg,
		Thinking: false,
		CompleteFunc: func(prompt *Tokens, opts GenerateOptions) (string, error) {
			callCount++
			if callCount == 1 {
				return "let me calculate" + first, nil
			}
			return "the answer is 5", nil
		},
	}

	result, err := loop.Run(ToolLoopOptions{
		System: "you can use tools",
		History: []ChatMessage{{
			Role:    "user",
			Content: "what is 2 + 3?",
		}},
		MaxRounds: 2,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Assistant.Content != "the answer is 5" {
		t.Fatalf("Assistant.Content = %q", result.Assistant.Content)
	}
	if result.ToolRounds != 1 {
		t.Fatalf("ToolRounds = %d, want 1", result.ToolRounds)
	}
	if len(result.History) != 4 {
		t.Fatalf("expected 4 history messages, got %d", len(result.History))
	}
	if result.History[2].Role != "tool" || result.History[2].Content != "5" {
		t.Fatalf("unexpected tool result message: %#v", result.History[2])
	}
}

func TestStreamCompletionStopsAtToolBlockClose(t *testing.T) {
	blockParts := []string{
		"checking", "\n\n", "<｜DSML｜tool_calls>", "\n",
		"<｜DSML｜invoke name=\"add\">", "\n",
		"<｜DSML｜parameter name=\"a\" string=\"false\">", "2", "</｜DSML｜parameter>", "\n",
		"</｜DSML｜invoke>", "\n",
		"</｜DSML｜tool_calls>",
	}
	junkParts := []string{"<trailing>", "junk the model would have produced"}

	var events []dsml.StreamEvent
	fed := 0
	text, err := streamCompletion(context.Background(), false,
		func(ev dsml.StreamEvent) { events = append(events, ev) },
		func(ctx context.Context, emit func(string), _ func() bool) error {
			// Mirrors Generator.Continue: check the context before each
			// token, return its error when cancelled.
			for _, p := range append(append([]string(nil), blockParts...), junkParts...) {
				if err := ctx.Err(); err != nil {
					return err
				}
				emit(p)
				fed++
			}
			return nil
		}, nil)
	if err != nil {
		t.Fatalf("streamCompletion: %v", err)
	}
	if fed != len(blockParts) {
		t.Fatalf("fed %d parts, want generation stopped after the %d block parts", fed, len(blockParts))
	}
	want := ""
	for _, p := range blockParts {
		want += p
	}
	if text != want {
		t.Fatalf("text = %q, want the completion cut at the block close %q", text, want)
	}

	var started, ended bool
	for _, ev := range events {
		switch ev.Type {
		case dsml.EventToolCallStart:
			started = ev.Name == "add"
		case dsml.EventToolCallEnd:
			ended = ev.Arguments == `{"a": 2}`
		}
	}
	if !started || !ended {
		t.Fatalf("missing tool events: started=%v ended=%v events=%+v", started, ended, events)
	}
}

func TestStreamCompletionPlainContent(t *testing.T) {
	var deltas []string
	//lint:ignore SA1012 a nil parent is part of the contract: GenerateOptions.Context may be nil
	text, err := streamCompletion(nil, false,
		func(ev dsml.StreamEvent) {
			if ev.Type == dsml.EventContentDelta {
				deltas = append(deltas, ev.Delta)
			}
		},
		func(ctx context.Context, emit func(string), _ func() bool) error {
			emit("hello ")
			emit("world")
			return nil
		}, nil)
	if err != nil {
		t.Fatalf("streamCompletion: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q", text)
	}
	var joined string
	for _, d := range deltas {
		joined += d
	}
	if joined != "hello world" {
		t.Fatalf("content deltas reconstruct %q, want %q", joined, "hello world")
	}
}

func TestStreamCompletionPropagatesUserCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := streamCompletion(ctx, false, nil,
		func(genCtx context.Context, emit func(string), _ func() bool) error {
			emit("partial")
			cancel()
			return genCtx.Err()
		}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestStreamCompletionPropagatesGenerationError(t *testing.T) {
	boom := errors.New("boom")
	_, err := streamCompletion(context.Background(), false, nil,
		func(ctx context.Context, emit func(string), _ func() bool) error {
			return boom
		}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestToolLoopRunDefaultPathStreamsEvents(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	var deltas []string
	loop := ToolLoop{Engine: eng, Session: sess, Tools: NewToolRegistry()}
	result, err := loop.Run(ToolLoopOptions{
		History:  []ChatMessage{{Role: "user", Content: "hi"}},
		Generate: GenerateOptions{MaxTokens: 4},
		OnStreamEvent: func(ev dsml.StreamEvent) {
			if ev.Type == dsml.EventContentDelta {
				deltas = append(deltas, ev.Delta)
			}
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ToolRounds != 0 {
		t.Fatalf("ToolRounds = %d, want 0", result.ToolRounds)
	}
	if result.Assistant.Content == "" {
		t.Fatal("empty assistant content from default generation path")
	}
	var joined string
	for _, d := range deltas {
		joined += d
	}
	if strings.TrimSpace(joined) != result.Assistant.Content {
		t.Fatalf("content deltas reconstruct %q, want %q", joined, result.Assistant.Content)
	}
}

// malformedCompletion is balanced (not repairable) but the invoke header has
// no name attribute, so it degrades to plain content with a MalformedReason.
const malformedCompletion = "\n\n<｜DSML｜tool_calls>\n<｜DSML｜invoke>\n</｜DSML｜invoke>\n</｜DSML｜tool_calls>"

func TestToolLoopRunRetriesMalformedDSML(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()
	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	callCount := 0
	loop := ToolLoop{
		Engine: eng, Session: sess, Tools: NewToolRegistry(),
		CompleteFunc: func(prompt *Tokens, opts GenerateOptions) (string, error) {
			callCount++
			if callCount == 1 {
				return malformedCompletion, nil
			}
			return "the answer is 5", nil
		},
	}
	result, err := loop.Run(ToolLoopOptions{
		History: []ChatMessage{{Role: "user", Content: "what is 2 + 3?"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want a retry after the malformed turn", callCount)
	}
	if result.Assistant.Content != "the answer is 5" {
		t.Fatalf("Assistant.Content = %q", result.Assistant.Content)
	}
	if result.ToolRounds != 0 {
		t.Fatalf("ToolRounds = %d, want 0 — a failed parse is not a tool round", result.ToolRounds)
	}
	if len(result.History) != 4 {
		t.Fatalf("history length = %d, want user, failed assistant, tool error, final assistant", len(result.History))
	}
	if result.History[2].Role != "tool" ||
		!strings.Contains(result.History[2].Content, "Tool error: invalid DSML tool call") {
		t.Fatalf("missing tool error turn: %#v", result.History[2])
	}
}

func TestToolLoopRunSingleConsecutiveSyntaxRetry(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()
	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	callCount := 0
	loop := ToolLoop{
		Engine: eng, Session: sess, Tools: NewToolRegistry(),
		CompleteFunc: func(prompt *Tokens, opts GenerateOptions) (string, error) {
			callCount++
			return malformedCompletion, nil
		},
	}
	result, err := loop.Run(ToolLoopOptions{
		History: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want exactly one retry before giving up", callCount)
	}
	if result.Assistant.Content != strings.TrimSpace(malformedCompletion) {
		t.Fatalf("Assistant.Content = %q, want the raw completion returned as content", result.Assistant.Content)
	}
	errorTurns := 0
	for _, msg := range result.History {
		if msg.Role == "tool" && strings.Contains(msg.Content, "Tool error: invalid DSML tool call") {
			errorTurns++
		}
	}
	if errorTurns != 1 {
		t.Fatalf("error turns = %d, want 1", errorTurns)
	}
}

func TestToolLoopRunNoSyntaxRetryWithoutRoundBudget(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()
	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	callCount := 0
	loop := ToolLoop{
		Engine: eng, Session: sess, Tools: NewToolRegistry(),
		CompleteFunc: func(prompt *Tokens, opts GenerateOptions) (string, error) {
			callCount++
			return malformedCompletion, nil
		},
	}
	result, err := loop.Run(ToolLoopOptions{
		History:   []ChatMessage{{Role: "user", Content: "hi"}},
		MaxRounds: 1,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("callCount = %d, want no retry with MaxRounds 1", callCount)
	}
	if len(result.History) != 2 {
		t.Fatalf("history length = %d, want user and assistant only", len(result.History))
	}
}

func TestToolLoopRunMaxRounds(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()

	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	reg := NewToolRegistry()
	if err := reg.RegisterFunc(ToolSchema{
		Name:        "again",
		Description: "Repeat forever",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, args json.RawMessage) (string, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("RegisterFunc: %v", err)
	}

	rendered, err := dsml.RenderToolCalls([]dsml.ToolCall{{
		Name:      "again",
		Arguments: `{"n":1}`,
	}})
	if err != nil {
		t.Fatalf("RenderToolCalls: %v", err)
	}

	loop := ToolLoop{
		Engine:  eng,
		Session: sess,
		Tools:   reg,
		CompleteFunc: func(prompt *Tokens, opts GenerateOptions) (string, error) {
			return "still working" + rendered, nil
		},
	}

	_, err = loop.Run(ToolLoopOptions{
		History:   []ChatMessage{{Role: "user", Content: "loop"}},
		MaxRounds: 1,
	})
	if err == nil {
		t.Fatal("expected max-rounds error")
	}
}

func TestStreamCompletionRecoversToolCallInUnclosedThink(t *testing.T) {
	phase := 0
	recovered := 0
	thinkRecover := func() (string, error) {
		recovered++
		return "</think>\n\n", nil
	}
	gen := func(ctx context.Context, emit func(string), _ func() bool) error {
		if phase == 0 {
			phase = 1
			for _, p := range []string{"<think>", "I will call the tool ", "\n\n<｜DSML｜tool_calls>", "never reached"} {
				if err := ctx.Err(); err != nil {
					return err
				}
				emit(p)
			}
			return errors.New("generation was not cancelled for think recovery")
		}
		// Resumed after the forced close: the model restarts the call on the
		// executable side.
		for _, p := range []string{"\n\n<｜DSML｜tool_calls>\n", "<｜DSML｜invoke name=\"add\">\n", "</｜DSML｜invoke>\n", "</｜DSML｜tool_calls>"} {
			if err := ctx.Err(); err != nil {
				return err
			}
			emit(p)
		}
		return nil
	}

	text, err := streamCompletion(context.Background(), true, nil, gen, thinkRecover)
	if err != nil {
		t.Fatalf("streamCompletion: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recover called %d times, want 1", recovered)
	}
	msg, perr := dsml.ParseCompletion(text, true)
	if perr != nil {
		t.Fatalf("ParseCompletion: %v", perr)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "add" {
		t.Fatalf("recovered turn parsed to %+v, want one add call", msg.ToolCalls)
	}
	if !strings.Contains(msg.ReasoningContent, "<｜DSML｜tool_calls>") {
		t.Fatalf("dangling stanza opening not kept inside reasoning: %q", msg.ReasoningContent)
	}
}

func TestStreamCompletionNoRecoveryWithoutCallback(t *testing.T) {
	gen := func(ctx context.Context, emit func(string), _ func() bool) error {
		for _, p := range []string{"<think>", "thinking ", "\n\n<｜DSML｜tool_calls>", " more thinking"} {
			if err := ctx.Err(); err != nil {
				return err
			}
			emit(p)
		}
		return nil
	}
	text, err := streamCompletion(context.Background(), true, nil, gen, nil)
	if err != nil {
		t.Fatalf("streamCompletion: %v", err)
	}
	msg, perr := dsml.ParseCompletion(text, true)
	if perr != nil {
		t.Fatalf("ParseCompletion: %v", perr)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("tool calls = %+v, want none without recovery", msg.ToolCalls)
	}
	if !strings.Contains(msg.ReasoningContent, "more thinking") {
		t.Fatalf("generation was interrupted without a recovery callback: %q", msg.ReasoningContent)
	}
}

func TestCompleteTurnDelegatesToCompleteFunc(t *testing.T) {
	loop := ToolLoop{
		CompleteFunc: func(prompt *Tokens, opts GenerateOptions) (string, error) {
			return "from-complete-func", nil
		},
	}
	text, err := loop.CompleteTurn(nil, GenerateOptions{}, nil)
	if err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if text != "from-complete-func" {
		t.Fatalf("text = %q, want CompleteFunc result", text)
	}
}

func TestStreamCompletionThreadsGreedyPredicate(t *testing.T) {
	// A bare-invoke completion fed char-by-char; the fake generator samples the
	// greedy predicate after each emit. Proves WantsGreedySampling is wired to
	// the live decoder state and flips on inside DSML grammar.
	full := "answer\n\n<｜DSML｜invoke name=\"x\">\n</｜DSML｜invoke><｜end▁of▁sentence｜>"
	var sawGreedy, sawNonGreedy bool
	gen := func(ctx context.Context, emit func(string), wantGreedy func() bool) error {
		for _, r := range full {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			emit(string(r))
			if wantGreedy() {
				sawGreedy = true
			} else {
				sawNonGreedy = true
			}
		}
		return nil
	}
	text, err := streamCompletion(context.Background(), false, nil, gen, nil)
	if err != nil {
		t.Fatalf("streamCompletion: %v", err)
	}
	if text == "" {
		t.Fatal("expected accumulated text")
	}
	if !sawGreedy {
		t.Error("expected greedy sampling while emitting DSML structure")
	}
	if !sawNonGreedy {
		t.Error("expected non-greedy sampling while emitting plain content")
	}
}

func TestCompleteTurnStreamsViaDecoder(t *testing.T) {
	eng := mockEngine(t)
	defer eng.Close()
	sess, err := eng.NewSession(128)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	toks, err := eng.TokenizeText("hi")
	if err != nil {
		t.Fatalf("TokenizeText: %v", err)
	}
	defer toks.Free()

	var deltas []string
	loop := ToolLoop{Engine: eng, Session: sess, Tools: NewToolRegistry()}
	text, err := loop.CompleteTurn(toks, GenerateOptions{MaxTokens: 4, StopOnEOS: true},
		func(ev dsml.StreamEvent) {
			if ev.Type == dsml.EventContentDelta {
				deltas = append(deltas, ev.Delta)
			}
		})
	if err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if text == "" {
		t.Fatal("empty completion from mock engine")
	}
	if joined := strings.Join(deltas, ""); joined != text {
		t.Fatalf("content deltas reconstruct %q, want %q", joined, text)
	}
}
