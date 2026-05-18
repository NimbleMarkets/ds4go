package dsml

import (
	"bytes"
	"encoding/json"
	"strings"
)

// safetyMargin is the number of trailing bytes we hold back in the buffer
// to avoid splitting a DSML marker across chunk boundaries. The longest
// marker is <｜end▁of▁sentence｜> at 27 UTF-8 bytes; 64 is generous.
const safetyMargin = 64

// StreamEventType classifies the kind of incremental update.
type StreamEventType int

const (
	// EventReasoningDelta carries a fragment of the assistant's reasoning.
	EventReasoningDelta StreamEventType = iota
	// EventContentDelta carries a fragment of the assistant's user-facing reply.
	EventContentDelta
	// EventToolCallStart signals the beginning of a new tool call. Name holds
	// the tool name and Index is the 0-based tool-call position.
	EventToolCallStart
	// EventToolCallArgumentsDelta carries a JSON fragment that should be
	// concatenated with previous deltas for the same Index to reconstruct the
	// full arguments object.
	EventToolCallArgumentsDelta
	// EventToolCallEnd signals the completion of a tool call at Index.
	EventToolCallEnd
)

// StreamEvent carries one incremental update from the decoder.
type StreamEvent struct {
	Type  StreamEventType
	Index int    // tool-call index for tool-related events
	Delta string // text fragment for delta events
	Name  string // tool name for EventToolCallStart
}

// StreamDecoder incrementally parses assistant output and emits streaming
// events. It is NOT safe for concurrent use.
//
// Tool-call events are buffered internally until the enclosing
// </tool_calls> block is fully validated. This guarantees that callers never
// see tool events for a block that ParseCompletion would reject as malformed.
// If malformed DSML is detected, the decoder falls back to raw-content mode
// and replays any consumed text as content deltas.
type StreamDecoder struct {
	thinking  bool
	buf       []byte
	fullText  []byte
	state     decoderState
	finalPass bool // true during Close(); disables safety margins

	// Tool-block parsing state.
	syntax          dsmlSyntax
	seenToolCallsOp bool
	rawBlockStart   int // absolute offset in fullText
	calls           []ToolCall
	pendingArgs     *orderedArgs
	paramName       string
	paramIsString   bool
	hasDupParam     bool

	// pendingEvents buffers tool-call-related events until the enclosing
	// </tool_calls> block is validated.
	pendingEvents []StreamEvent
}

type decoderState int

const (
	stateThinking decoderState = iota
	stateContent
	stateInToolCalls
	stateInInvoke
	stateInInvokeBody
	stateInParameter
	stateInParameterValue
	stateCheckingToolBlockEnd
	stateRaw
	stateDone
)

// NewStreamDecoder returns a decoder that consumes assistant output
// incrementally. thinking has the same meaning as in ParseCompletion.
func NewStreamDecoder(thinking bool) *StreamDecoder {
	s := stateContent
	if thinking {
		s = stateThinking
	}
	return &StreamDecoder{thinking: thinking, state: s}
}

// Write feeds the next chunk of decoded model text and returns any events
// that became complete. Chunk boundaries are arbitrary — a marker may be
// split across calls.
func (d *StreamDecoder) Write(chunk string) []StreamEvent {
	d.buf = append(d.buf, chunk...)
	d.fullText = append(d.fullText, chunk...)
	return d.process()
}

// Close finalizes the stream and returns any trailing events plus the fully
// assembled ParsedMessage (identical to what ParseCompletion would return for
// the concatenated input).
func (d *StreamDecoder) Close() ([]StreamEvent, ParsedMessage, error) {
	d.finalPass = true
	events := d.process()

	// Flush any remaining buffer based on current state.
	switch d.state {
	case stateThinking:
		d.emitReasoning(&events, string(d.buf))
	case stateContent, stateRaw:
		d.emitContent(&events, string(d.buf))
	case stateCheckingToolBlockEnd:
		if len(d.buf) == 0 || len(skipLeadingWhitespaceBytes(d.buf)) == 0 {
			d.completeToolBlock(&events)
		} else {
			d.emitContent(&events, string(d.buf))
		}
	case stateDone:
		// nothing
	default:
		// Unterminated tool structure – replay consumed text and emit rest.
		d.replayConsumedAsContent(&events)
		d.emitContent(&events, string(d.buf))
	}
	d.buf = nil

	msg, err := ParseCompletion(string(d.fullText), d.thinking)
	return events, msg, err
}

func (d *StreamDecoder) process() []StreamEvent {
	var events []StreamEvent
	for {
		before := len(d.buf)
		switch d.state {
		case stateThinking:
			d.processThinking(&events)
		case stateContent:
			d.processContent(&events)
		case stateInToolCalls:
			d.processInToolCalls(&events)
		case stateInInvoke:
			d.processInInvoke(&events)
		case stateInInvokeBody:
			d.processInInvokeBody(&events)
		case stateInParameter:
			d.processInParameter(&events)
		case stateInParameterValue:
			d.processInParameterValue(&events)
		case stateCheckingToolBlockEnd:
			d.processCheckingToolBlockEnd(&events)
		case stateRaw:
			d.processRaw(&events)
		case stateDone:
			d.buf = nil
		}
		if len(d.buf) == before {
			break
		}
	}
	return events
}

// ----------------------------------------------------------------------
// State handlers
// ----------------------------------------------------------------------

func (d *StreamDecoder) processThinking(events *[]StreamEvent) {
	if bytes.HasPrefix(d.buf, []byte("<think>")) {
		d.buf = d.buf[len("<think>"):]
	}

	if idx := bytes.Index(d.buf, []byte(thinkingEndToken)); idx >= 0 {
		d.emitReasoning(events, string(d.buf[:idx]))
		d.buf = d.buf[idx+len(thinkingEndToken):]
		d.state = stateContent
		return
	}

	if d.finalPass {
		d.emitReasoning(events, string(d.buf))
		d.buf = nil
		return
	}

	holdBack := len(thinkingEndToken)
	if len(d.buf) > holdBack {
		d.emitReasoning(events, string(d.buf[:len(d.buf)-holdBack]))
		d.buf = d.buf[len(d.buf)-holdBack:]
	}
}

func (d *StreamDecoder) processContent(events *[]StreamEvent) {
	eosIdx := bytes.Index(d.buf, []byte(eosToken))
	_, rawStart, syn, hasTool := findToolBlockStart(string(d.buf), 0)

	if hasTool && (eosIdx < 0 || rawStart < eosIdx) {
		// Tool block appears before EOS (or EOS absent).
		d.emitContent(events, string(d.buf[:rawStart]))
		d.buf = d.buf[rawStart:]
		d.syntax = syn
		d.rawBlockStart = len(d.fullText) - len(d.buf)
		d.state = stateInToolCalls
		return
	}

	if eosIdx >= 0 && (!hasTool || eosIdx < rawStart) {
		// EOS appears before tool block (or tool block absent).
		d.emitContent(events, string(d.buf[:eosIdx]))
		d.buf = d.buf[eosIdx+len(eosToken):]
		d.state = stateDone
		return
	}

	// No complete transition marker; emit a safe prefix.
	safe := d.safeContentLen()
	if safe > 0 {
		d.emitContent(events, string(d.buf[:safe]))
		d.buf = d.buf[safe:]
	}
}

func (d *StreamDecoder) processInToolCalls(events *[]StreamEvent) {
	if !d.seenToolCallsOp {
		if bytes.HasPrefix(d.buf, []byte("\n\n")) {
			if bytes.HasPrefix(d.buf[2:], []byte(d.syntax.toolStart)) {
				d.buf = d.buf[2+len(d.syntax.toolStart):]
				d.seenToolCallsOp = true
				return
			}
			d.enterRawMode(events)
			return
		}
		if bytes.HasPrefix(d.buf, []byte(d.syntax.toolStart)) {
			d.buf = d.buf[len(d.syntax.toolStart):]
			d.seenToolCallsOp = true
			return
		}
		d.enterRawMode(events)
		return
	}

	d.buf = skipLeadingWhitespaceBytes(d.buf)
	if len(d.buf) == 0 {
		return
	}

	if matchComplete(d.buf, d.syntax.toolEnd) {
		d.buf = d.buf[len(d.syntax.toolEnd):]
		d.state = stateCheckingToolBlockEnd
		return
	}
	if matchComplete(d.buf, d.syntax.invokeStart) {
		d.state = stateInInvoke
		return
	}
	if !d.finalPass && (matchPartial(d.buf, d.syntax.toolEnd) || matchPartial(d.buf, d.syntax.invokeStart)) {
		return // wait for more data
	}
	d.enterRawMode(events)
}

func (d *StreamDecoder) processInInvoke(events *[]StreamEvent) {
	idx := bytes.IndexByte(d.buf, '>')
	if idx < 0 {
		if d.finalPass {
			d.enterRawMode(events)
		}
		return
	}
	tag := string(d.buf[:idx+1])
	name, ok := dsmlAttr(tag, "name")
	if !ok || name == "" {
		d.enterRawMode(events)
		return
	}
	d.buf = d.buf[idx+1:]
	d.calls = append(d.calls, ToolCall{Name: name})
	d.pendingArgs = newOrderedArgs()
	d.hasDupParam = false
	d.pendingEvents = append(d.pendingEvents, StreamEvent{
		Type:  EventToolCallStart,
		Index: len(d.calls) - 1,
		Name:  name,
	})
	d.state = stateInInvokeBody
}

func (d *StreamDecoder) processInInvokeBody(events *[]StreamEvent) {
	d.buf = skipLeadingWhitespaceBytes(d.buf)
	if len(d.buf) == 0 {
		return
	}

	if matchComplete(d.buf, d.syntax.invokeEnd) {
		d.buf = d.buf[len(d.syntax.invokeEnd):]
		d.completeInvoke(events)
		return
	}
	if matchComplete(d.buf, d.syntax.paramStart) {
		d.state = stateInParameter
		return
	}
	if !d.finalPass && (matchPartial(d.buf, d.syntax.invokeEnd) || matchPartial(d.buf, d.syntax.paramStart)) {
		return
	}
	d.enterRawMode(events)
}

func (d *StreamDecoder) processInParameter(events *[]StreamEvent) {
	idx := bytes.IndexByte(d.buf, '>')
	if idx < 0 {
		if d.finalPass {
			d.enterRawMode(events)
		}
		return
	}
	tag := string(d.buf[:idx+1])
	pName, ok := dsmlAttr(tag, "name")
	if !ok || pName == "" {
		d.enterRawMode(events)
		return
	}
	isStringText, _ := dsmlAttr(tag, "string")
	if isStringText == "" {
		isStringText = "true"
	}
	d.paramName = pName
	d.paramIsString = isStringText != "false"
	d.buf = d.buf[idx+1:]
	d.state = stateInParameterValue
}

func (d *StreamDecoder) processInParameterValue(events *[]StreamEvent) {
	idx := bytes.Index(d.buf, []byte(d.syntax.paramEnd))
	if idx < 0 {
		if d.finalPass {
			d.enterRawMode(events)
		}
		return
	}
	raw := string(d.buf[:idx])
	d.buf = d.buf[idx+len(d.syntax.paramEnd):]

	// Track duplicate parameters – we fall back to full-JSON emission at </invoke>.
	if _, dup := d.pendingArgs.values[d.paramName]; dup {
		d.hasDupParam = true
	}

	// Build incremental JSON fragment.
	if !d.hasDupParam {
		var frag strings.Builder
		if len(d.pendingArgs.keys) == 0 {
			frag.WriteByte('{')
		} else {
			frag.WriteString(", ")
		}
		frag.WriteString(toJSONString(d.paramName))
		frag.WriteString(": ")
		if d.paramIsString {
			frag.WriteString(toJSONString(dsmlUnescapeText(raw)))
		} else {
			frag.WriteString(raw)
		}
		d.pendingEvents = append(d.pendingEvents, StreamEvent{
			Type:  EventToolCallArgumentsDelta,
			Index: len(d.calls) - 1,
			Delta: frag.String(),
		})
	}

	d.pendingArgs.set(d.paramName, dsmlUnescapeText(raw), d.paramIsString)
	d.state = stateInInvokeBody
}

func (d *StreamDecoder) processCheckingToolBlockEnd(events *[]StreamEvent) {
	d.buf = skipLeadingWhitespaceBytes(d.buf)
	if len(d.buf) == 0 {
		return
	}
	if bytes.HasPrefix(d.buf, []byte(eosToken)) {
		d.buf = d.buf[len(eosToken):]
		d.completeToolBlock(events)
		d.state = stateDone
		return
	}
	if d.finalPass {
		// No more data coming. If buffer is empty or just whitespace, it's valid.
		if len(skipLeadingWhitespaceBytes(d.buf)) == 0 {
			d.completeToolBlock(events)
			d.state = stateContent
			return
		}
		d.enterRawMode(events)
		return
	}
	// Non-EOS text after </tool_calls> means malformed DSML unless it's a
	// partial EOS that we should wait for.
	if matchPartial(d.buf, eosToken) {
		return
	}
	d.enterRawMode(events)
}

func (d *StreamDecoder) processRaw(events *[]StreamEvent) {
	if idx := bytes.Index(d.buf, []byte(eosToken)); idx >= 0 {
		d.emitContent(events, string(d.buf[:idx]))
		d.buf = d.buf[idx+len(eosToken):]
		d.state = stateDone
		return
	}
	safe := d.safeContentLen()
	if safe > 0 {
		d.emitContent(events, string(d.buf[:safe]))
		d.buf = d.buf[safe:]
	}
}

// ----------------------------------------------------------------------
// Completion helpers
// ----------------------------------------------------------------------

func (d *StreamDecoder) completeInvoke(events *[]StreamEvent) {
	tc := &d.calls[len(d.calls)-1]
	tc.Arguments = d.pendingArgs.buildJSON()

	if !json.Valid([]byte(tc.Arguments)) {
		d.enterRawMode(events)
		return
	}

	if d.hasDupParam || len(d.pendingArgs.keys) == 0 {
		// Full JSON for duplicate parameters or no parameters.
		d.stripPendingArgDeltas(len(d.calls) - 1)
		d.pendingEvents = append(d.pendingEvents, StreamEvent{
			Type:  EventToolCallArgumentsDelta,
			Index: len(d.calls) - 1,
			Delta: tc.Arguments,
		})
	} else {
		d.pendingEvents = append(d.pendingEvents, StreamEvent{
			Type:  EventToolCallArgumentsDelta,
			Index: len(d.calls) - 1,
			Delta: "}",
		})
	}

	d.pendingEvents = append(d.pendingEvents, StreamEvent{
		Type:  EventToolCallEnd,
		Index: len(d.calls) - 1,
	})

	d.pendingArgs = nil
	d.state = stateInToolCalls
}

func (d *StreamDecoder) completeToolBlock(events *[]StreamEvent) {
	blockEnd := len(d.fullText) - len(d.buf)
	exact := string(d.fullText[d.rawBlockStart:blockEnd])
	for i := range d.calls {
		d.calls[i].Exact = exact
	}
	*events = append(*events, d.pendingEvents...)
	d.pendingEvents = nil
	d.seenToolCallsOp = false
	d.calls = nil
	d.state = stateContent
}

// stripPendingArgDeltas removes all EventToolCallArgumentsDelta events for
// the given tool index from pendingEvents. It is used when a duplicate
// parameter is detected so the earlier speculative fragments can be replaced
// by a single full-JSON delta.
func (d *StreamDecoder) stripPendingArgDeltas(toolIdx int) {
	filtered := d.pendingEvents[:0]
	for _, ev := range d.pendingEvents {
		if ev.Type == EventToolCallArgumentsDelta && ev.Index == toolIdx {
			continue
		}
		filtered = append(filtered, ev)
	}
	d.pendingEvents = filtered
}

// enterRawMode discards any buffered tool events, replays consumed DSML text
// as content, and transitions to raw-content mode.
func (d *StreamDecoder) enterRawMode(events *[]StreamEvent) {
	d.replayConsumedAsContent(events)
	d.state = stateRaw
	d.pendingEvents = nil
	d.calls = nil
	d.pendingArgs = nil
	d.seenToolCallsOp = false
}

// replayConsumedAsContent emits any text that was consumed from d.buf during
// tool-block parsing but never emitted as a delta. This ensures the
// concatenation of all content deltas reconstructs the raw completion on
// malformed-DSML fallback.
func (d *StreamDecoder) replayConsumedAsContent(events *[]StreamEvent) {
	consumedEnd := len(d.fullText) - len(d.buf)
	if consumedEnd > d.rawBlockStart {
		d.emitContent(events, string(d.fullText[d.rawBlockStart:consumedEnd]))
	}
}

func (d *StreamDecoder) emitReasoning(events *[]StreamEvent, text string) {
	if text == "" {
		return
	}
	*events = append(*events, StreamEvent{Type: EventReasoningDelta, Delta: text})
}

func (d *StreamDecoder) emitContent(events *[]StreamEvent, text string) {
	if text == "" {
		return
	}
	*events = append(*events, StreamEvent{Type: EventContentDelta, Delta: text})
}

// ----------------------------------------------------------------------
// Low-level helpers
// ----------------------------------------------------------------------

// safeContentLen returns the number of leading bytes in d.buf that can be
// safely emitted as content/reasoning without splitting a marker.
func (d *StreamDecoder) safeContentLen() int {
	if d.finalPass {
		return len(d.buf)
	}
	if len(d.buf) <= safetyMargin {
		return 0
	}
	tailStart := len(d.buf) - safetyMargin
	for i := len(d.buf) - 1; i >= tailStart; i-- {
		if d.buf[i] == '<' {
			pos := i
			if i >= 2 && d.buf[i-2] == '\n' && d.buf[i-1] == '\n' {
				pos = i - 2
			}
			return pos
		}
		if d.buf[i] == '\n' {
			return i
		}
	}
	return tailStart
}

func matchComplete(buf []byte, str string) bool {
	return len(buf) >= len(str) && bytes.HasPrefix(buf, []byte(str))
}

func matchPartial(buf []byte, str string) bool {
	return len(buf) < len(str) && strings.HasPrefix(str, string(buf))
}

func skipLeadingWhitespaceBytes(buf []byte) []byte {
	for len(buf) > 0 {
		switch buf[0] {
		case ' ', '\t', '\n', '\r':
			buf = buf[1:]
		default:
			return buf
		}
	}
	return buf
}
