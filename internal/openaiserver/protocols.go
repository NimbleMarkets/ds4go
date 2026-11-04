package openaiserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func decodeProtocolRequest(w http.ResponseWriter, r *http.Request, protocol string) (chatRequest, error) {
	if protocol == "chat" {
		return decodeChatRequest(w, r, maxChatRequestBytes)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatRequestBytes)
	defer r.Body.Close()
	var raw json.RawMessage
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&raw); err != nil {
		return chatRequest{}, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return chatRequest{}, errors.New("request body must contain one JSON object")
	}
	return translateRequest(raw, protocol)
}
func jsonRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func textMessage(role, text string) chatInputMessage {
	return chatInputMessage{Role: role, Content: jsonRaw(text)}
}

func translateRequest(raw json.RawMessage, protocol string) (chatRequest, error) {
	var req chatRequest
	// Tools have different wire schemas; decode shared scalar controls separately.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return req, errors.New("request must be an object")
	}
	shared := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		if k != "tools" && k != "messages" && k != "tool_choice" {
			shared[k] = v
		}
	}
	if err := json.Unmarshal(jsonRaw(shared), &req); err != nil {
		return req, err
	}
	if protocol == "completion" {
		var prompt string
		if err := json.Unmarshal(fields["prompt"], &prompt); err != nil {
			var prompts []string
			if err := json.Unmarshal(fields["prompt"], &prompts); err != nil || len(prompts) != 1 {
				return req, errors.New("prompt must be a string or a single-element string array")
			}
			prompt = prompts[0]
		}
		req.RawPrompt = &prompt
		return req, nil
	}
	if protocol == "responses" {
		return translateResponses(req, fields)
	}
	return translateAnthropic(req, fields)
}
func translateResponses(req chatRequest, f map[string]json.RawMessage) (chatRequest, error) {
	if value := f["previous_response_id"]; len(value) > 0 && string(value) != "null" && string(value) != `""` {
		return req, errors.New("previous_response_id is not available; replay the previous output and tool results in input")
	}
	if value := f["background"]; string(value) == "true" {
		return req, errors.New("background responses are not supported")
	}
	if v := f["max_output_tokens"]; len(v) > 0 {
		if err := json.Unmarshal(v, &req.MaxTokens); err != nil {
			return req, err
		}
	}
	var instructions string
	if v := f["instructions"]; len(v) > 0 {
		if err := json.Unmarshal(v, &instructions); err != nil {
			return req, err
		}
	}
	if instructions != "" {
		req.Messages = append(req.Messages, textMessage("system", instructions))
	}
	if v := f["reasoning"]; len(v) > 0 {
		var reasoning struct {
			Effort json.RawMessage `json:"effort"`
		}
		if err := json.Unmarshal(v, &reasoning); err != nil {
			return req, err
		}
		req.ReasoningEffort = reasoning.Effort
	}
	var tools []struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	if v := f["tools"]; len(v) > 0 {
		if err := json.Unmarshal(v, &tools); err != nil {
			return req, err
		}
	}
	for _, tool := range tools {
		if tool.Type != "function" {
			return req, fmt.Errorf("unsupported Responses tool type %q", tool.Type)
		}
		req.Tools = append(req.Tools, chatTool{Type: "function", Function: chatToolFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}})
	}
	req.ToolChoice = f["tool_choice"]
	var text string
	if json.Unmarshal(f["input"], &text) == nil {
		req.Messages = append(req.Messages, textMessage("user", text))
		return req, nil
	}
	var items []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Summary []struct {
			Text string `json:"text"`
		} `json:"summary"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(f["input"], &items); err != nil {
		return req, errors.New("input must be a string or an array of message/tool items")
	}
	reasoning := ""
	for _, item := range items {
		switch item.Type {
		case "reasoning":
			for _, part := range item.Summary {
				reasoning += part.Text
			}
		case "function_call":
			call := chatToolCall{ID: item.CallID, Type: "function", Function: chatToolFunction{Name: item.Name, Arguments: item.Arguments}}
			if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == "assistant" {
				req.Messages[n-1].ToolCalls = append(req.Messages[n-1].ToolCalls, call)
				req.Messages[n-1].ReasoningContent += reasoning
			} else {
				msg := textMessage("assistant", "")
				msg.ReasoningContent = reasoning
				msg.ToolCalls = []chatToolCall{call}
				req.Messages = append(req.Messages, msg)
			}
			reasoning = ""

		case "function_call_output":
			content, err := responsesContent(item.Output)
			if err != nil {
				return req, err
			}
			req.Messages = append(req.Messages, chatInputMessage{Role: "tool", ToolCallID: item.CallID, Content: content})
		case "", "message":
			role := item.Role
			if role == "developer" {
				role = "system"
			}
			if role != "user" && role != "assistant" && role != "system" {
				return req, fmt.Errorf("unsupported Responses role %q", role)
			}
			content, err := responsesContent(item.Content)
			if err != nil {
				return req, err
			}
			msg := chatInputMessage{Role: role, Content: content}
			if role == "assistant" {
				msg.ReasoningContent = reasoning
				reasoning = ""
			}
			req.Messages = append(req.Messages, msg)
		default:
			return req, fmt.Errorf("unsupported Responses input type %q", item.Type)
		}
	}
	return req, nil
}
func responsesContent(raw json.RawMessage) (json.RawMessage, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return jsonRaw(text), nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, errors.New("content must be a string or content array")
	}
	out := []any{}
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			out = append(out, map[string]any{"type": "text", "text": p.Text})
		case "input_image":
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]string{"url": p.ImageURL}})
		default:
			return nil, fmt.Errorf("unsupported content type %q", p.Type)
		}
	}
	return jsonRaw(out), nil
}

type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	Source    struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

func anthropicContent(raw json.RawMessage) (json.RawMessage, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return jsonRaw(text), nil
	}
	var parts []anthropicBlock
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, err
	}
	out := []any{}
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": p.Text})
		case "image":
			if p.Source.Type != "base64" {
				return nil, errors.New("only inline base64 images are supported")
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + p.Source.MediaType + ";base64," + p.Source.Data}})
		default:
			return nil, fmt.Errorf("unsupported Anthropic content type %q", p.Type)
		}
	}
	return jsonRaw(out), nil
}
func translateAnthropic(req chatRequest, f map[string]json.RawMessage) (chatRequest, error) {
	if req.MaxTokens <= 0 {
		return req, errors.New("max_tokens must be positive")
	}
	if v := f["system"]; len(v) > 0 {
		content, err := anthropicContent(v)
		if err != nil {
			return req, err
		}
		req.Messages = append(req.Messages, chatInputMessage{Role: "system", Content: content})
	}
	if v := f["stop_sequences"]; len(v) > 0 {
		req.Stop = v
	}
	if v := f["thinking"]; len(v) > 0 {
		var th struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(v, &th); err != nil {
			return req, err
		}
		switch th.Type {
		case "disabled":
			req.ReasoningEffort = jsonRaw("none")
		case "enabled", "adaptive":
			req.ReasoningEffort = jsonRaw("high")
		default:
			return req, fmt.Errorf("unsupported thinking type %q", th.Type)
		}
	}
	if v := f["tool_choice"]; len(v) > 0 {
		var choice struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(v, &choice); err != nil {
			return req, err
		}
		req.ToolChoice = jsonRaw(choice.Type)
	}
	var tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if v := f["tools"]; len(v) > 0 {
		if err := json.Unmarshal(v, &tools); err != nil {
			return req, err
		}
	}
	for _, tool := range tools {
		req.Tools = append(req.Tools, chatTool{Type: "function", Function: chatToolFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema}})
	}
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(f["messages"], &messages); err != nil {
		return req, err
	}
	for _, m := range messages {
		if m.Role != "user" && m.Role != "assistant" {
			return req, fmt.Errorf("unsupported Anthropic role %q", m.Role)
		}
		var text string
		if json.Unmarshal(m.Content, &text) == nil {
			req.Messages = append(req.Messages, textMessage(m.Role, text))
			continue
		}
		var blocks []anthropicBlock
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			return req, err
		}
		msg := textMessage(m.Role, "")
		var content []anthropicBlock
		flush := func() error {
			if len(content) == 0 && len(msg.ToolCalls) == 0 && msg.ReasoningContent == "" {
				return nil
			}
			raw, err := anthropicContent(jsonRaw(content))
			if err != nil {
				return err
			}
			msg.Content = raw
			req.Messages = append(req.Messages, msg)
			msg = textMessage(m.Role, "")
			content = nil
			return nil
		}
		for _, b := range blocks {
			switch b.Type {
			case "thinking":
				if m.Role != "assistant" {
					return req, errors.New("thinking blocks require assistant role")
				}
				msg.ReasoningContent += b.Thinking
			case "tool_use":
				if m.Role != "assistant" {
					return req, errors.New("tool_use requires assistant role")
				}
				if !json.Valid(b.Input) {
					return req, errors.New("tool input must be JSON")
				}
				msg.ToolCalls = append(msg.ToolCalls, chatToolCall{ID: b.ID, Type: "function", Function: chatToolFunction{Name: b.Name, Arguments: string(b.Input)}})
			case "tool_result":
				if m.Role != "user" {
					return req, errors.New("tool_result requires user role")
				}
				if err := flush(); err != nil {
					return req, err
				}
				raw, err := anthropicContent(b.Content)
				if err != nil {
					return req, err
				}
				req.Messages = append(req.Messages, chatInputMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: raw})
			case "text", "image":
				content = append(content, b)
			default:
				return req, fmt.Errorf("unsupported Anthropic block %q", b.Type)
			}
		}
		if err := flush(); err != nil {
			return req, err
		}
	}
	return req, nil
}

func responseItems(out result) []map[string]any {
	items := []map[string]any{}
	if out.Message.ReasoningContent != "" {
		items = append(items, map[string]any{"id": "rs_" + out.ID, "type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": out.Message.ReasoningContent}}})
	}
	if out.Message.Content != "" {
		items = append(items, map[string]any{"id": "msg_" + out.ID, "type": "message", "role": "assistant", "status": responseStatus(out.Finish), "content": []any{map[string]any{"type": "output_text", "text": out.Message.Content, "annotations": []any{}}}})
	}
	for _, call := range out.Message.ToolCalls {
		items = append(items, map[string]any{"id": "fc_" + call.ID, "type": "function_call", "status": "completed", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
	}
	return items
}
func responseStatus(finish string) string {
	if finish == "length" {
		return "incomplete"
	}
	return "completed"
}
func anthropicStop(finish string) string {
	switch finish {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}
func anthropicBlocks(out result) []any {
	blocks := []any{}
	if out.Message.ReasoningContent != "" {
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": out.Message.ReasoningContent})
	}
	if out.Message.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": out.Message.Content})
	}
	for _, c := range out.Message.ToolCalls {
		var input any
		if json.Unmarshal([]byte(c.Function.Arguments), &input) != nil {
			input = map[string]any{}
		}
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Function.Name, "input": input})
	}
	return blocks
}
func completionChunk(out result, text string, finish *string, u *usage) any {
	return map[string]any{"id": out.ID, "object": "text_completion", "created": out.Created, "model": out.Model, "choices": []any{map[string]any{"text": text, "index": 0, "logprobs": nil, "finish_reason": finish}}, "usage": u}
}
func protocolResponse(protocol string, out result) any {
	if protocol == "completion" {
		return completionChunk(out, out.Message.Content, &out.Finish, &out.Usage)
	}
	if protocol == "anthropic" {
		return map[string]any{"id": out.ID, "type": "message", "role": "assistant", "model": out.Model, "content": anthropicBlocks(out), "stop_reason": anthropicFinish(out), "stop_sequence": stopSequence(out), "usage": map[string]int{"input_tokens": out.Usage.PromptTokens, "output_tokens": out.Usage.CompletionTokens}}
	}
	var incomplete any
	if out.Finish == "length" {
		incomplete = map[string]string{"reason": "max_output_tokens"}
	}
	return map[string]any{"id": out.ID, "object": "response", "created_at": out.Created, "status": responseStatus(out.Finish), "model": out.Model, "output": responseItems(out), "error": nil, "incomplete_details": incomplete, "usage": map[string]int{"input_tokens": out.Usage.PromptTokens, "output_tokens": out.Usage.CompletionTokens, "total_tokens": out.Usage.TotalTokens}}
}

// protocolStream translates canonical deltas, retaining stable item IDs and
// indexes from item creation through terminal response events.
type protocolStream struct {
	w                      http.ResponseWriter
	protocol               string
	out                    *result
	started                bool
	sequence               int
	items                  []map[string]any
	textIndex, reasonIndex int
	text, reason           strings.Builder
	anthropicOpen          bool
	anthropicIndex         int
	anthropicKind          string
}

func newProtocolStream(w http.ResponseWriter, protocol string, out *result) *protocolStream {
	return &protocolStream{w: w, protocol: protocol, out: out, textIndex: -1, reasonIndex: -1, anthropicIndex: -1}
}
func (p *protocolStream) event(kind string, fields map[string]any) error {
	fields["type"] = kind
	if p.protocol == "responses" {
		fields["sequence_number"] = p.sequence
		p.sequence++
	}
	return writeSSE(p.w, kind, fields)
}
func (p *protocolStream) emit(d delta) error {
	if !p.started {
		p.started = true
		var err error
		if p.protocol == "responses" {
			err = p.event("response.created", map[string]any{"response": map[string]any{"id": p.out.ID, "object": "response", "created_at": p.out.Created, "status": "in_progress", "model": p.out.Model, "output": []any{}, "error": nil, "incomplete_details": nil, "usage": nil}})
		} else {
			err = p.event("message_start", map[string]any{"message": map[string]any{"id": p.out.ID, "type": "message", "role": "assistant", "model": p.out.Model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": p.out.Usage.PromptTokens, "output_tokens": 0}}})
		}
		if err != nil {
			return err
		}
	}
	if p.protocol == "anthropic" {
		return p.emitAnthropic(d)
	}
	for _, v := range []struct {
		kind, text string
		index      *int
		buffer     *strings.Builder
	}{{"reasoning", d.Reasoning, &p.reasonIndex, &p.reason}, {"message", d.Content, &p.textIndex, &p.text}} {
		if v.text == "" {
			continue
		}
		if *v.index < 0 {
			*v.index = len(p.items)
			id := "msg_" + p.out.ID
			item := map[string]any{"id": id, "type": v.kind, "status": "in_progress", "role": "assistant", "content": []any{}}
			if v.kind == "reasoning" {
				id = "rs_" + p.out.ID
				item = map[string]any{"id": id, "type": "reasoning", "summary": []any{}}
			}
			p.items = append(p.items, item)
			if err := p.event("response.output_item.added", map[string]any{"output_index": *v.index, "item": item}); err != nil {
				return err
			}
			partType, event := "output_text", "response.content_part.added"
			key := "content_index"
			if v.kind == "reasoning" {
				partType, event, key = "summary_text", "response.reasoning_summary_part.added", "summary_index"
			}
			if err := p.event(event, map[string]any{"item_id": id, "output_index": *v.index, key: 0, "part": map[string]any{"type": partType, "text": ""}}); err != nil {
				return err
			}
		}
		v.buffer.WriteString(v.text)
		event, key := "response.output_text.delta", "content_index"
		if v.kind == "reasoning" {
			event, key = "response.reasoning_summary_text.delta", "summary_index"
		}
		if err := p.event(event, map[string]any{"item_id": p.items[*v.index]["id"], "output_index": *v.index, key: 0, "delta": v.text}); err != nil {
			return err
		}
	}
	for _, c := range d.ToolCalls {
		idx := len(p.items)
		id := "fc_" + c.ID
		item := map[string]any{"id": id, "type": "function_call", "call_id": c.ID, "name": c.Function.Name, "arguments": "", "status": "in_progress"}
		p.items = append(p.items, item)
		if err := p.event("response.output_item.added", map[string]any{"output_index": idx, "item": item}); err != nil {
			return err
		}
		if err := p.event("response.function_call_arguments.delta", map[string]any{"item_id": id, "output_index": idx, "delta": c.Function.Arguments}); err != nil {
			return err
		}
		item["arguments"] = c.Function.Arguments
		item["status"] = "completed"
		if err := p.event("response.function_call_arguments.done", map[string]any{"item_id": id, "output_index": idx, "name": c.Function.Name, "arguments": c.Function.Arguments}); err != nil {
			return err
		}
	}
	return nil
}
func (p *protocolStream) closeAnthropic() error {
	if !p.anthropicOpen {
		return nil
	}
	p.anthropicOpen = false
	return p.event("content_block_stop", map[string]any{"index": p.anthropicIndex})
}
func (p *protocolStream) emitAnthropic(d delta) error {
	for _, part := range []struct{ kind, text string }{{"thinking", d.Reasoning}, {"text", d.Content}} {
		if part.text == "" {
			continue
		}
		if !p.anthropicOpen || p.anthropicKind != part.kind {
			if err := p.closeAnthropic(); err != nil {
				return err
			}
			p.anthropicIndex++
			p.anthropicKind = part.kind
			p.anthropicOpen = true
			if err := p.event("content_block_start", map[string]any{"index": p.anthropicIndex, "content_block": map[string]any{"type": part.kind, part.kind: ""}}); err != nil {
				return err
			}
		}
		if err := p.event("content_block_delta", map[string]any{"index": p.anthropicIndex, "delta": map[string]any{"type": part.kind + "_delta", part.kind: part.text}}); err != nil {
			return err
		}
	}
	for _, c := range d.ToolCalls {
		if err := p.closeAnthropic(); err != nil {
			return err
		}
		p.anthropicIndex++
		if err := p.event("content_block_start", map[string]any{"index": p.anthropicIndex, "content_block": map[string]any{"type": "tool_use", "id": c.ID, "name": c.Function.Name, "input": map[string]any{}}}); err != nil {
			return err
		}
		if err := p.event("content_block_delta", map[string]any{"index": p.anthropicIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": c.Function.Arguments}}); err != nil {
			return err
		}
		p.anthropicOpen = true
		if err := p.closeAnthropic(); err != nil {
			return err
		}
	}
	return nil
}
func (p *protocolStream) finish(out result) error {
	if p.protocol == "anthropic" {
		if err := p.closeAnthropic(); err != nil {
			return err
		}
		if err := p.event("message_delta", map[string]any{"delta": map[string]any{"stop_reason": anthropicFinish(out), "stop_sequence": stopSequence(out)}, "usage": map[string]int{"output_tokens": out.Usage.CompletionTokens}}); err != nil {
			return err
		}
		return p.event("message_stop", map[string]any{})
	}
	for _, part := range []struct {
		index      int
		text, kind string
	}{{p.reasonIndex, p.reason.String(), "reasoning"}, {p.textIndex, p.text.String(), "message"}} {
		if part.index < 0 {
			continue
		}
		item := p.items[part.index]
		event, key, partEvent, partType := "response.output_text.done", "content_index", "response.content_part.done", "output_text"
		if part.kind == "reasoning" {
			event, key, partEvent, partType = "response.reasoning_summary_text.done", "summary_index", "response.reasoning_summary_part.done", "summary_text"
		}
		if err := p.event(event, map[string]any{"item_id": item["id"], "output_index": part.index, key: 0, "text": part.text}); err != nil {
			return err
		}
		block := map[string]any{"type": partType, "text": part.text}
		if part.kind == "message" {
			block["annotations"] = []any{}
			item["content"] = []any{block}
			item["status"] = responseStatus(out.Finish)
		} else {
			item["summary"] = []any{block}
		}
		if err := p.event(partEvent, map[string]any{"item_id": item["id"], "output_index": part.index, key: 0, "part": block}); err != nil {
			return err
		}
	}
	for i, item := range p.items {
		if err := p.event("response.output_item.done", map[string]any{"output_index": i, "item": item}); err != nil {
			return err
		}
	}
	response := protocolResponse("responses", out).(map[string]any)
	response["output"] = p.items
	return p.event("response."+responseStatus(out.Finish), map[string]any{"response": response})
}

func anthropicFinish(out result) string {
	if out.StopSequence != "" {
		return "stop_sequence"
	}
	return anthropicStop(out.Finish)
}
func stopSequence(out result) any {
	if out.StopSequence != "" {
		return out.StopSequence
	}
	return nil
}
