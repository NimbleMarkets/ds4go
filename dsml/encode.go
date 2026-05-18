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
// passing the system message to libds4's chat helpers.
func RenderToolsSection(tools []Tool) string {
	schemas := make([]string, len(tools))
	for i, t := range tools {
		params := []byte(t.Parameters)
		if len(params) == 0 || !json.Valid(params) {
			params = []byte("{}")
		}
		schemas[i] = fmt.Sprintf(`{"name": %s, "description": %s, "parameters": %s}`,
			toJSONString(t.Name), toJSONString(t.Description), string(params))
	}
	return fmt.Sprintf(toolsSectionTemplate, strings.Join(schemas, "\n"))
}

// RenderToolCalls renders an assistant "<｜DSML｜tool_calls>" block. The
// caller appends the result to an assistant message's content when replaying
// tool-call history into a multi-turn prompt. It returns "" for no calls.
func RenderToolCalls(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	invokes := make([]string, len(calls))
	for i, c := range calls {
		body := ""
		if args := encodeArguments(c.Arguments); args != "" {
			body = "\n" + args
		}
		invokes[i] = invokeStartToken + " name=\"" + c.Name + "\">" + body + "\n" +
			invokeEndToken + ">"
	}
	return toolCallsStartToken + ">\n" +
		strings.Join(invokes, "\n") +
		"\n" + toolCallsEndToken
}

// encodeArguments renders a tool call's JSON-object arguments string as
// newline-joined "<｜DSML｜parameter>" elements, preserving key order. A value
// that is a JSON string is emitted with string="true" and its unquoted text;
// any other JSON value is emitted with string="false" and its compact JSON.
// Invalid or non-object arguments render as no parameters.
// A value containing the literal parameterEndToken cannot be represented in
// DSML and would corrupt decoding; see the matching note in decode.go. Such a
// value is vanishingly unlikely in real tool arguments.
func encodeArguments(argsJSON string) string {
	pairs, err := orderedJSONPairs(argsJSON)
	if err != nil {
		return ""
	}
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		var s string
		if json.Unmarshal(p.value, &s) == nil {
			lines = append(lines, parameterElement(p.key, s, true))
			continue
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, p.value); err != nil {
			continue
		}
		lines = append(lines, parameterElement(p.key, buf.String(), false))
	}
	return strings.Join(lines, "\n")
}

// parameterElement renders one "<｜DSML｜parameter>" element.
func parameterElement(name, value string, isString bool) string {
	return parameterStartToken + " name=\"" + name + "\" string=\"" +
		boolStr(isString) + "\">" + value + "<" + parameterEndToken
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
