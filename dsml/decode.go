package dsml

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// toolNameRe matches the header between "<｜DSML｜invoke" and the next
// element, capturing the tool name.
var toolNameRe = regexp.MustCompile(`^\s*name="([^"]+)">\r?\n$`)

// A parameter value that contains the literal parameterEndToken cannot be
// represented in DSML; such a value fails the paramRe match below.

// paramRe matches a "<｜DSML｜parameter" body up to the closing "<" of its
// end tag, capturing name, the string flag, and the raw value. The (?s) flag
// lets a value span newlines.
var paramRe = regexp.MustCompile(`(?s)^ name="(.*?)" string="(true|false)">(.*?)<$`)

// ParseCompletion parses one assistant completion (raw model output) into a
// ParsedMessage. When thinking is true, a leading reasoning block terminated
// by </think> is required.
//
// The completion may end either with the explicit <｜end▁of▁sentence｜>
// marker or simply at end-of-input: ds4go generation stops on the EOS token
// id before that marker is ever decoded to text, so a missing marker is not
// an error.
func ParseCompletion(text string, thinking bool) (ParsedMessage, error) {
	msg := ParsedMessage{Role: "assistant"}
	idx := 0

	if thinking {
		var stop string
		idx, msg.ReasoningContent, stop = readUntilStop(idx, text, []string{thinkingEndToken, toolCallsStartToken})
		msg.ReasoningContent = strings.TrimSpace(msg.ReasoningContent)
		if stop != thinkingEndToken {
			return ParsedMessage{}, fmt.Errorf("dsml: invalid thinking format: missing %q", thinkingEndToken)
		}
	}

	var stop, content string
	idx, content, stop = readUntilStop(idx, text, []string{eosToken, toolCallsStartToken})
	msg.Content = strings.TrimSpace(content)

	if stop != toolCallsStartToken {
		// stop is either eosToken or "" (end of input); both are valid ends.
		return msg, nil
	}

	calls, newIdx, err := parseToolCalls(idx, text)
	if err != nil {
		return ParsedMessage{}, err
	}
	msg.ToolCalls = calls

	_, trailing, _ := readUntilStop(newIdx, text, []string{eosToken})
	if strings.TrimSpace(trailing) != "" {
		return ParsedMessage{}, fmt.Errorf("dsml: unexpected content after tool calls: %q", trailing)
	}
	return msg, nil
}

// parseToolCalls parses the body of a tool-calls block beginning at index
// (just past the toolCallsStartToken prefix). It returns the parsed calls and
// the index just past the closing tool-calls tag.
func parseToolCalls(index int, text string) (calls []ToolCall, newIndex int, err error) {
	first := true
	for {
		var stop string
		var gap string
		index, gap, stop = readUntilStop(index, text, []string{invokeStartToken, toolCallsEndToken})
		if first {
			if gap != ">\n" {
				return nil, index, fmt.Errorf("dsml: malformed tool_calls start: %q", gap)
			}
			first = false
		} else if gap != "\n" {
			return nil, index, fmt.Errorf("dsml: malformed tool call separator: %q", gap)
		}
		if stop == toolCallsEndToken {
			return calls, index, nil
		}
		if stop == "" {
			return nil, index, fmt.Errorf("dsml: unterminated tool_calls block")
		}

		invokeStart := index - len(invokeStartToken)
		var nameContent string
		index, nameContent, stop = readUntilStop(index, text, []string{parameterStartToken, invokeEndToken})
		nameMatch := toolNameRe.FindStringSubmatch(nameContent)
		if len(nameMatch) != 2 {
			return nil, index, fmt.Errorf("dsml: malformed invoke header: %q", nameContent)
		}

		args := newOrderedArgs()
		for stop == parameterStartToken {
			var paramContent string
			index, paramContent, stop = readUntilStop(index, text, []string{parameterEndToken})
			if stop == "" {
				return nil, index, fmt.Errorf("dsml: unterminated parameter")
			}
			pm := paramRe.FindStringSubmatch(paramContent)
			if len(pm) != 4 {
				return nil, index, fmt.Errorf("dsml: malformed parameter: %q", paramContent)
			}
			if _, seen := args.values[pm[1]]; seen {
				return nil, index, fmt.Errorf("dsml: duplicate parameter name %q", pm[1])
			}
			args.set(pm[1], pm[3], pm[2] == "true")
			index, _, stop = readUntilStop(index, text, []string{parameterStartToken, invokeEndToken})
		}
		if stop != invokeEndToken {
			return nil, index, fmt.Errorf("dsml: unterminated invoke block")
		}
		if index >= len(text) || text[index] != '>' {
			return nil, index, fmt.Errorf("dsml: malformed invoke terminator")
		}
		exact := text[invokeStart : index+1]
		index++

		argsJSON := args.buildJSON()
		if !json.Valid([]byte(argsJSON)) {
			return nil, index, fmt.Errorf("dsml: tool %q produced invalid arguments JSON: %s", nameMatch[1], argsJSON)
		}
		calls = append(calls, ToolCall{Name: nameMatch[1], Arguments: argsJSON, Exact: exact})
	}
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
// a JSON string; otherwise it is taken to be a JSON literal already (number,
// bool, array, object). If a name is recorded twice, the first occurrence
// keeps its position and the value is overwritten.
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
