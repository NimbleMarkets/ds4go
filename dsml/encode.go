package dsml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// toolsSectionTemplate is the "## Tools" instruction block. The single %s is
// filled with the newline-joined tool schemas.
var toolsSectionTemplate = "## Tools\n\n" +
	"You have access to a set of tools to help answer the user's question. " +
	"You can invoke tools by writing a \"<" + dsmlMarker + "tool_calls>\" block " +
	"like the following as part of your reply:\n\n" +
	"<" + dsmlMarker + "tool_calls>\n" +
	"<" + dsmlMarker + "invoke name=\"$TOOL_NAME\">\n" +
	"<" + dsmlMarker + "parameter name=\"$PARAMETER_NAME\" string=\"true|false\">$PARAMETER_VALUE</" + dsmlMarker + "parameter>\n" +
	"...\n" +
	"</" + dsmlMarker + "invoke>\n" +
	"</" + dsmlMarker + "tool_calls>\n\n" +
	"String parameters should be specified as is with string=\"true\". For all " +
	"other types (numbers, booleans, arrays, objects), pass the value in JSON " +
	"format and set string=\"false\".\n\n" +
	"### Available Tool Schemas\n\n%s\n\n" +
	"You MUST strictly follow the tool names and parameter schemas defined above."

// RenderToolsSection renders the "## Tools" instruction block for the given
// tools. The caller appends the result to the system message content before
// passing the system message to libds4's chat helpers. An empty tool list
// renders nothing (an empty string), so callers need not special-case it.
func RenderToolsSection(tools []Tool) (string, error) {
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
		schemas[i] = fmt.Sprintf(`{"name": %s, "description": %s, "parameters": %s}`,
			toJSONString(t.Name), toJSONString(t.Description), string(params))
	}
	return fmt.Sprintf(toolsSectionTemplate, strings.Join(schemas, "\n")), nil
}

// RenderToolCall renders one assistant "<｜DSML｜invoke>" block.
func RenderToolCall(call ToolCall) (string, error) {
	if err := validateTagAttribute("tool name", call.Name); err != nil {
		return "", err
	}
	body, err := encodeArguments(call.Arguments)
	if err != nil {
		return "", err
	}
	if body != "" {
		body = "\n" + body
	}
	return invokeStartToken + " name=\"" + call.Name + "\">" + body + "\n" +
		invokeEndToken + ">", nil
}

// WrapToolCalls wraps rendered invoke blocks in a "<｜DSML｜tool_calls>" block.
func WrapToolCalls(invokes []string) string {
	if len(invokes) == 0 {
		return ""
	}
	return toolCallsStartToken + ">\n" +
		strings.Join(invokes, "\n") +
		"\n" + toolCallsEndToken
}

// RenderToolCalls renders an assistant "<｜DSML｜tool_calls>" block. The
// caller appends the result to an assistant message's content when replaying
// tool-call history into a multi-turn prompt. It returns "" for no calls.
func RenderToolCalls(calls []ToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	invokes := make([]string, len(calls))
	for i, c := range calls {
		invoke, err := RenderToolCall(c)
		if err != nil {
			return "", err
		}
		invokes[i] = invoke
	}
	return WrapToolCalls(invokes), nil
}

// encodeArguments renders a tool call's JSON-object arguments string as
// newline-joined "<｜DSML｜parameter>" elements, preserving key order. A value
// that is a JSON string is emitted with string="true" and its unquoted text;
// any other JSON value is emitted with string="false" and its compact JSON.
// Invalid or non-object arguments render as no parameters.
// A value containing the literal parameterEndToken cannot be represented in
// DSML and would corrupt decoding; see the matching note in decode.go. Such a
// value is vanishingly unlikely in real tool arguments.
func encodeArguments(argsJSON string) (string, error) {
	pairs, err := orderedJSONPairs(argsJSON)
	if err != nil {
		return parameterElement("arguments", argsJSON, true)
	}
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if err := validateTagAttribute("parameter name", p.key); err != nil {
			return "", err
		}
		var s string
		if json.Unmarshal(p.value, &s) == nil {
			line, err := parameterElement(p.key, s, true)
			if err != nil {
				return "", err
			}
			lines = append(lines, line)
			continue
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, p.value); err != nil {
			return "", fmt.Errorf("dsml: could not compact argument %q: %w", p.key, err)
		}
		line, err := parameterElement(p.key, buf.String(), false)
		if err != nil {
			return "", err
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

// parameterElement renders one "<｜DSML｜parameter>" element.
func parameterElement(name, value string, isString bool) (string, error) {
	if err := validateTagAttribute("parameter name", name); err != nil {
		return "", err
	}
	if err := validateParameterValue(value); err != nil {
		return "", err
	}
	return parameterStartToken + " name=\"" + name + "\" string=\"" +
		boolStr(isString) + "\">" + value + "<" + parameterEndToken, nil
}

// jsonPair is one key/value entry of a JSON object, in document order.
type jsonPair struct {
	key   string
	value json.RawMessage
}

// orderedJSONPairs parses a JSON object string into its key/value pairs in
// document order.
func orderedJSONPairs(s string) ([]jsonPair, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("dsml: arguments is not a JSON object")
	}
	var pairs []jsonPair
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("dsml: non-string JSON object key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		pairs = append(pairs, jsonPair{key: key, value: raw})
	}
	return pairs, nil
}

func jsonIsObject(b []byte) bool {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return false
	}
	_, ok := v.(map[string]any)
	return ok
}

// RenderToolResult wraps one tool result payload the way DeepSeek/ds4 expect
// tool outputs to appear in the next user turn. It returns an error when
// content embeds a reserved token — the tool_result delimiters, the DSML
// marker, or the EOS/thinking control tokens — that would let the result
// break out of its wrapper and inject markup into the surrounding prompt.
func RenderToolResult(content string) (string, error) {
	if err := validateToolResultContent(content); err != nil {
		return "", err
	}
	return toolResultStart + content + toolResultEnd, nil
}

// validateToolResultContent rejects tool-result text containing a token that
// could escape the <tool_result> wrapper or inject DSML/control markup.
func validateToolResultContent(content string) error {
	for _, tok := range []string{toolResultStart, toolResultEnd, dsmlMarker, eosToken, thinkingEndToken} {
		if strings.Contains(content, tok) {
			return fmt.Errorf("dsml: tool result content contains reserved token %q", tok)
		}
	}
	return nil
}
