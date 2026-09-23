package dsml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Qwen3.8 Flash Next tool-call markup: an XML block whose function and
// parameter names are tag attributes written without quotes and whose values
// sit on their own lines. Adjacent calls repeat the <tool_call> element.
//
//	<tool_call>
//	<function=NAME>
//	<parameter=KEY>
//	VALUE
//	</parameter>
//	</function>
//	</tool_call>
//
// One newline is stripped from each end of a value; a value that reads as a
// JSON object, array, boolean, null, or number is typed as JSON, anything
// else is a string with only its escaped closing delimiter decoded.
// Ported from upstream ds4's agent_qwen_tool_parse (ds4_agent.c) and
// ds4-server's append_qwen_tool_calls_text.
const (
	qwenToolCallStart = "<tool_call>"
	qwenToolCallEnd   = "</tool_call>"
	qwenFnStart       = "<function="
	qwenFnEnd         = "</function>"
	qwenParamStart    = "<parameter="
	qwenParamEnd      = "</parameter>"
)

// qwenToolsSectionTemplate is the Qwen tools instruction block, lifted from
// upstream ds4's agent_qwen_tools_prompt_intro and
// agent_qwen_tools_prompt_after_schemas (ds4-server renders the same block
// without the escape and thinking rules). The single %s is filled with the
// tool schemas as JSON lines. As with GLM, ds4's usage rules for the C
// agent's fixed tool set are omitted; ds4go's tool set is caller-defined.
var qwenToolsSectionTemplate = "# Tools\n\n" +
	"You have access to the following functions:\n\n" +
	"<tools>\n%s\n</tools>\n\n" +
	"Inside string values only, escape a literal </parameter> as &lt;/parameter>. " +
	"To write that escaped spelling literally, use &amp;lt;/parameter>. Other HTML entities are unchanged.\n\n" +
	"If you choose to call a function ONLY reply in the following format with NO suffix:\n\n" +
	"<tool_call>\n<function=example_function_name>\n<parameter=example_parameter_1>\nvalue_1\n</parameter>\n" +
	"<parameter=example_parameter_2>\nThis is the value for the second parameter\nthat can span\nmultiple lines\n" +
	"</parameter>\n</function>\n</tool_call>\n\n<IMPORTANT>\nReminder:\n" +
	"- Function calls MUST follow the specified format: an inner <function=...></function> block must be nested " +
	"within <tool_call></tool_call> XML tags\n" +
	"- Required parameters MUST be specified\n" +
	"- You may provide optional reasoning for your function call in natural language BEFORE the function call, " +
	"but NOT after\n" +
	"- If there is no function call available, answer the question like normal with your current knowledge and " +
	"do not tell the user about function calls\n</IMPORTANT>\n\n" +
	"Tool calls are not allowed inside <think></think>; finish thinking before emitting <tool_call>.\n"

// qwenSyntaxReminder is ds4's agent_qwen_syntax_reminder.
const qwenSyntaxReminder = "Tool-call syntax reminder:\n" +
	"<tool_call>\n<function=$TOOL_NAME>\n<parameter=$PARAMETER_NAME>\n$PARAMETER_VALUE\n</parameter>\n" +
	"</function>\n</tool_call>\n"

// qwenValidateName checks a function or parameter name for the unquoted
// attribute position: ds4's parsers end the name at the first '>' and reject
// a '<' before it.
func qwenValidateName(kind, value string) error {
	if err := validateTagAttribute(kind, value); err != nil {
		return err
	}
	if strings.ContainsAny(value, "<>") {
		return fmt.Errorf("dsml: %s %q contains '<' or '>'", kind, value)
	}
	return nil
}

// renderQwenToolsSection renders the Qwen tools instruction block.
func renderQwenToolsSection(tools []Tool) (string, error) {
	if len(tools) == 0 {
		return "", nil
	}
	schemas := make([]string, len(tools))
	for i, t := range tools {
		if err := qwenValidateName("tool name", t.Name); err != nil {
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
			canonicalJSONString(t.Name), canonicalJSONString(t.Description),
			canonicalSchemaParams(params))
	}
	return fmt.Sprintf(qwenToolsSectionTemplate, strings.Join(schemas, "\n")), nil
}

// renderQwenToolCalls renders assistant tool calls as Qwen markup, mirroring
// ds4-server's append_qwen_tool_calls_text: arguments in recorded order,
// string values as their text and other JSON values compacted, each on its
// own line, and calls separated by a newline. Only the closing delimiter is
// escaped inside a value.
func renderQwenToolCalls(calls []ToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	var b strings.Builder
	for i, c := range calls {
		if err := qwenValidateName("tool name", c.Name); err != nil {
			return "", err
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(qwenToolCallStart)
		b.WriteByte('\n')
		b.WriteString(qwenFnStart)
		b.WriteString(c.Name)
		b.WriteString(">\n")
		pairs, err := orderedJSONPairs(c.Arguments)
		if err != nil {
			return "", fmt.Errorf("dsml: tool %q arguments are not a JSON object: %w", c.Name, err)
		}
		for _, p := range pairs {
			if err := qwenValidateName("argument name", p.key); err != nil {
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
			b.WriteString(qwenParamStart)
			b.WriteString(p.key)
			b.WriteString(">\n")
			b.WriteString(escapeToolText(value, qwenParamEnd))
			b.WriteByte('\n')
			b.WriteString(qwenParamEnd)
			b.WriteByte('\n')
		}
		b.WriteString(qwenFnEnd)
		b.WriteByte('\n')
		b.WriteString(qwenToolCallEnd)
	}
	return b.String(), nil
}

// qwenStripValueNewlines drops the one newline that frames a value on its
// own lines (ds4's qwen_strip_value_newlines).
func qwenStripValueNewlines(v string) string {
	v = strings.TrimPrefix(v, "\n")
	return strings.TrimSuffix(v, "\n")
}

// qwenValueIsJSON mirrors agent_qwen_value_is_json: after trimming, an
// object or array by its brackets, the three literals, or a number that
// fits upstream's 64-byte buffer. A bracketed value must also parse as JSON,
// as ds4-server's qwen_param_value_is_json requires (json_raw_value must
// consume the whole value), and the number must be valid JSON (strtod would
// accept "0x10" or "inf"), so the arguments object stays well formed;
// anything that fails is a string.
func qwenValueIsJSON(v string) bool {
	v = strings.TrimLeft(v, " \t")
	v = strings.TrimRight(v, " \t\n")
	if v == "" {
		return false
	}
	if (v[0] == '{' && v[len(v)-1] == '}') || (v[0] == '[' && v[len(v)-1] == ']') {
		return json.Valid([]byte(v))
	}
	if v == "true" || v == "false" || v == "null" {
		return true
	}
	if len(v) >= 64 {
		return false
	}
	if _, err := strconv.ParseFloat(v, 64); err != nil {
		return false
	}
	return json.Valid([]byte(v))
}

// qwenTrimValue is the trimmed text a JSON-typed value contributes.
func qwenTrimValue(v string) string {
	return strings.TrimRight(strings.TrimLeft(v, " \t"), " \t\n")
}

// parseQwenCompletion is ParseCompletion for the Qwen grammar. The thinking
// contract is shared with the other dialects: markup is executable only after
// the final structural </think>.
func parseQwenCompletion(text string, thinking bool) (ParsedMessage, error) {
	msg := ParsedMessage{Role: "assistant"}
	searchFrom := 0
	contentBase := 0

	if thinking {
		if end := lastStructuralIndex(text, thinkingEndToken); end >= 0 {
			msg.ReasoningContent = strings.TrimSpace(strings.TrimPrefix(text[:end], thinkingStartToken))
			searchFrom = end + len(thinkingEndToken)
			contentBase = searchFrom
		} else {
			msg.ReasoningContent = strings.TrimSpace(strings.TrimPrefix(text, thinkingStartToken))
			return msg, nil
		}
	}

	start := indexFrom(text, searchFrom, qwenToolCallStart)
	if start < 0 {
		_, content, _ := readUntilStop(contentBase, text, []string{eosToken})
		msg.Content = strings.TrimSpace(content)
		return msg, nil
	}
	if eos := indexFrom(text, contentBase, eosToken); eos >= 0 && eos < start {
		msg.Content = strings.TrimSpace(text[contentBase:eos])
		return msg, nil
	}

	calls, end, err := qwenParseToolCalls(start, text)
	if err != nil {
		return rawCompletionMessage(text, err.Error(), thinking), nil
	}
	msg.Content = strings.TrimSpace(text[contentBase:start])
	msg.ToolCalls = calls

	_, trailing, _ := readUntilStop(end, text, []string{eosToken})
	if strings.TrimSpace(trailing) != "" {
		return rawCompletionMessage(text, "unexpected text after the Qwen tool call", thinking), nil
	}
	return msg, nil
}

// qwenParseToolCalls parses one or more adjacent Qwen tool calls beginning at
// the opening "<tool_call>" tag at start. It returns the parsed calls and the
// index just past the final "</tool_call>". Errors carry ds4's
// agent_dsml_set_error wording so the model gets the same retry guidance.
func qwenParseToolCalls(start int, text string) (calls []ToolCall, newIndex int, err error) {
	index := start
	for {
		if !strings.HasPrefix(text[index:], qwenToolCallStart) {
			break
		}
		index += len(qwenToolCallStart)
		var call ToolCall
		call, index, err = qwenParseOneCall(index, text)
		if err != nil {
			return nil, 0, err
		}
		calls = append(calls, call)
		next := index
		for next < len(text) && isASCIISpace(text[next]) {
			next++
		}
		if next < len(text) && strings.HasPrefix(text[next:], qwenToolCallStart) {
			index = next
			continue
		}
		break
	}
	if len(calls) == 0 {
		return nil, 0, errors.New("Qwen tool call without function name")
	}
	return calls, index, nil
}

func qwenSkipSpace(text string, index int) int {
	for index < len(text) && isASCIISpace(text[index]) {
		index++
	}
	return index
}

// qwenParseOneCall parses the body of a single call, with index positioned
// just past its "<tool_call>" opening tag.
func qwenParseOneCall(index int, text string) (ToolCall, int, error) {
	var call ToolCall
	index = qwenSkipSpace(text, index)
	if !strings.HasPrefix(text[index:], qwenFnStart) {
		return call, 0, errors.New("expected <function=...> in Qwen tool call")
	}
	index += len(qwenFnStart)
	gt := strings.IndexAny(text[index:], "<>")
	if gt < 0 || text[index+gt] != '>' {
		return call, 0, errors.New("expected <function=...> in Qwen tool call")
	}
	name := strings.TrimSpace(text[index : index+gt])
	if name == "" {
		return call, 0, errors.New("Qwen tool call without function name")
	}
	call.Name = name
	index += gt + 1

	args := newOrderedArgs()
	for {
		index = qwenSkipSpace(text, index)
		if strings.HasPrefix(text[index:], qwenFnEnd) {
			index += len(qwenFnEnd)
			break
		}
		if !strings.HasPrefix(text[index:], qwenParamStart) {
			return call, 0, errors.New("expected <parameter=...> or </function> in Qwen tool call")
		}
		index += len(qwenParamStart)
		gt := strings.IndexAny(text[index:], "<>")
		if gt < 0 || text[index+gt] != '>' {
			return call, 0, errors.New("expected <parameter=...> or </function> in Qwen tool call")
		}
		key := strings.TrimSpace(text[index : index+gt])
		if key == "" {
			return call, 0, errors.New("empty <parameter=> name in Qwen tool call")
		}
		index += gt + 1
		// Only the parameter delimiter ends its value; tool and think
		// markers inside argument data are literal text.
		end := indexFrom(text, index, qwenParamEnd)
		if end < 0 {
			return call, 0, errors.New("unterminated <parameter=> value in Qwen tool call")
		}
		value := qwenStripValueNewlines(text[index:end])
		if qwenValueIsJSON(value) {
			args.set(key, qwenTrimValue(value), false)
		} else {
			args.set(key, unescapeToolText(value, qwenParamEnd), true)
		}
		index = end + len(qwenParamEnd)
	}
	index = qwenSkipSpace(text, index)
	if !strings.HasPrefix(text[index:], qwenToolCallEnd) {
		return call, 0, errors.New("expected </tool_call> after </function>")
	}
	index += len(qwenToolCallEnd)
	call.Arguments = args.buildJSON()
	return call, index, nil
}
