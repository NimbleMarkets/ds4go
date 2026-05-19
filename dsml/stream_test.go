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
