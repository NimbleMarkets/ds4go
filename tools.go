package ds4

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/NimbleMarkets/ds4go/dsml"
)

// ToolSchema describes one Go-exposed tool.
type ToolSchema struct {
	// Name is the tool's callable name.
	Name string
	// Description explains what the tool does.
	Description string
	// Parameters is the JSON Schema object for the tool's arguments.
	Parameters json.RawMessage
}

// ToolFunc is a function-backed tool implementation.
type ToolFunc func(ctx context.Context, args json.RawMessage) (string, error)

// ToolHandler exposes a Go tool to the model.
type ToolHandler interface {
	// Schema returns the public tool schema shown to the model.
	Schema() ToolSchema
	// Invoke executes the tool with the JSON arguments requested by the model.
	Invoke(ctx context.Context, args json.RawMessage) (string, error)
}

// Tool binds a schema to a Go function.
type Tool struct {
	ToolSchema
	Handler ToolFunc
}

// Schema returns the tool schema.
func (t Tool) Schema() ToolSchema { return t.ToolSchema }

// Invoke executes the bound handler.
func (t Tool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Handler == nil {
		return "", fmt.Errorf("ds4go: tool %q has no handler", t.Name)
	}
	return t.Handler(ctx, args)
}

// ChatMessage is one tool-aware chat turn.
type ChatMessage struct {
	// Role is "system", "user", "assistant", or "tool".
	Role string
	// Content is the plain text content for the message.
	Content string
	// ReasoningContent is the assistant reasoning block when thinking mode is enabled.
	ReasoningContent string
	// ToolCalls is the assistant's requested tool calls for this turn.
	ToolCalls []ToolCall
	// ToolCallID associates a tool result message with the call it answers.
	// DSML does not render this ID into <tool_result>; prompt builders expect
	// tool result messages to be ordered to match the assistant's ToolCalls.
	ToolCallID string
	// MalformedReason is the parse failure that degraded an assistant tool
	// stanza to plain content, and is empty for clean turns. ToolLoop uses it
	// to send the model a syntax-error retry turn.
	MalformedReason string
}

// ToolCall is one tool request emitted by the assistant.
type ToolCall struct {
	// ID is the stable tool-call identifier used for exact replay.
	ID string
	// Name is the called tool name.
	Name string
	// Arguments is the JSON argument object string.
	Arguments string
}

// ToolRegistry stores Go-exposed tools and exact sampled DSML replay state.
type ToolRegistry struct {
	mu     sync.RWMutex
	order  []string
	tools  map[string]ToolHandler
	replay *dsml.ReplayStore
	nextID atomic.Uint64
}

// NewToolRegistry creates an empty tool registry with exact DSML replay enabled.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:  make(map[string]ToolHandler),
		replay: dsml.NewReplayStore(100000),
	}
}

// SetReplayStore replaces the exact sampled DSML replay store. Passing nil disables replay.
func (r *ToolRegistry) SetReplayStore(store *dsml.ReplayStore) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replay = store
}

// ReplayStore returns the exact sampled DSML replay store.
func (r *ToolRegistry) ReplayStore() *dsml.ReplayStore {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.replay
}

// Register adds one tool handler to the registry.
func (r *ToolRegistry) Register(handler ToolHandler) error {
	if r == nil {
		return errors.New("ds4go: nil tool registry")
	}
	if handler == nil {
		return errors.New("ds4go: nil tool handler")
	}
	schema := handler.Schema()
	if schema.Name == "" {
		return errors.New("ds4go: tool name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[schema.Name]; exists {
		return fmt.Errorf("ds4go: tool %q already registered", schema.Name)
	}
	r.tools[schema.Name] = handler
	r.order = append(r.order, schema.Name)
	return nil
}

// RegisterFunc adds one function-backed tool to the registry.
func (r *ToolRegistry) RegisterFunc(schema ToolSchema, fn ToolFunc) error {
	return r.Register(Tool{ToolSchema: schema, Handler: fn})
}

// MustRegister adds one tool handler and panics on error.
func (r *ToolRegistry) MustRegister(handler ToolHandler) {
	if err := r.Register(handler); err != nil {
		panic(err)
	}
}

// Schemas returns the registered tool schemas in registration order.
func (r *ToolRegistry) Schemas() []ToolSchema {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolSchema, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name].Schema())
	}
	return out
}

// RenderToolsSection renders the DSML tools section for the registered tools.
func (r *ToolRegistry) RenderToolsSection() (string, error) {
	return dsml.RenderToolsSection(dsmlToolsFromSchemas(r.Schemas()))
}

// BuildChatPrompt renders a tool-aware chat prompt using ds4's chat helpers.
//
// The tools section is prepended to the system message. If system is empty and
// tools is non-empty, BuildChatPrompt creates a system turn containing only the
// rendered tools section. Tool result messages are rendered as user turns
// containing DSML <tool_result> blocks.
func BuildChatPrompt(engine *Engine, system string, tools []dsml.Tool, history []ChatMessage, think ThinkMode) (*Tokens, error) {
	return buildChatPrompt(engine, system, tools, history, think, renderChatMessage)
}

// BuildPrompt renders a tool-aware chat prompt using ds4's chat helpers.
func (r *ToolRegistry) BuildPrompt(engine *Engine, system string, history []ChatMessage, think ThinkMode) (*Tokens, error) {
	return buildChatPrompt(engine, system, dsmlToolsFromSchemas(r.Schemas()), history, think, r.renderMessage)
}

// turnRenderInfo carries the per-turn rendering decisions computed from the
// whole transcript: whether think markers are enabled for this prompt and
// whether this assistant turn replays its reasoning.
type turnRenderInfo struct {
	thinking        bool
	replayReasoning bool
}

// promptRenderOptions configures renderPromptMessages for one prompt build.
type promptRenderOptions struct {
	thinking    bool
	toolContext bool
}

type chatMessageRenderer func(ChatMessage, turnRenderInfo) (renderedChatMessage, error)

func buildChatPrompt(engine *Engine, system string, tools []dsml.Tool, history []ChatMessage, think ThinkMode, render chatMessageRenderer) (*Tokens, error) {
	if engine == nil {
		return nil, errors.New("ds4go: nil engine")
	}
	if render == nil {
		render = renderChatMessage
	}
	tokens, err := engine.NewTokens(nil)
	if err != nil {
		return nil, err
	}
	if err := engine.ChatBegin(tokens); err != nil {
		tokens.Free()
		return nil, err
	}
	if think == ThinkMax {
		if err := engine.ChatAppendMaxEffortPrefix(tokens); err != nil {
			tokens.Free()
			return nil, err
		}
	}
	toolsSection, err := dsml.RenderToolsSection(tools)
	if err != nil {
		tokens.Free()
		return nil, err
	}
	if system != "" || toolsSection != "" {
		content := toolAwareSystemContent(system, toolsSection)
		if err := engine.ChatAppendMessage(tokens, "system", content); err != nil {
			tokens.Free()
			return nil, err
		}
	}
	rendered, err := renderPromptMessages(history, render, promptRenderOptions{
		thinking:    think == ThinkHigh || think == ThinkMax,
		toolContext: len(tools) > 0 || historyUsesToolContext(history),
	})
	if err != nil {
		tokens.Free()
		return nil, err
	}
	for _, msg := range rendered {
		if msg.prerendered {
			turn, err := engine.TokenizeRenderedChat(msg.content)
			if err != nil {
				tokens.Free()
				return nil, err
			}
			for _, id := range turn.Slice() {
				tokens.Push(id)
			}
			turn.Free()
			continue
		}
		if err := engine.ChatAppendMessage(tokens, msg.role, msg.content); err != nil {
			tokens.Free()
			return nil, err
		}
	}
	if err := engine.ChatAppendAssistantPrefix(tokens, think); err != nil {
		tokens.Free()
		return nil, err
	}
	return tokens, nil
}

// ParseAssistant parses one assistant completion and assigns stable tool-call IDs.
func (r *ToolRegistry) ParseAssistant(text string, thinking bool) (ChatMessage, error) {
	parsed, err := dsml.ParseCompletion(text, thinking)
	if err != nil {
		return ChatMessage{}, err
	}
	if len(parsed.ToolCalls) == 0 {
		// A completion truncated by the token limit mid-stanza degrades to
		// plain content under the strict parse. Repair the missing closers
		// and adopt the result only when it actually recovers a call.
		if repaired, ok := dsml.RepairCompletion(text); ok {
			if reparsed, rerr := dsml.ParseCompletion(repaired, thinking); rerr == nil && len(reparsed.ToolCalls) > 0 {
				parsed = reparsed
			}
		}
	}
	msg := ChatMessage{
		Role:             parsed.Role,
		Content:          parsed.Content,
		ReasoningContent: parsed.ReasoningContent,
		ToolCalls:        make([]ToolCall, len(parsed.ToolCalls)),
		MalformedReason:  parsed.MalformedReason,
	}
	replay := r.ReplayStore()
	for i, call := range parsed.ToolCalls {
		id := r.nextToolCallID()
		msg.ToolCalls[i] = ToolCall{
			ID:        id,
			Name:      call.Name,
			Arguments: call.Arguments,
		}
		if replay != nil && call.Exact != "" {
			if err := replay.Remember(id, call.Exact); err != nil {
				return ChatMessage{}, err
			}
		}
	}
	return msg, nil
}

// ExecuteToolCalls invokes the registered Go handlers for the given tool calls.
// It stops and returns an error if ctx is cancelled, if a call names an
// unregistered tool, or if a handler returns an error.
func (r *ToolRegistry) ExecuteToolCalls(ctx context.Context, calls []ToolCall) ([]ChatMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	out := make([]ChatMessage, 0, len(calls))
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		handler, err := r.lookup(call.Name)
		if err != nil {
			return nil, err
		}
		result, err := handler.Invoke(ctx, json.RawMessage(call.Arguments))
		if err != nil {
			return nil, fmt.Errorf("ds4go: tool %q failed: %w", call.Name, err)
		}
		out = append(out, ChatMessage{
			Role:       "tool",
			Content:    result,
			ToolCallID: call.ID,
		})
	}
	return out, nil
}

func (r *ToolRegistry) renderMessage(msg ChatMessage, turn turnRenderInfo) (renderedChatMessage, error) {
	if msg.Role != "assistant" {
		return renderChatMessage(msg, turn)
	}
	renderedCalls, err := r.renderAssistantToolCalls(msg.ToolCalls)
	if err != nil {
		return renderedChatMessage{}, err
	}
	return assistantRendered(msg, renderedCalls, turn), nil
}

func renderChatMessage(msg ChatMessage, turn turnRenderInfo) (renderedChatMessage, error) {
	switch msg.Role {
	case "", "system", "user":
		return renderedChatMessage{role: msg.Role, content: msg.Content}, nil
	case "assistant":
		renderedCalls, err := renderToolCalls(msg.ToolCalls)
		if err != nil {
			return renderedChatMessage{}, err
		}
		return assistantRendered(msg, renderedCalls, turn), nil
	case "tool":
		result, err := dsml.RenderToolResult(msg.Content)
		if err != nil {
			return renderedChatMessage{}, err
		}
		return renderedChatMessage{role: "user", content: result}, nil
	default:
		return renderedChatMessage{}, fmt.Errorf("ds4go: unsupported chat role %q", msg.Role)
	}
}

// assistantRendered renders an assistant history turn as raw chat-template
// text for rendered-chat tokenization. Unlike ChatAppendMessage's plain BPE
// path, this maps the template markers — and the ｜DSML｜ marker inside
// replayed tool calls — to their special vocab tokens, matching what the
// model actually sampled so the session's KV prefix stays reusable.
func assistantRendered(msg ChatMessage, renderedCalls string, turn turnRenderInfo) renderedChatMessage {
	return renderedChatMessage{
		role:        "assistant",
		content:     dsml.RenderAssistantTurn(msg.Content, msg.ReasoningContent, renderedCalls, turn.thinking, turn.replayReasoning),
		prerendered: true,
	}
}

// historyUsesToolContext mirrors upstream's chat_history_uses_tool_context:
// any tool result or assistant tool call in the transcript puts the whole
// prompt in tool context, where assistant reasoning is replayed.
func historyUsesToolContext(history []ChatMessage) bool {
	for _, msg := range history {
		if msg.Role == "tool" || (msg.Role == "assistant" && len(msg.ToolCalls) > 0) {
			return true
		}
	}
	return false
}

type renderedChatMessage struct {
	role    string
	content string
	// prerendered marks content as raw chat-template text (role markers
	// included) that must be tokenized with the rendered-chat tokenizer so
	// special markers map to their vocab token ids, instead of being passed
	// to ChatAppendMessage.
	prerendered bool
}

func toolAwareSystemContent(system string, toolsSection string) string {
	if toolsSection == "" {
		return system
	}
	if system == "" {
		return toolsSection
	}
	return toolsSection + "\n\n" + system
}

func renderPromptMessages(history []ChatMessage, render chatMessageRenderer, opts promptRenderOptions) ([]renderedChatMessage, error) {
	if render == nil {
		render = renderChatMessage
	}
	// Assistant turns after the last user-like turn replay their reasoning
	// even outside tool context, mirroring upstream's last_user_idx rule.
	lastUser := -1
	for i, msg := range history {
		if msg.Role == "user" || msg.Role == "tool" {
			lastUser = i
		}
	}
	out := make([]renderedChatMessage, 0, len(history))
	for i := 0; i < len(history); {
		if history[i].Role == "tool" {
			var content strings.Builder
			for i < len(history) && history[i].Role == "tool" {
				rendered, err := render(history[i], turnRenderInfo{})
				if err != nil {
					return nil, err
				}
				if rendered.role != "user" {
					return nil, fmt.Errorf("ds4go: tool message rendered as %q", rendered.role)
				}
				content.WriteString(rendered.content)
				i++
			}
			out = append(out, renderedChatMessage{role: "user", content: content.String()})
			continue
		}

		rendered, err := render(history[i], turnRenderInfo{
			thinking:        opts.thinking,
			replayReasoning: opts.toolContext || i > lastUser,
		})
		if err != nil {
			return nil, err
		}
		i++
		if rendered.role == "" {
			continue
		}
		out = append(out, rendered)
	}
	return out, nil
}

func renderToolCalls(calls []ToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	invokes := make([]string, len(calls))
	for i, call := range calls {
		invoke, err := dsml.RenderToolCall(dsml.ToolCall{
			Name:      call.Name,
			Arguments: call.Arguments,
		})
		if err != nil {
			return "", err
		}
		invokes[i] = invoke
	}
	return dsml.WrapToolCalls(invokes), nil
}

func (r *ToolRegistry) renderAssistantToolCalls(calls []ToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	replay := r.ReplayStore()
	if replay != nil {
		ids := make([]string, len(calls))
		for i, call := range calls {
			ids[i] = call.ID
		}
		if exact, ok := replay.LookupBlock(ids); ok {
			return exact, nil
		}
	}
	return renderToolCalls(calls)
}

func dsmlToolsFromSchemas(schemas []ToolSchema) []dsml.Tool {
	tools := make([]dsml.Tool, len(schemas))
	for i, schema := range schemas {
		tools[i] = dsml.Tool{
			Name:        schema.Name,
			Description: schema.Description,
			Parameters:  schema.Parameters,
		}
	}
	return tools
}

func (r *ToolRegistry) lookup(name string) (ToolHandler, error) {
	if r == nil {
		return nil, errors.New("ds4go: nil tool registry")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("ds4go: no tool named %q", name)
	}
	return handler, nil
}

func (r *ToolRegistry) nextToolCallID() string {
	if r == nil {
		return ""
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "call_" + hex.EncodeToString(raw[:])
	}
	n := r.nextID.Add(1)
	return "call_" + strconv.FormatUint(n, 10)
}
