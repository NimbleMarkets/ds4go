package dsml

import (
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// streamFixture is a completion fixture shared between ParseCompletion and
// StreamDecoder parity tests.
type streamFixture struct {
	name     string
	text     string
	thinking bool
}

// Fixtures mirror every test case in decode_test.go so parity can be verified.
var streamFixtures = []streamFixture{
	{"plainContent", "Hello there.", false},
	{"thinking", "reasoning here</think>final answer", true},
	{"thinkingMissingEnd", "reasoning with no end", true},
	{
		"toolCalls",
		"answer\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke name=\"add\">\n" +
			"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">2</" + dsmlMarker + "parameter>\n" +
			"<" + dsmlMarker + "parameter name=\"b\" string=\"false\">3</" + dsmlMarker + "parameter>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>" + eosToken,
		false,
	},
	{
		"stringParameter",
		"ok\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke name=\"weather\">\n" +
			"<" + dsmlMarker + "parameter name=\"city\" string=\"true\">New York</" + dsmlMarker + "parameter>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>",
		false,
	},
	{
		"malformedInvoke",
		"x\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke garbage>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>",
		false,
	},
	{
		"multipleToolCalls",
		"doing two things\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke name=\"add\">\n" +
			"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">1</" + dsmlMarker + "parameter>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"<" + dsmlMarker + "invoke name=\"greet\">\n" +
			"<" + dsmlMarker + "parameter name=\"who\" string=\"true\">world</" + dsmlMarker + "parameter>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>" + eosToken,
		false,
	},
	{
		"unexpectedTextAfterToolCalls",
		"x\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke name=\"calc\">\n" +
			"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">1</" + dsmlMarker + "parameter>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>\nextra",
		false,
	},
	{
		"thinkingWithToolCalls",
		"let me compute</think>here goes\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke name=\"add\">\n" +
			"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">5</" + dsmlMarker + "parameter>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>" + eosToken,
		true,
	},
	{
		"invalidJSONArgument",
		"x\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke name=\"calc\">\n" +
			"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">not json</" + dsmlMarker + "parameter>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>",
		false,
	},
	{
		"duplicateParameter",
		"x\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke name=\"calc\">\n" +
			"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">1</" + dsmlMarker + "parameter>\n" +
			"<" + dsmlMarker + "parameter name=\"n\" string=\"false\">2</" + dsmlMarker + "parameter>\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>",
		false,
	},
	{
		"toolCallsInsideUnfinishedThinking",
		"reasoning\n\n<" + dsmlMarker + "tool_calls>\n" +
			"<" + dsmlMarker + "invoke name=\"danger\">\n" +
			"</" + dsmlMarker + "invoke>\n" +
			"</" + dsmlMarker + "tool_calls>",
		true,
	},
	{
		"plainXMLToolCalls",
		"ok\n\n<tool_calls>\n" +
			"<invoke name=\"add\">\n" +
			"<parameter name=\"a\" string=\"false\">1</parameter>\n" +
			"</invoke>\n" +
			"</tool_calls>",
		false,
	},
}

// chunkStrategy turns a string into a slice of chunks.
type chunkStrategy struct {
	name  string
	split func(string) []string
}

var chunkStrategies = []chunkStrategy{
	{
		name: "whole",
		split: func(s string) []string {
			return []string{s}
		},
	},
	{
		name: "1-byte",
		split: func(s string) []string {
			var chunks []string
			for i := 0; i < len(s); i++ {
				chunks = append(chunks, s[i:i+1])
			}
			return chunks
		},
	},
	{
		name: "3-byte",
		split: func(s string) []string {
			var chunks []string
			for i := 0; i < len(s); i += 3 {
				end := i + 3
				if end > len(s) {
					end = len(s)
				}
				chunks = append(chunks, s[i:end])
			}
			return chunks
		},
	},
	{
		name: "random",
		split: func(s string) []string {
			rng := rand.New(rand.NewSource(42))
			var chunks []string
			for len(s) > 0 {
				n := rng.Intn(16) + 1
				if n > len(s) {
					n = len(s)
				}
				chunks = append(chunks, s[:n])
				s = s[n:]
			}
			return chunks
		},
	},
}

// TestStreamDecoderParity verifies that for every fixture and every chunk
// strategy the assembled ParsedMessage from Close matches ParseCompletion.
func TestStreamDecoderParity(t *testing.T) {
	for _, fix := range streamFixtures {
		want, err := ParseCompletion(fix.text, fix.thinking)
		if err != nil {
			t.Fatalf("ParseCompletion(%q) err = %v", fix.name, err)
		}

		for _, strat := range chunkStrategies {
			t.Run(fix.name+"/"+strat.name, func(t *testing.T) {
				d := NewStreamDecoder(fix.thinking)
				var allEvents []StreamEvent
				for _, chunk := range strat.split(fix.text) {
					allEvents = append(allEvents, d.Write(chunk)...)
				}
				finalEvents, got, err := d.Close()
				if err != nil {
					t.Fatalf("Close err = %v", err)
				}
				allEvents = append(allEvents, finalEvents...)

				if !reflect.DeepEqual(got, want) {
					t.Fatalf("ParsedMessage mismatch:\n got: %+v\nwant: %+v", got, want)
				}

				// Verify that the event stream can be reconstructed into the
				// ParsedMessage produced by ParseCompletion.
				verifyDeltaInvariant(t, allEvents, want)
			})
		}
	}
}

func verifyDeltaInvariant(t *testing.T, events []StreamEvent, want ParsedMessage) {
	t.Helper()

	var reasoning, content strings.Builder
	argDeltas := map[int][]string{}
	hasToolStart := map[int]bool{}
	endArguments := map[int]string{}

	for _, ev := range events {
		switch ev.Type {
		case EventReasoningDelta:
			reasoning.WriteString(ev.Delta)
		case EventContentDelta:
			content.WriteString(ev.Delta)
		case EventToolCallStart:
			hasToolStart[ev.Index] = true
		case EventToolCallArgumentsDelta:
			argDeltas[ev.Index] = append(argDeltas[ev.Index], ev.Delta)
		case EventToolCallEnd:
			endArguments[ev.Index] = ev.Arguments
		}
	}

	if got := strings.TrimSpace(reasoning.String()); got != want.ReasoningContent {
		t.Errorf("concatenated reasoning = %q, want %q", got, want.ReasoningContent)
	}
	if got := strings.TrimSpace(content.String()); got != want.Content {
		t.Errorf("concatenated content = %q, want %q", got, want.Content)
	}

	if len(want.ToolCalls) == 0 {
		if len(hasToolStart) > 0 || len(endArguments) > 0 || len(argDeltas) > 0 {
			t.Errorf("unexpected tool events for completion with no tool calls")
		}
		return
	}

	for i, tc := range want.ToolCalls {
		if !hasToolStart[i] {
			t.Errorf("tool %d missing ToolCallStart event", i)
		}
		endArgs, ok := endArguments[i]
		if !ok {
			t.Errorf("tool %d missing ToolCallEnd event", i)
		} else if endArgs != tc.Arguments {
			t.Errorf("tool %d end arguments = %q, want %q", i, endArgs, tc.Arguments)
		}
		fragments := argDeltas[i]
		if len(fragments) == 0 && tc.Arguments == "" {
			continue
		}
		got := strings.Join(fragments, "")
		if got != tc.Arguments && (!ok || endArgs == "" || !strings.HasSuffix(got, endArgs)) {
			t.Errorf("tool %d concatenated arguments = %q, want %q", i, got, tc.Arguments)
		}
	}
}

// TestStreamDecoderEvents verifies the exact event sequence for a well-formed
// tool-calls completion.
func TestStreamDecoderEvents(t *testing.T) {
	completion := "answer\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"add\">\n" +
		"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">2</" + dsmlMarker + "parameter>\n" +
		"<" + dsmlMarker + "parameter name=\"b\" string=\"false\">3</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"

	d := NewStreamDecoder(false)
	events := d.Write(completion)
	finalEvents, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	events = append(events, finalEvents...)

	if msg.Content != "answer" {
		t.Fatalf("Content = %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d", len(msg.ToolCalls))
	}

	wantTypes := []StreamEventType{
		EventContentDelta,
		EventToolCallStart,
		EventToolCallArgumentsDelta,
		EventToolCallArgumentsDelta,
		EventToolCallArgumentsDelta, // closing "}"
		EventToolCallEnd,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, ev := range events {
		if ev.Type != wantTypes[i] {
			t.Errorf("event %d type = %d, want %d", i, ev.Type, wantTypes[i])
		}
	}

	if events[1].Name != "add" {
		t.Errorf("ToolCallStart Name = %q", events[1].Name)
	}
	if events[2].Delta != `{"a": 2` {
		t.Errorf("first args delta = %q", events[2].Delta)
	}
	if events[3].Delta != `, "b": 3` {
		t.Errorf("second args delta = %q", events[3].Delta)
	}
	if events[4].Delta != "}" {
		t.Errorf("closing args delta = %q", events[4].Delta)
	}
	if events[5].Arguments != msg.ToolCalls[0].Arguments {
		t.Errorf("end args = %q, want %q", events[5].Arguments, msg.ToolCalls[0].Arguments)
	}
}

// TestStreamDecoderMalformedEvents verifies that malformed DSML does not emit
// tool-call events and falls back to raw content deltas.
func TestStreamDecoderMalformedEvents(t *testing.T) {
	completion := "x\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke garbage>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"

	d := NewStreamDecoder(false)
	var allEvents []StreamEvent
	for _, chunk := range []string{"x\n\n<" + dsmlMarker + "tool_calls>\n", "<" + dsmlMarker + "invoke garbage>\n", "</" + dsmlMarker + "invoke>\n", "</" + dsmlMarker + "tool_calls>"} {
		allEvents = append(allEvents, d.Write(chunk)...)
	}
	finalEvents, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	allEvents = append(allEvents, finalEvents...)

	if msg.Content != completion {
		t.Fatalf("Content = %q, want %q", msg.Content, completion)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("malformed DSML produced tool calls: %#v", msg.ToolCalls)
	}

	for _, ev := range allEvents {
		if ev.Type == EventToolCallStart || ev.Type == EventToolCallArgumentsDelta || ev.Type == EventToolCallEnd {
			t.Fatalf("malformed DSML emitted tool event: %+v", ev)
		}
	}

	var content strings.Builder
	for _, ev := range allEvents {
		if ev.Type == EventContentDelta {
			content.WriteString(ev.Delta)
		}
	}
	if content.String() != completion {
		t.Fatalf("concatenated content = %q, want raw completion %q", content.String(), completion)
	}
}

// TestStreamDecoderThinkingChunked verifies reasoning deltas are emitted
// correctly when </think> is split across chunks.
func TestStreamDecoderThinkingChunked(t *testing.T) {
	chunks := []string{"reason", "ing he", "re</th", "ink>final answer"}
	d := NewStreamDecoder(true)
	var allEvents []StreamEvent
	for _, c := range chunks {
		allEvents = append(allEvents, d.Write(c)...)
	}
	finalEvents, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	allEvents = append(allEvents, finalEvents...)

	if msg.ReasoningContent != "reasoning here" {
		t.Fatalf("ReasoningContent = %q", msg.ReasoningContent)
	}
	if msg.Content != "final answer" {
		t.Fatalf("Content = %q", msg.Content)
	}

	var reasoning strings.Builder
	for _, ev := range allEvents {
		if ev.Type == EventReasoningDelta {
			reasoning.WriteString(ev.Delta)
		}
	}
	if strings.TrimSpace(reasoning.String()) != "reasoning here" {
		t.Fatalf("concatenated reasoning = %q", reasoning.String())
	}
}

// TestStreamDecoderEOSChunked verifies that the EOS marker split across
// chunks is handled correctly.
func TestStreamDecoderEOSChunked(t *testing.T) {
	marker := eosToken
	chunks := []string{"hello ", marker[:3], marker[3:7], marker[7:]}
	d := NewStreamDecoder(false)
	var allEvents []StreamEvent
	for _, c := range chunks {
		allEvents = append(allEvents, d.Write(c)...)
	}
	finalEvents, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	allEvents = append(allEvents, finalEvents...)

	if msg.Content != "hello" {
		t.Fatalf("Content = %q", msg.Content)
	}

	var content strings.Builder
	for _, ev := range allEvents {
		if ev.Type == EventContentDelta {
			content.WriteString(ev.Delta)
		}
	}
	if strings.TrimSpace(content.String()) != "hello" {
		t.Fatalf("concatenated content = %q", content.String())
	}
}

// TestStreamDecoderToolBlockSplit verifies that a tool-calls block split at
// every possible byte boundary still assembles correctly.
func TestStreamDecoderToolBlockSplit(t *testing.T) {
	completion := "ok\n\n<" + dsmlMarker + "tool_calls>\n" +
		"<" + dsmlMarker + "invoke name=\"add\">\n" +
		"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">1</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>\n" +
		"</" + dsmlMarker + "tool_calls>"

	want, err := ParseCompletion(completion, false)
	if err != nil {
		t.Fatalf("ParseCompletion: %v", err)
	}

	for split := 1; split < len(completion); split++ {
		chunks := []string{completion[:split], completion[split:]}
		d := NewStreamDecoder(false)
		for _, c := range chunks {
			d.Write(c)
		}
		_, got, err := d.Close()
		if err != nil {
			t.Fatalf("split=%d Close: %v", split, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("split=%d mismatch:\n got: %+v\nwant: %+v", split, got, want)
		}
	}
}

// TestStreamDecoderEmptyInput verifies graceful handling of empty writes.
func TestStreamDecoderEmptyInput(t *testing.T) {
	d := NewStreamDecoder(false)
	if evs := d.Write(""); len(evs) != 0 {
		t.Fatalf("empty Write emitted %d events", len(evs))
	}
	finalEvents, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(finalEvents) != 0 {
		t.Fatalf("Close emitted %d events", len(finalEvents))
	}
	if msg.Content != "" || len(msg.ToolCalls) != 0 {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

// TestStreamDecoderRace runs the parity test with many goroutines to exercise
// the race detector. Each goroutine gets its own decoder.
func TestStreamDecoderRace(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for _, fix := range streamFixtures {
				want, _ := ParseCompletion(fix.text, fix.thinking)

				var chunks []string
				s := fix.text
				for len(s) > 0 {
					n := rng.Intn(16) + 1
					if n > len(s) {
						n = len(s)
					}
					chunks = append(chunks, s[:n])
					s = s[n:]
				}

				d := NewStreamDecoder(fix.thinking)
				for _, c := range chunks {
					d.Write(c)
				}
				_, got, _ := d.Close()
				if !reflect.DeepEqual(got, want) {
					t.Errorf("fixture %q mismatch", fix.name)
				}
			}
		}(time.Now().UnixNano() + int64(i))
	}
	wg.Wait()
}

// Predicate tests for generation-driving callers: ToolBlockClosed lets a
// caller stop sampling as soon as the tool block closes instead of paying
// for trailing tokens; Done reports an explicit end-of-sentence marker.

func TestStreamDecoderToolBlockClosed(t *testing.T) {
	block := "I'll check.\n\n<｜DSML｜tool_calls>\n" +
		"<｜DSML｜invoke name=\"bash\">\n" +
		"<｜DSML｜parameter name=\"command\" string=\"true\">pwd</｜DSML｜parameter>\n" +
		"</｜DSML｜invoke>\n" +
		"</｜DSML｜tool_calls>"
	closer := "</｜DSML｜tool_calls>"

	dec := NewStreamDecoder(false)
	dec.Write(block[:len(block)-len(closer)])
	if dec.ToolBlockClosed() {
		t.Fatal("ToolBlockClosed true before the closing tag")
	}
	dec.Write(block[len(block)-len(closer):])
	if !dec.ToolBlockClosed() {
		t.Fatal("ToolBlockClosed false after the closing tag")
	}
	_, msg, err := dec.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "bash" {
		t.Fatalf("tool calls = %+v", msg.ToolCalls)
	}
}

func TestStreamDecoderToolBlockClosedRevertsOnTrailingText(t *testing.T) {
	dec := NewStreamDecoder(false)
	dec.Write("\n\n<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\">\n</｜DSML｜invoke>\n</｜DSML｜tool_calls>")
	if !dec.ToolBlockClosed() {
		t.Fatal("ToolBlockClosed false after the closing tag")
	}
	// Trailing non-whitespace text degrades the block to raw content; the
	// closed signal must not survive that.
	dec.Write("and some trailing prose")
	if dec.ToolBlockClosed() {
		t.Fatal("ToolBlockClosed still true after the block degraded to raw content")
	}
	_, msg, err := dec.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("degraded block still produced tool calls: %+v", msg.ToolCalls)
	}
}

func TestStreamDecoderDone(t *testing.T) {
	dec := NewStreamDecoder(false)
	dec.Write("all finished")
	if dec.Done() {
		t.Fatal("Done true before the end-of-sentence marker")
	}
	dec.Write("<｜end▁of▁sentence｜>")
	if !dec.Done() {
		t.Fatal("Done false after the end-of-sentence marker")
	}
}

// Detection for upstream ds4's think-tool recovery: a complete stanza opening
// inside a still-unclosed <think> block almost always means the model forgot
// to close its thinking. A generation-driving caller can force-feed
// "</think>\n\n" so the model restarts the call on the executable side.

func TestStreamDecoderToolStanzaInThinking(t *testing.T) {
	dec := NewStreamDecoder(true)
	dec.Write("<think>I should call the tool now ")
	if dec.ToolStanzaInThinking() {
		t.Fatal("detected without any stanza opening")
	}
	dec.Write("<｜DSML｜tool_")
	if dec.ToolStanzaInThinking() {
		t.Fatal("detected on a partial opening")
	}
	dec.Write("calls>")
	if !dec.ToolStanzaInThinking() {
		t.Fatal("complete stanza opening inside unclosed think not detected")
	}
	// The forced close moves decoding out of thinking; the signal clears.
	dec.Write("</think>\n\n")
	if dec.ToolStanzaInThinking() {
		t.Fatal("signal survived leaving the thinking state")
	}
}

func TestStreamDecoderToolStanzaInThinkingHoldsBackAcrossChunks(t *testing.T) {
	dec := NewStreamDecoder(true)
	dec.Write("<think>" + strings.Repeat("reasoning ", 30))
	dec.Write("<｜DSML｜tool_")
	dec.Write("calls>")
	if !dec.ToolStanzaInThinking() {
		t.Fatal("opening split across chunks after long reasoning not detected")
	}
}

func TestStreamDecoderToolStanzaIgnoresClosedThink(t *testing.T) {
	dec := NewStreamDecoder(true)
	dec.Write("reasoning</think>\n\n<｜DSML｜tool_calls>\n")
	if dec.ToolStanzaInThinking() {
		t.Fatal("stanza after </think> misreported as inside thinking")
	}
}

func TestStreamDecoderToolStanzaNotInPlainContent(t *testing.T) {
	dec := NewStreamDecoder(false)
	dec.Write("\n\n<｜DSML｜tool_calls>\n")
	if dec.ToolStanzaInThinking() {
		t.Fatal("non-thinking decoder reported a stanza in thinking")
	}
}

func TestStreamDecoderImplicitInvoke(t *testing.T) {
	completion := "ok\n\n<" + dsmlMarker + "invoke name=\"add\">\n" +
		"<" + dsmlMarker + "parameter name=\"a\" string=\"false\">2</" + dsmlMarker + "parameter>\n" +
		"</" + dsmlMarker + "invoke>" + eosToken
	d := NewStreamDecoder(false)
	var events []StreamEvent
	events = append(events, d.Write(completion)...)
	tail, msg, err := d.Close()
	events = append(events, tail...)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "add" {
		t.Fatalf("want one call to add, got %#v", msg.ToolCalls)
	}
	var starts, ends int
	for _, ev := range events {
		switch ev.Type {
		case EventToolCallStart:
			starts++
		case EventToolCallEnd:
			ends++
		}
	}
	if starts != 1 || ends != 1 {
		t.Errorf("tool-call events start=%d end=%d, want 1/1", starts, ends)
	}
}

func TestStreamDecoderImplicitInvokeClosesEarly(t *testing.T) {
	// ToolBlockClosed must fire on the implicit block so generation can stop.
	d := NewStreamDecoder(false)
	d.Write("<" + dsmlMarker + "invoke name=\"x\">\n</" + dsmlMarker + "invoke>" + eosToken)
	if !d.ToolBlockClosed() {
		t.Fatal("ToolBlockClosed() = false for a complete implicit block")
	}
}

func TestStreamDecoderInvokeNameBoundaryDegradesToRaw(t *testing.T) {
	// "<｜DSML｜invokefoo" must not be treated as an invoke; the block is raw.
	d := NewStreamDecoder(false)
	completion := "<" + dsmlMarker + "tool_calls>\n<" + dsmlMarker + "invokefoo name=\"x\">"
	d.Write(completion)
	_, msg, err := d.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("invokefoo must not produce tool calls: %#v", msg.ToolCalls)
	}
}

func TestStreamDecoderImplicitInvokeNameBoundaryEmitsNoToolEvents(t *testing.T) {
	// A wrapper-less "<｜DSML｜invokefoo" must not enter the tool path: the
	// streaming decoder emits no tool-call events and never reports the block
	// closed. (Close() also re-parses, but this asserts the streaming path.)
	d := NewStreamDecoder(false)
	var events []StreamEvent
	events = append(events, d.Write("<"+dsmlMarker+"invokefoo name=\"x\">\n</"+dsmlMarker+"invoke>"+eosToken)...)
	if d.ToolBlockClosed() {
		t.Fatal("ToolBlockClosed() = true for a non-invoke (invokefoo) implicit input")
	}
	tail, msg, err := d.Close()
	events = append(events, tail...)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, ev := range events {
		if ev.Type == EventToolCallStart || ev.Type == EventToolCallEnd {
			t.Fatalf("unexpected tool-call event for invokefoo: %+v", ev)
		}
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("invokefoo must not produce tool calls: %#v", msg.ToolCalls)
	}
}
