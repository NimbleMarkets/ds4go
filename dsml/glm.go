package dsml

import (
	"errors"
	"strings"
)

// GLM tool-call markup. GLM DSA models emit a different tool-call grammar from
// DeepSeek's DSML: no wrapper block, the function name is bare text directly
// after the opening tag, and arguments are <arg_key>/<arg_value> pairs whose
// values are always strings.
//
//	<tool_call>NAME<arg_key>k</arg_key><arg_value>v</arg_value>...</tool_call>
//
// Ported from upstream ds4's agent_glm_tool_parse (ds4_agent.c).
const (
	glmToolCallStart  = "<tool_call>"
	glmToolCallEnd    = "</tool_call>"
	glmArgKeyStart    = "<arg_key>"
	glmArgKeyEnd      = "</arg_key>"
	glmArgValueStart  = "<arg_value>"
	glmArgValueEnd    = "</arg_value>"
	glmObservation    = "<|observation|>"
	glmToolResponseSt = "<tool_response>"
	glmToolResponseEn = "</tool_response>"
)

// Syntax selects the tool-call markup grammar, mirroring upstream ds4's
// agent_tool_syntax. The zero value is DeepSeek's DSML, so callers that predate
// GLM support keep their behaviour.
type Syntax int

const (
	// SyntaxDSML is DeepSeek V4's DSML markup.
	SyntaxDSML Syntax = iota
	// SyntaxGLM is GLM DSA's <tool_call> markup.
	SyntaxGLM
)

// String implements fmt.Stringer using ds4's own syntax labels.
func (s Syntax) String() string {
	if s == SyntaxGLM {
		return "glm"
	}
	return "dsml"
}

// parseGLMCompletion is ParseCompletion for the GLM grammar. The thinking
// contract is shared with DSML: markup is executable only after the final
// </think>, so a call emitted inside an unclosed thinking block is reasoning.
func parseGLMCompletion(text string, thinking bool) (ParsedMessage, error) {
	msg := ParsedMessage{Role: "assistant"}
	searchFrom := 0
	contentBase := 0

	if thinking {
		if end := strings.LastIndex(text, thinkingEndToken); end >= 0 {
			msg.ReasoningContent = strings.TrimSpace(strings.TrimPrefix(text[:end], thinkingStartToken))
			searchFrom = end + len(thinkingEndToken)
			contentBase = searchFrom
		} else {
			msg.ReasoningContent = strings.TrimSpace(strings.TrimPrefix(text, thinkingStartToken))
			return msg, nil
		}
	}

	start := glmFindToolCallStart(text, searchFrom)
	if start < 0 {
		_, content, _ := readUntilStop(contentBase, text, []string{eosToken})
		msg.Content = strings.TrimSpace(content)
		return msg, nil
	}
	if eos := indexFrom(text, contentBase, eosToken); eos >= 0 && eos < start {
		msg.Content = strings.TrimSpace(text[contentBase:eos])
		return msg, nil
	}

	calls, end, err := glmParseToolCalls(start, text)
	if err != nil {
		return rawCompletionMessage(text, err.Error()), nil
	}
	msg.Content = strings.TrimSpace(text[contentBase:start])
	msg.ToolCalls = calls

	_, trailing, _ := readUntilStop(end, text, []string{eosToken})
	if strings.TrimSpace(trailing) != "" {
		return rawCompletionMessage(text, "unexpected text after the GLM tool call"), nil
	}
	return msg, nil
}

// glmParseToolCalls parses one or more adjacent GLM tool calls beginning at the
// opening "<tool_call>" tag at start. It returns the parsed calls and the index
// just past the final "</tool_call>".
//
// Errors mirror ds4's agent_dsml_set_error messages so a malformed call
// produces the same retryable guidance the C agent gives the model.
func glmParseToolCalls(start int, text string) (calls []ToolCall, newIndex int, err error) {
	index := start
	for {
		if !strings.HasPrefix(text[index:], glmToolCallStart) {
			break
		}
		index += len(glmToolCallStart)

		var call ToolCall
		call, index, err = glmParseOneCall(index, text)
		if err != nil {
			return nil, 0, err
		}
		calls = append(calls, call)

		// Adjacent calls may be separated by whitespace; anything else ends the
		// block (ds4's glm_after_call handling).
		next := index
		for next < len(text) && isASCIISpace(text[next]) {
			next++
		}
		if next < len(text) && strings.HasPrefix(text[next:], glmToolCallStart) {
			index = next
			continue
		}
		break
	}
	if len(calls) == 0 {
		return nil, 0, errors.New("GLM tool call without function name")
	}
	return calls, index, nil
}

// glmParseOneCall parses the body of a single call, with index positioned just
// past its "<tool_call>" opening tag.
func glmParseOneCall(index int, text string) (ToolCall, int, error) {
	var call ToolCall

	// The function name is the bare text up to the first tag.
	lt := indexFrom(text, index, "<")
	if lt < 0 {
		return call, 0, errors.New("expected <arg_key> or </tool_call> in GLM tool call")
	}
	if !strings.HasPrefix(text[lt:], glmArgKeyStart) && !strings.HasPrefix(text[lt:], glmToolCallEnd) {
		return call, 0, errors.New("expected <arg_key> or </tool_call> in GLM tool call")
	}
	name := strings.TrimSpace(text[index:lt])
	if name == "" {
		return call, 0, errors.New("GLM tool call without function name")
	}
	call.Name = name
	index = lt

	args := newOrderedArgs()
	for {
		if strings.HasPrefix(text[index:], glmToolCallEnd) {
			index += len(glmToolCallEnd)
			break
		}
		if !strings.HasPrefix(text[index:], glmArgKeyStart) {
			return call, 0, errors.New("expected <arg_key> in GLM tool call")
		}
		index += len(glmArgKeyStart)

		keyEnd := indexFrom(text, index, glmArgKeyEnd)
		if keyEnd < 0 {
			return call, 0, errors.New("unterminated <arg_key> in GLM tool call")
		}
		key := strings.TrimSpace(text[index:keyEnd])
		if key == "" {
			return call, 0, errors.New("empty <arg_key> in GLM tool call")
		}
		index = keyEnd + len(glmArgKeyEnd)
		for index < len(text) && isASCIISpace(text[index]) {
			index++
		}

		if !strings.HasPrefix(text[index:], glmArgValueStart) {
			return call, 0, errors.New("expected <arg_value> in GLM tool call")
		}
		index += len(glmArgValueStart)
		valueEnd := indexFrom(text, index, glmArgValueEnd)
		if valueEnd < 0 {
			return call, 0, errors.New("unterminated <arg_value> in GLM tool call")
		}
		// GLM argument values are always strings (ds4 sets param_is_string).
		args.set(key, text[index:valueEnd], true)
		index = valueEnd + len(glmArgValueEnd)
	}

	call.Arguments = args.buildJSON()
	return call, index, nil
}

// glmFindToolCallStart returns the offset of the first "<tool_call>" at or
// after from, or -1.
func glmFindToolCallStart(text string, from int) int {
	return indexFrom(text, from, glmToolCallStart)
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}
