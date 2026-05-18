// Package dsml encodes and decodes DeepSeek DSML tool-calling markup.
//
// DSML (the markup DeepSeek V4 uses for structured tool calls) is not part of
// the libds4 chat-template API. This package renders the DSML *fragments* that
// libds4 does not — a "## Tools" system-prompt section and assistant
// tool-call blocks — and parses an assistant completion back into a typed
// ParsedMessage. It does NOT render the surrounding chat envelope (begin/end
// markers, user/assistant roles, thinking tags); libds4's own chat helpers
// own that, and re-implementing it here would duplicate and drift from it.
//
// The package is pure text processing: no FFI, no engine, standard library
// only. Callers compose its output with libds4's chat helpers — append a
// rendered tools section to the system message content, append a rendered
// tool-call block to assistant-history content.
package dsml

import (
	"encoding/json"
	"strings"
)

// DSML markers, ported verbatim from DeepSeek's encoding_dsv4.py.
const (
	dsmlMarker         = "｜DSML｜"
	eosToken           = "<｜end▁of▁sentence｜>"
	thinkingEndToken   = "</think>"
	toolCallsBlockName = "tool_calls"

	// toolCallsStartToken is the prefix that opens a tool-calls block. It
	// intentionally omits the trailing ">" so it matches as a prefix of the
	// rendered "<｜DSML｜tool_calls>" element.
	toolCallsStartToken = "\n\n<" + dsmlMarker + toolCallsBlockName
	toolCallsEndToken   = "</" + dsmlMarker + toolCallsBlockName + ">"
	invokeStartToken    = "<" + dsmlMarker + "invoke"
	invokeEndToken      = "</" + dsmlMarker + "invoke"
	parameterStartToken = "<" + dsmlMarker + "parameter"
	parameterEndToken   = "/" + dsmlMarker + "parameter>"
)

// Tool is an OpenAI-style function/tool schema.
type Tool struct {
	// Name is the tool's callable name. It must not contain a double-quote
	// character.
	Name string
	// Description explains what the tool does.
	Description string
	// Parameters is the JSON Schema for the tool's parameters object.
	// An empty value is rendered as "{}".
	Parameters json.RawMessage
}

// ToolCall is one parsed or to-be-rendered tool invocation.
type ToolCall struct {
	// Name is the invoked tool's name. It must not contain a double-quote
	// character.
	Name string
	// Arguments holds the call arguments as a JSON object string.
	Arguments string
}

// ParsedMessage is the decoded result of one assistant completion.
type ParsedMessage struct {
	// Role is set to "assistant" by ParseCompletion.
	Role string
	// Content is the assistant's user-facing reply, trimmed.
	Content string
	// ReasoningContent is the thinking-mode reasoning block, trimmed. It is
	// empty when ParseCompletion is called with thinking == false.
	ReasoningContent string
	// ToolCalls holds any tool calls the completion requested.
	ToolCalls []ToolCall
}

// readUntilStop scans text from index for the earliest occurrence of any
// string in stops. It returns the index just past the matched stop, the text
// between index and the match, and the matched stop string. If no stop is
// found it returns len(text), the remaining text, and an empty matched value.
func readUntilStop(index int, text string, stops []string) (newIndex int, content string, matched string) {
	minPos := len(text)
	for _, s := range stops {
		pos := indexFrom(text, index, s)
		if pos >= 0 && pos < minPos {
			minPos = pos
			matched = s
		}
	}
	if matched != "" {
		return minPos + len(matched), text[index:minPos], matched
	}
	return len(text), text[index:], ""
}

// indexFrom returns the absolute index of the first occurrence of sub in text
// at or after start, or -1 if sub does not occur or start is out of range.
func indexFrom(text string, start int, sub string) int {
	if start < 0 || start > len(text) {
		return -1
	}
	rel := strings.Index(text[start:], sub)
	if rel < 0 {
		return -1
	}
	return start + rel
}

// toJSONString marshals s into a JSON string literal (with surrounding
// quotes). It never fails for a Go string.
func toJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// boolStr renders a Go bool as the lowercase word DSML expects.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
