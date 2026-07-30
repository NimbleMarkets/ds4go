package dsml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

// glmToolsSectionTemplate is the GLM tools instruction block, lifted from
// upstream ds4's agent_glm_tools_prompt_intro and
// agent_glm_tools_prompt_after_schemas. The single %s is filled with the tool
// schemas as JSON lines.
//
// ds4's version continues with usage rules for the C agent's fixed tool set
// (read/more/edit/bash). Those are omitted here because ds4go's tool set is
// caller-defined; callers that want them can add them to the system message.
var glmToolsSectionTemplate = "# Tools\n\n" +
	"You may call one or more functions to assist with the user query.\n\n" +
	"You are provided with function signatures within <tools></tools> XML tags:\n" +
	"<tools>\n%s\n</tools>\n\n" +
	"For a function call, output the function name and arguments within exactly this XML format:\n" +
	"<tool_call>{function-name}<arg_key>{arg-key-1}</arg_key><arg_value>{arg-value-1}</arg_value>" +
	"<arg_key>{arg-key-2}</arg_key><arg_value>{arg-value-2}</arg_value>...</tool_call>\n\n" +
	"Tool calls are not allowed inside <think></think>; finish thinking before emitting <tool_call>.\n\n" +
	"# Rules\n\n" +
	"- Use strict native GLM tool-call syntax with <tool_call>, <arg_key>, and <arg_value> tags.\n" +
	"- Use the exact tool names and argument keys from the schemas above.\n"

// glmSyntaxReminder is ds4's agent_glm_syntax_reminder.
const glmSyntaxReminder = "GLM tool-call syntax reminder:\n" +
	"<tool_call>$TOOL_NAME<arg_key>$PARAMETER_NAME</arg_key>" +
	"<arg_value>$PARAMETER_VALUE</arg_value></tool_call>\n"

// renderGLMToolsSection renders the GLM tools instruction block.
func renderGLMToolsSection(tools []Tool) (string, error) {
	if len(tools) == 0 {
		return "", nil
	}
	schemas := make([]string, len(tools))
	for i, t := range tools {
		if err := validateTagAttribute("tool name", t.Name); err != nil {
			return "", err
		}
		params := []byte(t.Parameters)
		if len(params) == 0 {
			params = []byte("{}")
		}
		if !json.Valid(params) {
			return "", fmt.Errorf("dsml: tool %q parameters is not valid JSON", t.Name)
		}
		if !jsonIsObject(params) {
			return "", fmt.Errorf("dsml: tool %q parameters must be a JSON object", t.Name)
		}
		schemas[i] = fmt.Sprintf(
			`{"type": "function", "function": {"name": %s, "description": %s, "parameters": %s}}`,
			toJSONString(t.Name), toJSONString(t.Description), string(params))
	}
	return fmt.Sprintf(glmToolsSectionTemplate, strings.Join(schemas, "\n")), nil
}

// renderGLMToolCalls renders assistant tool calls as GLM markup. Arguments are
// emitted in their recorded order; GLM has no type marker, so non-string JSON
// values render as their literal text and parse back as strings.
func renderGLMToolCalls(calls []ToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, c := range calls {
		if err := validateTagAttribute("tool name", c.Name); err != nil {
			return "", err
		}
		b.WriteString(glmToolCallStart)
		b.WriteString(c.Name)

		pairs, err := orderedJSONPairs(c.Arguments)
		if err != nil {
			return "", fmt.Errorf("dsml: tool %q arguments are not a JSON object: %w", c.Name, err)
		}
		for _, p := range pairs {
			if err := validateTagAttribute("argument name", p.key); err != nil {
				return "", err
			}
			var value string
			if json.Unmarshal(p.value, &value) != nil {
				var buf bytes.Buffer
				if err := json.Compact(&buf, p.value); err != nil {
					return "", fmt.Errorf("dsml: could not compact argument %q: %w", p.key, err)
				}
				value = buf.String()
			}
			b.WriteString(glmArgKeyStart)
			b.WriteString(p.key)
			b.WriteString(glmArgKeyEnd)
			b.WriteString(glmArgValueStart)
			b.WriteString(escapeGLMArgValue(value))
			b.WriteString(glmArgValueEnd)
		}
		b.WriteString(glmToolCallEnd)
	}
	return b.String(), nil
}

// escapeGLMArgValue protects the closing sentinel so an argument value cannot
// terminate its own <arg_value> element early, mirroring how ds4 escapes the
// exact closing tag in wrapped payloads.
func escapeGLMArgValue(s string) string {
	return strings.ReplaceAll(s, glmArgValueEnd, "&lt;/arg_value>")
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
