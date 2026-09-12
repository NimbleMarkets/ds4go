package dsml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// toolsSectionTemplate is the "## Tools" instruction block. The single %s is
// filled with the newline-joined tool schemas.
var toolsSectionTemplate = "## Tools\n\n" +
	"You have access to a set of tools to help answer the user question. " +
	"You can invoke tools by writing a \"<" + dsmlMarker + "tool_calls>\" block " +
	"like the following:\n\n" +
	"<" + dsmlMarker + "tool_calls>\n" +
	"<" + dsmlMarker + "invoke name=\"$TOOL_NAME\">\n" +
	"<" + dsmlMarker + "parameter name=\"$PARAMETER_NAME\" string=\"true|false\">$PARAMETER_VALUE</" + dsmlMarker + "parameter>\n" +
	"...\n" +
	"</" + dsmlMarker + "invoke>\n" +
	"<" + dsmlMarker + "invoke name=\"$TOOL_NAME2\">\n" +
	"...\n" +
	"</" + dsmlMarker + "invoke>\n" +
	"</" + dsmlMarker + "tool_calls>\n\n" +
	"String parameters should be specified as raw text and set `string=\"true\"`. " +
	"Preserve characters such as `>`, `&`, and `&&` exactly; never replace normal " +
	"string characters with XML or HTML entity escapes. Only if a string value " +
	"itself contains the exact closing parameter tag `</" + dsmlMarker + "parameter>`, " +
	"write that tag as `&lt;/" + dsmlMarker + "parameter>` inside the value. For all " +
	"other types (numbers, booleans, arrays, objects), pass the value in JSON format " +
	"and set `string=\"false\"`.\n\n" +
	"If thinking_mode is enabled (triggered by <think>), you MUST output your " +
	"complete reasoning inside <think>...</think> BEFORE any tool calls or final " +
	"response.\n\n" +
	"Otherwise, output directly after </think> with tool calls or final response.\n\n" +
	"### Available Tool Schemas\n\n%s\n\n" +
	"You MUST strictly follow the above defined tool name and parameter schemas " +
	"to invoke tool calls. Use the exact parameter names from the schemas."

// RenderToolsSection renders the "## Tools" instruction block for the given
// tools. The caller prepends the result to the system message content before
// passing the system message to libds4's chat helpers. An empty tool list
// renders nothing (an empty string), so callers need not special-case it.
func RenderToolsSection(tools []Tool) (string, error) {
	return RenderToolsSectionSyntax(SyntaxDSML, tools)
}

// RenderToolsSectionSyntax is [RenderToolsSection] for an explicit tool-call
// markup syntax. GLM renders a "# Tools" block with <tools> schemas instead of
// DSML's "## Tools" section.
func RenderToolsSectionSyntax(syntax Syntax, tools []Tool) (string, error) {
	if syntax == SyntaxGLM {
		return renderGLMToolsSection(tools)
	}
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
			canonicalJSONString(t.Name), canonicalJSONString(t.Description),
			canonicalSchemaParams(params))
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
	return invokeStartToken + " name=\"" + dsmlEscapeAttr(call.Name) + "\">" + body + "\n" +
		invokeEndToken, nil
}

// WrapToolCalls wraps rendered invoke blocks in a "<｜DSML｜tool_calls>" block.
func WrapToolCalls(invokes []string) string {
	if len(invokes) == 0 {
		return ""
	}
	return "\n\n<" + dsmlMarker + toolCallsBlockName + ">\n" +
		strings.Join(invokes, "\n") +
		"\n" + toolCallsEndToken
}

// RenderToolCalls renders an assistant "<｜DSML｜tool_calls>" block. The
// caller appends the result to an assistant message's content when replaying
// tool-call history into a multi-turn prompt. It returns "" for no calls.
func RenderToolCalls(calls []ToolCall) (string, error) {
	return RenderToolCallsSyntax(SyntaxDSML, calls)
}

// RenderToolCallsSyntax is [RenderToolCalls] for an explicit tool-call markup
// syntax. GLM renders adjacent "<tool_call>" elements with no wrapper block.
func RenderToolCallsSyntax(syntax Syntax, calls []ToolCall) (string, error) {
	if syntax == SyntaxGLM {
		return renderGLMToolCalls(calls)
	}
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
// Invalid or non-object arguments render as one string parameter named
// "arguments", matching ds4-server's fallback path.
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
	if isString {
		value = escapeParameterText(value)
	} else {
		value = escapeJSONLiteral(value)
	}
	return parameterStartToken + " name=\"" + dsmlEscapeAttr(name) + "\" string=\"" +
		boolStr(isString) + "\">" + value + parameterEndToken, nil
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
	tok, err = dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '}' {
		return nil, fmt.Errorf("dsml: arguments object is not closed")
	}
	if dec.More() {
		return nil, fmt.Errorf("dsml: unexpected trailing JSON content")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("dsml: unexpected trailing JSON content")
		}
		return nil, err
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
// tool outputs to appear in the next user turn. Tool output is treated as data:
// normal '<', '>', '&', DSML text, and control-token-looking text are preserved.
// Only the </tool_result> sentinel, and an already-escaped spelling of it that
// would otherwise decode back into the sentinel, are escaped so the payload
// cannot break out of the wrapper and the escape stays reversible.
//
// DeepSeek V4's rendered DSML format does not include a tool name or call ID in
// <tool_result>; result correlation is positional. When returning results for
// multiple assistant tool calls, emit the result blocks in the same order as the
// assistant's <invoke> blocks.
func RenderToolResult(content string) (string, error) {
	return toolResultStart + escapeToolResultText(content) + toolResultEnd, nil
}

// RenderToolResultParts is the multimodal counterpart of RenderToolResult,
// for tool-result text segments that alternate with images instead of one
// contiguous payload. Each segment is escaped exactly as RenderToolResult
// escapes its single payload, so no segment's text can smuggle a closing
// sentinel; the wrapper markers are applied only to the first and last
// returned segment (both, for a single segment), so together the segments
// still read as one wrapped tool result.
func RenderToolResultParts(texts []string) []string {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = escapeToolResultText(t)
	}
	if len(out) > 0 {
		out[0] = toolResultStart + out[0]
		out[len(out)-1] += toolResultEnd
	}
	return out
}

// RenderAssistantTurn renders one assistant history turn as raw chat-template
// text — role marker, think block, visible content, rendered tool calls, and
// the end-of-sentence terminator — for rendered-chat tokenization, which maps
// the special markers (including ｜DSML｜) to their vocab token ids.
//
// Mirroring upstream ds4's render_chat_prompt_text: when thinking is enabled
// and replayReasoning is true (the turn is in tool context or follows the last
// user turn), the reasoning is re-rendered inside <think>...</think> — for
// reasoning models, tool-call turns must keep their reasoning when replayed,
// and dropping it also breaks KV prefix reuse against the live session.
// Otherwise the think slot is rendered closed, the DeepSeek convention for
// prior-turn reasoning.
func RenderAssistantTurn(content, reasoning, toolCalls string, thinking, replayReasoning bool) string {
	var b strings.Builder
	b.WriteString(assistantToken)
	if thinking && replayReasoning {
		b.WriteString(thinkingStartToken)
		b.WriteString(reasoning)
		b.WriteString(thinkingEndToken)
	} else {
		b.WriteString(thinkingEndToken)
	}
	b.WriteString(content)
	b.WriteString(toolCalls)
	b.WriteString(eosToken)
	return b.String()
}

// ToolSyntaxErrorMessage renders the tool-error payload sent back to the model
// when its DSML tool call could not be parsed, mirroring upstream ds4's
// invalid-DSML error suffix. detail is the parse failure (typically
// ParsedMessage.MalformedReason) and may be empty. The payload is plain text:
// wrap it as a tool result (or send it as a "tool" role message) so the model
// sees the failure where it expects tool output, then retries or answers
// normally.
func ToolSyntaxErrorMessage(detail string) string {
	return ToolSyntaxErrorMessageSyntax(SyntaxDSML, detail)
}

// ToolSyntaxErrorMessageSyntax is [ToolSyntaxErrorMessage] for an explicit
// tool-call markup syntax. The GLM form carries ds4's agent_glm_syntax_reminder
// so the model sees the grammar it is expected to emit.
func ToolSyntaxErrorMessageSyntax(syntax Syntax, detail string) string {
	var b strings.Builder
	if syntax == SyntaxGLM {
		b.WriteString("Tool error: invalid GLM tool call")
		if detail != "" {
			b.WriteString(": ")
			b.WriteString(detail)
		}
		b.WriteString("\nThe previous assistant output was not executed because the tool-call syntax was " +
			"malformed. Emit a new valid tool call, or answer normally if no tool is needed.\n")
		b.WriteString(glmSyntaxReminder)
		return b.String()
	}
	b.WriteString("Tool error: invalid DSML tool call")
	if detail != "" {
		b.WriteString(": ")
		b.WriteString(detail)
	}
	b.WriteString("\nThe previous assistant output was not executed because the DSML syntax was malformed. " +
		"Emit a new valid DSML tool call, or answer normally if no tool is needed.")
	return b.String()
}
