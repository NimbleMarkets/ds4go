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
	// EventToolCallArgumentsDelta carries a live JSON fragment. For exact final
	// arguments, use EventToolCallEnd.Arguments.
	EventToolCallArgumentsDelta
	// EventToolCallEnd signals the completion of a tool call at Index.
	// Arguments holds the final authoritative JSON arguments object.
	EventToolCallEnd
)

// StreamEvent carries one incremental update from the decoder.
type StreamEvent struct {
	Type      StreamEventType
	Index     int    // tool-call index for tool-related events
	Delta     string // text fragment for delta events
	Name      string // tool name for EventToolCallStart
	Arguments string // final JSON arguments for EventToolCallEnd
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
	syntaxMode      Syntax // grammar selector (ds4's agent_tool_syntax)
	syntax          dsmlSyntax
	seenToolCallsOp bool
	implicitBlock   bool // tool calls arrived without a <…tool_calls> wrapper
	rawBlockStart   int  // absolute offset in fullText
	calls           []ToolCall
	pendingArgs     *orderedArgs
	paramName       string
	paramIsString   bool
	hasDupParam     bool

	// pendingEvents buffers tool-call-related events until the enclosing
	// </tool_calls> block is validated.
	pendingEvents []StreamEvent

	// toolBlockClosed is set once a complete </tool_calls> tag has been
	// consumed and cleared if trailing text later degrades the block to raw
	// content.
	toolBlockClosed bool

	// thinkScanFrom and thinkStanza implement stanza detection inside an
	// unclosed <think> block (see ToolStanzaInThinking). thinkScanFrom is the
	// fullText offset already scanned, held back far enough that an opening
	// split across chunks is still seen from its first byte.
	thinkScanFrom int
	thinkStanza   bool
}

// thinkStanzaHoldback is how many trailing bytes of accumulated text stay
// unscanned-but-rescannable so a stanza opening split across chunk boundaries
// is still detected once it completes. Mirrors upstream ds4's hold of 80,
// comfortably above the longest opening tag.
const thinkStanzaHoldback = 80

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

	// GLM states. GLM has no wrapper block: a call opens with <tool_call>,
	// its name is bare text up to the first tag, then <arg_key>/<arg_value>
	// pairs repeat until </tool_call>.
	stateGLMName
	stateGLMArgKey
	stateGLMArgValue
	stateGLMAfterCall
)

// NewStreamDecoder returns a decoder that consumes assistant output
// incrementally. thinking has the same meaning as in ParseCompletion.
func NewStreamDecoder(thinking bool) *StreamDecoder {
	return NewStreamDecoderSyntax(SyntaxDSML, thinking)
}

// NewStreamDecoderSyntax is [NewStreamDecoder] for an explicit tool-call markup
// syntax. GLM DSA models emit a different grammar from DeepSeek's DSML; see
// [Syntax].
func NewStreamDecoderSyntax(syntax Syntax, thinking bool) *StreamDecoder {
	s := stateContent
	if thinking {
		s = stateThinking
	}
	return &StreamDecoder{thinking: thinking, state: s, syntaxMode: syntax}
}

// ToolBlockClosed reports whether a complete </tool_calls> tag has been
// consumed. Once the block closes, the only valid continuation for the
// completion is whitespace and end-of-sentence, so a caller driving
// generation can stop sampling as soon as this returns true instead of
// paying for trailing tokens. The signal reverts to false if trailing
// non-whitespace text later degrades the block to raw content.
func (d *StreamDecoder) ToolBlockClosed() bool {
	return d.toolBlockClosed
}

// Done reports whether the explicit <｜end▁of▁sentence｜> marker has been
// consumed. Generation past this point produces no further usable output.
func (d *StreamDecoder) Done() bool {
	return d.state == stateDone
}

// ToolStanzaInThinking reports whether a complete tool-calls stanza opening
// has appeared inside a still-unclosed <think> block. The decoder treats DSML
// inside reasoning as non-executable, so without intervention such a turn
// runs to the token limit and the call is dropped at parse time. A caller
// driving generation can recover by force-feeding "</think>\n\n" into the
// session (upstream ds4's think-tool recovery): measured on the real model,
// that position predicts a fresh stanza opening strongly enough that the call
// restarts cleanly on the executable side, while the dangling opening stays
// harmlessly inside reasoning. Only a complete opening triggers — quoted
// fragments and a lone "<" keep decoding untouched. The signal clears once
// decoding leaves the thinking state.
func (d *StreamDecoder) ToolStanzaInThinking() bool {
	return d.state == stateThinking && d.thinkStanza
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
	case stateGLMAfterCall:
		// A completed GLM call followed by end-of-stream: nothing pending.
		d.completeToolBlock(&events)
		d.emitContent(&events, string(d.buf))
	case stateDone:
		// nothing
	default:
		// Unterminated tool structure – replay consumed text and emit rest.
		d.replayConsumedAsContent(&events)
		d.emitContent(&events, string(d.buf))
	}
	d.buf = nil

	msg, err := ParseCompletionSyntax(d.syntaxMode, string(d.fullText), d.thinking)
	return events, msg, err
}

func (d *StreamDecoder) process() []StreamEvent {
	var events []StreamEvent
	for {
		before := len(d.buf)
		stateBefore := d.state
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
		case stateGLMName:
			d.processGLMName(&events)
		case stateGLMArgKey:
			d.processGLMArgKey(&events)
		case stateGLMArgValue:
			d.processGLMArgValue(&events)
		case stateGLMAfterCall:
			d.processGLMAfterCall(&events)
		case stateDone:
			d.buf = nil
		}
		// A handler can transition state without consuming bytes (e.g. a
		// tool block at offset 0 of the content); only stop once neither
		// bytes nor state change, meaning the decoder is waiting for input.
		if len(d.buf) == before && d.state == stateBefore {
			break
		}
	}
	return events
}

// ----------------------------------------------------------------------
// State handlers
// ----------------------------------------------------------------------

func (d *StreamDecoder) processThinking(events *[]StreamEvent) {
	d.scanThinkingForToolStanza()
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

// scanThinkingForToolStanza looks for a complete stanza opening in the
// accumulated text while thinking is still open. Detection works on
// fullText, so marker tokenization and already-emitted reasoning deltas do
// not matter; thinkScanFrom holds back far enough that an opening split
// across future chunks is still seen from its first byte.
func (d *StreamDecoder) scanThinkingForToolStanza() {
	if d.thinkStanza {
		return
	}
	if d.thinkScanFrom > len(d.fullText) {
		d.thinkScanFrom = len(d.fullText)
	}
	region := string(d.fullText[d.thinkScanFrom:])
	if d.syntaxMode == SyntaxGLM {
		if strings.Contains(region, glmToolCallStart) {
			d.thinkStanza = true
			return
		}
	} else {
		for _, syn := range syntaxTable(d.syntaxMode) {
			if strings.Contains(region, syn.toolStart) {
				d.thinkStanza = true
				return
			}
		}
	}
	if hold := len(d.fullText) - thinkStanzaHoldback; hold > d.thinkScanFrom {
		d.thinkScanFrom = hold
	}
}

func (d *StreamDecoder) processContent(events *[]StreamEvent) {
	if d.syntaxMode == SyntaxGLM {
		d.processGLMContent(events)
		return
	}
	eosIdx := bytes.Index(d.buf, []byte(eosToken))
	_, rawStart, syn, implicit, hasTool := findToolBlockStartSyntax(d.syntaxMode, string(d.buf), 0)

	if hasTool && (eosIdx < 0 || rawStart < eosIdx) {
		// Tool block (explicit or implicit) appears before EOS (or EOS absent).
		d.emitContent(events, string(d.buf[:rawStart]))
		d.buf = d.buf[rawStart:]
		d.syntax = syn
		d.implicitBlock = implicit
		d.seenToolCallsOp = implicit // implicit blocks have no wrapper to consume
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

	// Explicit blocks end with </tool_calls>; implicit blocks never see one.
	if !d.implicitBlock && matchComplete(d.buf, d.syntax.toolEnd) {
		d.buf = d.buf[len(d.syntax.toolEnd):]
		d.toolBlockClosed = true
		d.state = stateCheckingToolBlockEnd
		return
	}

	switch classifyTagStart(d.buf, d.syntax.invokeStart) {
	case tagStartYes:
		d.state = stateInInvoke
		return
	case tagStartPartial:
		if !d.finalPass {
			return // wait for the rest of the invoke opener
		}
	}

	// Explicit blocks may still be waiting for </tool_calls>.
	if !d.implicitBlock && !d.finalPass && matchPartial(d.buf, d.syntax.toolEnd) {
		return
	}

	// An implicit block closes at the first non-invoke content once at least
	// one call has been parsed.
	if d.implicitBlock && len(d.calls) > 0 {
		d.toolBlockClosed = true
		d.state = stateCheckingToolBlockEnd
		return
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

	switch classifyTagStart(d.buf, d.syntax.paramStart) {
	case tagStartYes:
		d.state = stateInParameter
		return
	case tagStartPartial:
		if !d.finalPass {
			return
		}
	}

	if !d.finalPass && matchPartial(d.buf, d.syntax.invokeEnd) {
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
			frag.WriteString(toJSONString(unescapeToolText(raw, d.syntax.paramEnd)))
		} else {
			frag.WriteString(raw)
		}
		d.pendingEvents = append(d.pendingEvents, StreamEvent{
			Type:  EventToolCallArgumentsDelta,
			Index: len(d.calls) - 1,
			Delta: frag.String(),
		})
	}

	d.pendingArgs.set(d.paramName, unescapeToolText(raw, d.syntax.paramEnd), d.paramIsString)
	d.state = stateInInvokeBody
}

func (d *StreamDecoder) processCheckingToolBlockEnd(events *[]StreamEvent) {
	// This function handles both explicit tool-block close paths:
	// 1. After consuming an explicit </tool_calls> wrapper (d.seenToolCallsOp is true)
	// 2. From implicit-block close in processInToolCalls (d.implicitBlock && len(d.calls) > 0)
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
		Type:      EventToolCallEnd,
		Index:     len(d.calls) - 1,
		Arguments: tc.Arguments,
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
	d.implicitBlock = false
	d.calls = nil
	d.state = stateContent
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
	d.implicitBlock = false
	d.toolBlockClosed = false
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
// GLM state handlers
//
// Ported from upstream ds4's agent_glm_tool_parse and its streaming renderer.
// GLM has no wrapper block, so a tool call is recognised by <tool_call> alone
// and the "block" is the run of adjacent calls.
// ----------------------------------------------------------------------

func (d *StreamDecoder) processGLMContent(events *[]StreamEvent) {
	eosIdx := bytes.Index(d.buf, []byte(eosToken))
	callIdx := bytes.Index(d.buf, []byte(glmToolCallStart))

	if callIdx >= 0 && (eosIdx < 0 || callIdx < eosIdx) {
		d.emitContent(events, string(d.buf[:callIdx]))
		d.buf = d.buf[callIdx:]
		d.rawBlockStart = len(d.fullText) - len(d.buf)
		d.buf = d.buf[len(glmToolCallStart):]
		d.state = stateGLMName
		return
	}
	if eosIdx >= 0 {
		d.emitContent(events, string(d.buf[:eosIdx]))
		d.buf = d.buf[eosIdx+len(eosToken):]
		d.state = stateDone
		return
	}
	if n := d.safeContentLen(); n > 0 {
		d.emitContent(events, string(d.buf[:n]))
		d.buf = d.buf[n:]
	}
}

// processGLMName reads the bare function name up to the first tag.
func (d *StreamDecoder) processGLMName(events *[]StreamEvent) {
	lt := bytes.IndexByte(d.buf, '<')
	if lt < 0 {
		return // name still arriving
	}
	rest := d.buf[lt:]
	atArg := bytes.HasPrefix(rest, []byte(glmArgKeyStart))
	atClose := bytes.HasPrefix(rest, []byte(glmToolCallEnd))
	if !atArg && !atClose {
		// Wait while either tag is still forming; anything else is malformed.
		if matchPartial(rest, glmArgKeyStart) || matchPartial(rest, glmToolCallEnd) {
			return
		}
		d.enterRawMode(events)
		return
	}
	name := strings.TrimSpace(string(d.buf[:lt]))
	if name == "" {
		d.enterRawMode(events)
		return
	}
	d.pendingEvents = append(d.pendingEvents, StreamEvent{
		Type:  EventToolCallStart,
		Index: len(d.calls),
		Name:  name,
	})
	d.calls = append(d.calls, ToolCall{Name: name})
	d.pendingArgs = newOrderedArgs()
	d.buf = d.buf[lt:]
	d.state = stateGLMArgKey
}

// processGLMArgKey consumes either </tool_call> or one <arg_key>…</arg_key>.
func (d *StreamDecoder) processGLMArgKey(events *[]StreamEvent) {
	if bytes.HasPrefix(d.buf, []byte(glmToolCallEnd)) {
		d.buf = d.buf[len(glmToolCallEnd):]
		d.finishGLMCall()
		d.state = stateGLMAfterCall
		return
	}
	if !bytes.HasPrefix(d.buf, []byte(glmArgKeyStart)) {
		if matchPartial(d.buf, glmArgKeyStart) || matchPartial(d.buf, glmToolCallEnd) {
			return
		}
		d.enterRawMode(events)
		return
	}
	body := d.buf[len(glmArgKeyStart):]
	end := bytes.Index(body, []byte(glmArgKeyEnd))
	if end < 0 {
		return // key still arriving
	}
	key := unescapeToolText(strings.TrimSpace(string(body[:end])), glmArgKeyEnd)
	if key == "" {
		d.enterRawMode(events)
		return
	}
	rest := body[end+len(glmArgKeyEnd):]
	trimmed := skipLeadingWhitespaceBytes(rest)
	if !bytes.HasPrefix(trimmed, []byte(glmArgValueStart)) {
		if matchPartial(trimmed, glmArgValueStart) {
			return
		}
		d.enterRawMode(events)
		return
	}
	d.paramName = key
	d.buf = trimmed[len(glmArgValueStart):]
	d.state = stateGLMArgValue
}

// processGLMArgValue accumulates one argument value up to </arg_value>.
func (d *StreamDecoder) processGLMArgValue(events *[]StreamEvent) {
	end := bytes.Index(d.buf, []byte(glmArgValueEnd))
	if end < 0 {
		return // value still arriving; held whole so the close tag is never split
	}
	// Only the escaped closing delimiter is decoded (ds4_tool_text_unescape);
	// every other entity in an argument is literal payload.
	value := unescapeToolText(string(d.buf[:end]), glmArgValueEnd)
	d.pendingArgs.set(d.paramName, value, true)
	d.pendingEvents = append(d.pendingEvents, StreamEvent{
		Type:  EventToolCallArgumentsDelta,
		Index: len(d.calls) - 1,
		Delta: value,
	})
	d.paramName = ""
	d.buf = d.buf[end+len(glmArgValueEnd):]
	d.state = stateGLMArgKey
}

// processGLMAfterCall accepts whitespace and an immediately adjacent call;
// anything else ends the run (ds4's glm_after_call).
func (d *StreamDecoder) processGLMAfterCall(events *[]StreamEvent) {
	trimmed := skipLeadingWhitespaceBytes(d.buf)
	if bytes.HasPrefix(trimmed, []byte(glmToolCallStart)) {
		d.buf = trimmed[len(glmToolCallStart):]
		d.state = stateGLMName
		return
	}
	if bytes.HasPrefix(trimmed, []byte(eosToken)) {
		d.buf = trimmed[len(eosToken):]
		d.completeToolBlock(events)
		d.state = stateDone
		return
	}
	if len(trimmed) == 0 {
		return
	}
	if !d.finalPass && (matchPartial(trimmed, glmToolCallStart) || matchPartial(trimmed, eosToken)) {
		return // another call may still be forming
	}
	// The strict completion parser rejects prose after the final GLM call.
	// Discard the pending tool events and replay the stanza as content so live
	// streaming agrees with the ParsedMessage returned by Close.
	d.enterRawMode(events)
}

// finishGLMCall closes the in-flight call, recording its arguments.
func (d *StreamDecoder) finishGLMCall() {
	if len(d.calls) == 0 {
		return
	}
	args := d.pendingArgs.buildJSON()
	d.calls[len(d.calls)-1].Arguments = args
	d.pendingEvents = append(d.pendingEvents, StreamEvent{
		Type:      EventToolCallEnd,
		Index:     len(d.calls) - 1,
		Arguments: args,
	})
	d.pendingArgs = newOrderedArgs()
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

// tagStartState classifies how a buffer relates to an open-tag prefix that
// carries a name but no terminating ">" (invokeStart / paramStart).
type tagStartState int

const (
	tagStartNo      tagStartState = iota // definitely not this tag
	tagStartPartial                      // a prefix; wait for more bytes
	tagStartYes                          // the tag, correctly delimited
)

// classifyTagStart decides whether buf begins the named open tag. It requires a
// delimiter after the name so "<｜DSML｜invokeX" is rejected, and waits when the
// delimiter byte has not yet arrived.
func classifyTagStart(buf []byte, prefix string) tagStartState {
	if len(buf) < len(prefix) {
		if strings.HasPrefix(prefix, string(buf)) {
			return tagStartPartial
		}
		return tagStartNo
	}
	if !bytes.HasPrefix(buf, []byte(prefix)) {
		return tagStartNo
	}
	if len(buf) == len(prefix) {
		return tagStartPartial // delimiter byte not yet arrived
	}
	switch buf[len(prefix)] {
	case '>', ' ', '\t', '\r', '\n':
		return tagStartYes
	default:
		return tagStartNo
	}
}

// markerParamCloses lists the parameter close tags of the marker-bearing
// dialects together with the unambiguous anchor (past the bare "</") that must
// be present before greedy sampling kicks in. The plain-XML "</parameter>" is
// intentionally excluded: its "</" is shared with ordinary markup, so values
// that contain HTML/XML stay under the configured sampling settings.
var markerParamCloses = []struct{ closeTag, anchor string }{
	{parameterEndToken, "</" + dsmlMarker},                           // </｜DSML｜parameter>
	{"</" + dsmlMarkerShort + "parameter>", "</" + dsmlMarkerShort},  // </DSML｜parameter>
	{"</" + dsmlMarker + " parameter>", "</" + dsmlMarker},           // </｜DSML｜ parameter> (V4.1)
	{"</" + dsmlMarkerShort + " parameter>", "</" + dsmlMarkerShort}, // </DSML｜ parameter> (V4.1)
}

// lastAngleTail returns buf from its last '<' to the end, or nil if there is no
// '<'. It is the candidate region for a forming DSML marker.
func lastAngleTail(buf []byte) []byte {
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] == '<' {
			return buf[i:]
		}
	}
	return nil
}

// WantsGreedySampling reports whether the next token should be sampled greedily
// (argmax) because the decoder is emitting DSML grammar rather than free text.
// A caller driving generation can consult this before sampling each token to
// keep tool-call structure reliable while leaving parameter values under the
// configured sampling settings — except a parameter's closing tag once it is
// unambiguously DSML. It is derived only from the current decoder state and
// buffered tail, so malformed output, EOS, or the next turn cannot leave a
// caller stuck in greedy mode.
func (d *StreamDecoder) WantsGreedySampling() bool {
	switch d.state {
	case stateInToolCalls, stateInInvoke, stateInInvokeBody, stateInParameter, stateCheckingToolBlockEnd:
		return true
	case stateGLMName, stateGLMArgKey, stateGLMAfterCall:
		return true
	case stateContent:
		return d.contentTailFormsOpener()
	case stateInParameterValue:
		return d.paramTailFormsClose()
	case stateGLMArgValue:
		return matchPartial(lastAngleTail(d.buf), glmArgValueEnd) && len(lastAngleTail(d.buf)) >= 2
	default: // stateThinking, stateRaw, stateDone
		return false
	}
}

// contentTailFormsOpener reports whether the held-back content tail is a forming
// DSML opener (a partial tool_calls or invoke start), so the opener's bytes are
// sampled greedily even while still in content state. A lone "<" (one byte) is
// too common in prose to force argmax.
func (d *StreamDecoder) contentTailFormsOpener() bool {
	tail := lastAngleTail(d.buf)
	if len(tail) < 2 {
		return false
	}
	if d.syntaxMode == SyntaxGLM {
		return matchPartial(tail, glmToolCallStart)
	}
	for _, syn := range syntaxTable(d.syntaxMode) {
		if matchPartial(tail, syn.toolStart) || matchPartial(tail, syn.invokeStart) {
			return true
		}
	}
	return false
}

// paramTailFormsClose reports whether the buffered parameter-value tail is a
// forming parameter close tag that has already reached the unambiguous marker
// anchor (so a bare "</" or ordinary "</div>" does not force argmax).
func (d *StreamDecoder) paramTailFormsClose() bool {
	tail := lastAngleTail(d.buf)
	if len(tail) < 2 {
		return false
	}
	for _, m := range markerParamCloses {
		if len(tail) >= len(m.anchor) && matchPartial(tail, m.closeTag) {
			return true
		}
	}
	return false
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
