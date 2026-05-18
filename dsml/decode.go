package dsml

import (
	"encoding/json"
	"fmt"
	"strings"
)

type dsmlSyntax struct {
	toolStart   string
	toolEnd     string
	invokeStart string
	invokeEnd   string
	paramStart  string
	paramEnd    string
}

var dsmlSyntaxes = []dsmlSyntax{
	{
		toolStart:   "<" + dsmlMarker + toolCallsBlockName + ">",
		toolEnd:     toolCallsEndToken,
		invokeStart: invokeStartToken,
		invokeEnd:   invokeEndToken,
		paramStart:  parameterStartToken,
		paramEnd:    parameterEndToken,
	},
	{
		toolStart:   "<" + dsmlMarkerShort + toolCallsBlockName + ">",
		toolEnd:     "</" + dsmlMarkerShort + toolCallsBlockName + ">",
		invokeStart: "<" + dsmlMarkerShort + "invoke",
		invokeEnd:   "</" + dsmlMarkerShort + "invoke>",
		paramStart:  "<" + dsmlMarkerShort + "parameter",
		paramEnd:    "</" + dsmlMarkerShort + "parameter>",
	},
	{
		toolStart:   "<tool_calls>",
		toolEnd:     "</tool_calls>",
		invokeStart: "<invoke",
		invokeEnd:   "</invoke>",
		paramStart:  "<parameter",
		paramEnd:    "</parameter>",
	},
}

// ParseCompletion parses one assistant completion (raw model output) into a
// ParsedMessage.
//
// When thinking is true, DSML is executable only after the final </think>.
// If the model has not closed thinking yet, the text is returned as reasoning
// and no tool calls are parsed. The completion may end either with the explicit
// <｜end▁of▁sentence｜> marker or simply at end-of-input.
func ParseCompletion(text string, thinking bool) (ParsedMessage, error) {
	msg := ParsedMessage{Role: "assistant"}
	searchFrom := 0
	contentBase := 0

	if thinking {
		if end := strings.LastIndex(text, thinkingEndToken); end >= 0 {
			msg.ReasoningContent = strings.TrimSpace(strings.TrimPrefix(text[:end], "<think>"))
			searchFrom = end + len(thinkingEndToken)
			contentBase = searchFrom
		} else {
			msg.ReasoningContent = strings.TrimSpace(strings.TrimPrefix(text, "<think>"))
			return msg, nil
		}
	}

	start, rawStart, syn, ok := findToolBlockStart(text, searchFrom)
	if !ok {
		_, content, _ := readUntilStop(contentBase, text, []string{eosToken})
		msg.Content = strings.TrimSpace(content)
		return msg, nil
	}
	if eos := indexFrom(text, contentBase, eosToken); eos >= 0 && eos < start {
		msg.Content = strings.TrimSpace(text[contentBase:eos])
		return msg, nil
	}

	msg.Content = strings.TrimSpace(text[contentBase:rawStart])
	calls, end, rawBlock, err := parseToolCalls(start, rawStart, text, syn)
	if err != nil {
		return rawCompletionMessage(text), nil
	}
	for i := range calls {
		calls[i].Exact = rawBlock
	}
	msg.ToolCalls = calls

	_, trailing, _ := readUntilStop(end, text, []string{eosToken})
	if strings.TrimSpace(trailing) != "" {
		return rawCompletionMessage(text), nil
	}
	return msg, nil
}

func rawCompletionMessage(text string) ParsedMessage {
	_, content, _ := readUntilStop(0, text, []string{eosToken})
	return ParsedMessage{
		Role:    "assistant",
		Content: strings.TrimSpace(content),
	}
}

func findToolBlockStart(text string, from int) (start int, rawStart int, syn dsmlSyntax, ok bool) {
	best := -1
	var bestSyn dsmlSyntax
	for _, candidate := range dsmlSyntaxes {
		pos := indexFrom(text, from, candidate.toolStart)
		if pos >= 0 && (best < 0 || pos < best) {
			best = pos
			bestSyn = candidate
		}
	}
	if best < 0 {
		return 0, 0, dsmlSyntax{}, false
	}
	raw := best
	if raw >= 2 && text[raw-2:raw] == "\n\n" {
		raw -= 2
	}
	return best, raw, bestSyn, true
}

// parseToolCalls parses a complete tool-calls block beginning at the opening
// "<...tool_calls>" tag. rawStart is the byte offset to preserve for exact
// replay, including any leading "\n\n" separator.
func parseToolCalls(start int, rawStart int, text string, syn dsmlSyntax) (calls []ToolCall, newIndex int, rawBlock string, err error) {
	index := start
	if !strings.HasPrefix(text[index:], syn.toolStart) {
		return nil, index, "", fmt.Errorf("dsml: malformed tool_calls start")
	}
	index += len(syn.toolStart)

	for {
		index = skipASCIIWhitespace(text, index)
		if strings.HasPrefix(text[index:], syn.toolEnd) {
			index += len(syn.toolEnd)
			return calls, index, text[rawStart:index], nil
		}
		if !strings.HasPrefix(text[index:], syn.invokeStart) {
			return nil, index, "", fmt.Errorf("dsml: malformed tool call separator or invoke start")
		}

		tagEnd := strings.IndexByte(text[index:], '>')
		if tagEnd < 0 {
			return nil, index, "", fmt.Errorf("dsml: unterminated invoke header")
		}
		tag := text[index : index+tagEnd+1]
		name, ok := dsmlAttr(tag, "name")
		if !ok || name == "" {
			return nil, index, "", fmt.Errorf("dsml: malformed invoke header: %q", tag)
		}
		index += tagEnd + 1

		args := newOrderedArgs()
		for {
			index = skipASCIIWhitespace(text, index)
			if strings.HasPrefix(text[index:], syn.invokeEnd) {
				index += len(syn.invokeEnd)
				break
			}
			if !strings.HasPrefix(text[index:], syn.paramStart) {
				return nil, index, "", fmt.Errorf("dsml: malformed parameter start")
			}
			tagEnd = strings.IndexByte(text[index:], '>')
			if tagEnd < 0 {
				return nil, index, "", fmt.Errorf("dsml: unterminated parameter header")
			}
			tag = text[index : index+tagEnd+1]
			paramName, ok := dsmlAttr(tag, "name")
			if !ok || paramName == "" {
				return nil, index, "", fmt.Errorf("dsml: malformed parameter: %q", tag)
			}
			isStringText, ok := dsmlAttr(tag, "string")
			if !ok {
				isStringText = "true"
			}
			isString := isStringText != "false"

			valueStart := index + tagEnd + 1
			valueEnd := strings.Index(text[valueStart:], syn.paramEnd)
			if valueEnd < 0 {
				return nil, index, "", fmt.Errorf("dsml: unterminated parameter")
			}
			raw := text[valueStart : valueStart+valueEnd]
			if isString {
				args.set(paramName, dsmlUnescapeText(raw), true)
			} else {
				args.set(paramName, raw, false)
			}
			index = valueStart + valueEnd + len(syn.paramEnd)
		}

		argsJSON := args.buildJSON()
		if !json.Valid([]byte(argsJSON)) {
			return nil, index, "", fmt.Errorf("dsml: tool %q produced invalid arguments JSON: %s", name, argsJSON)
		}
		calls = append(calls, ToolCall{Name: name, Arguments: argsJSON})
	}
}

func skipASCIIWhitespace(text string, index int) int {
	for index < len(text) {
		switch text[index] {
		case ' ', '\t', '\n', '\r':
			index++
		default:
			return index
		}
	}
	return index
}

func dsmlAttr(tag string, name string) (string, bool) {
	pat := name + `="`
	pos := strings.Index(tag, pat)
	if pos < 0 {
		return "", false
	}
	start := pos + len(pat)
	end := strings.IndexByte(tag[start:], '"')
	if end < 0 {
		return "", false
	}
	return dsmlUnescapeText(tag[start : start+end]), true
}

// orderedArgs accumulates tool-call arguments in emission order and renders
// them as a JSON object string.
type orderedArgs struct {
	keys   []string
	values map[string]string // key -> JSON-encoded value fragment
}

func newOrderedArgs() *orderedArgs {
	return &orderedArgs{values: map[string]string{}}
}

// set records one argument. When isString is true the raw value is encoded as
// a JSON string; otherwise it is taken to be a JSON literal already.
func (a *orderedArgs) set(name, raw string, isString bool) {
	if _, seen := a.values[name]; !seen {
		a.keys = append(a.keys, name)
	}
	if isString {
		a.values[name] = toJSONString(raw)
	} else {
		a.values[name] = raw
	}
}

// buildJSON renders the accumulated arguments as a JSON object string, preserving
// emission order.
func (a *orderedArgs) buildJSON() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range a.keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(toJSONString(k))
		b.WriteString(": ")
		b.WriteString(a.values[k])
	}
	b.WriteByte('}')
	return b.String()
}
