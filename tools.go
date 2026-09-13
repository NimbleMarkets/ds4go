package ds4

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/NimbleMarkets/ds4go/ds4api"
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

// ToolResult is a tool's multimodal output: text and image parts in order.
type ToolResult struct {
	Parts []ContentPart
}

// Text joins the text parts; it errors if any image part is present.
func (r ToolResult) Text() (string, error) {
	var b strings.Builder
	for _, p := range r.Parts {
		if p.Image != nil {
			return "", errors.New("ds4go: tool result carries an image; the caller must use InvokeParts")
		}
		b.WriteString(p.Text)
	}
	return b.String(), nil
}

func (r ToolResult) hasImage() bool {
	for _, p := range r.Parts {
		if p.Image != nil {
			return true
		}
	}
	return false
}

// MultimodalToolHandler is a ToolHandler whose results may include images.
// The registry prefers InvokeParts when a handler implements it.
type MultimodalToolHandler interface {
	ToolHandler
	InvokeParts(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// MultimodalTool binds a schema to a function returning a ToolResult.
type MultimodalTool struct {
	ToolSchema
	Handler func(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// Schema returns the tool schema.
func (t MultimodalTool) Schema() ToolSchema { return t.ToolSchema }

// Invoke runs the handler and returns its text; results with images error.
func (t MultimodalTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	res, err := t.InvokeParts(ctx, args)
	if err != nil {
		return "", err
	}
	return res.Text()
}

// InvokeParts runs the handler.
func (t MultimodalTool) InvokeParts(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	if t.Handler == nil {
		return ToolResult{}, fmt.Errorf("ds4go: tool %q has no handler", t.Name)
	}
	return t.Handler(ctx, args)
}

// ChatMessage is one tool-aware chat turn.
type ChatMessage struct {
	// Role is "system", "user", "assistant", or "tool".
	Role string
	// Content is the plain text content for the message.
	Content string
	// Parts is the ordered multimodal content of a user or tool message:
	// text and images interleaved. When non-empty it replaces Content for
	// prompt rendering. Only user and tool messages may carry images.
	Parts []ContentPart
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

// ErrImagesNeedMultimodalPrompt is returned by the text-only prompt builders
// when a message carries image parts; use BuildChatPromptMultimodal.
var ErrImagesNeedMultimodalPrompt = errors.New("ds4go: history contains images; build the prompt with BuildChatPromptMultimodal")

// messageParts splits a message's Parts into the text segments around each
// image (len(texts) == len(images)+1). Adjacent text parts merge. Only user
// and tool messages may carry images.
func messageParts(msg ChatMessage) (texts []string, images []ImageInput, err error) {
	if len(msg.Parts) == 0 {
		return []string{msg.Content}, nil, nil
	}
	texts = []string{""}
	for i, part := range msg.Parts {
		switch {
		case part.Image != nil && part.Text != "":
			return nil, nil, fmt.Errorf("ds4go: part %d has both text and an image", i)
		case part.Image != nil:
			if msg.Role != "user" && msg.Role != "tool" {
				return nil, nil, fmt.Errorf("ds4go: %s messages cannot carry images", msg.Role)
			}
			images = append(images, *part.Image)
			texts = append(texts, "")
		case part.Text != "":
			texts[len(texts)-1] += part.Text
		default:
			return nil, nil, fmt.Errorf("ds4go: part %d is empty", i)
		}
	}
	return texts, images, nil
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

// RenderToolsSectionSyntax renders the tools section in an explicit tool-call
// markup syntax; see [ToolSyntax].
func (r *ToolRegistry) RenderToolsSectionSyntax(syntax dsml.Syntax) (string, error) {
	return dsml.RenderToolsSectionSyntax(syntax, dsmlToolsFromSchemas(r.Schemas()))
}

// BuildChatPrompt renders a tool-aware chat prompt using ds4's chat helpers.
//
// The tools section is prepended to the system message. If system is empty and
// tools is non-empty, BuildChatPrompt creates a system turn containing only the
// rendered tools section. Tool result messages are rendered as user turns
// containing DSML <tool_result> blocks.
//
// BuildChatPrompt returns ErrImagesNeedMultimodalPrompt if any message in
// history carries image parts; use BuildChatPromptMultimodal instead.
func BuildChatPrompt(engine *Engine, system string, tools []dsml.Tool, history []ChatMessage, think ThinkMode) (*Tokens, error) {
	if historyHasImages(history) {
		return nil, ErrImagesNeedMultimodalPrompt
	}
	prompt, err := buildChatPrompt(engine, nil, system, tools, history, think, renderChatMessage)
	if err != nil {
		return nil, err
	}
	return prompt.Tokens, nil
}

// BuildPrompt renders a tool-aware chat prompt using ds4's chat helpers.
//
// BuildPrompt returns ErrImagesNeedMultimodalPrompt if any message in history
// carries image parts; use BuildPromptMultimodal instead.
func (r *ToolRegistry) BuildPrompt(engine *Engine, system string, history []ChatMessage, think ThinkMode) (*Tokens, error) {
	if historyHasImages(history) {
		return nil, ErrImagesNeedMultimodalPrompt
	}
	prompt, err := buildChatPrompt(engine, nil, system, dsmlToolsFromSchemas(r.Schemas()), history, think, r.renderMessage)
	if err != nil {
		return nil, err
	}
	return prompt.Tokens, nil
}

// BuildChatPromptMultimodal is BuildChatPrompt for histories that may carry
// image parts. images encodes them (nil is allowed only when no message has
// images). The returned Prompt owns the tokens and spans; free it after the
// session has been synced.
func BuildChatPromptMultimodal(engine *Engine, images *ImageEncoder, system string, tools []dsml.Tool, history []ChatMessage, think ThinkMode) (*Prompt, error) {
	return buildChatPrompt(engine, images, system, tools, history, think, nil)
}

// BuildPromptMultimodal is BuildPrompt for histories that may carry image parts.
func (r *ToolRegistry) BuildPromptMultimodal(engine *Engine, images *ImageEncoder, system string, history []ChatMessage, think ThinkMode) (*Prompt, error) {
	return buildChatPrompt(engine, images, system, dsmlToolsFromSchemas(r.Schemas()), history, think, r.renderMessage)
}

// historyHasImages reports whether any message in history carries an image part.
func historyHasImages(history []ChatMessage) bool {
	for _, msg := range history {
		for _, p := range msg.Parts {
			if p.Image != nil {
				return true
			}
		}
	}
	return false
}

// turnRenderInfo carries the per-turn rendering decisions computed from the
// whole transcript: whether think markers are enabled for this prompt and
// whether this assistant turn replays its reasoning.
type turnRenderInfo struct {
	thinking        bool
	replayReasoning bool
	syntax          dsml.Syntax
}

// promptRenderOptions configures renderPromptMessages for one prompt build.
type promptRenderOptions struct {
	thinking    bool
	toolContext bool
	syntax      dsml.Syntax
}

type chatMessageRenderer func(ChatMessage, turnRenderInfo) (renderedChatMessage, error)

// appendThinkPrefix emits the model-family reasoning-effort prefix through
// ds4_chat_append_think_prefix: GLM and DeepSeek V4.1 carry effort as a
// system line, V4 uses its max-effort prefix at ThinkMax. It must be appended
// before the caller's own system message, as ds4_encode_chat_prompt does.
func appendThinkPrefix(engine *Engine, tokens *Tokens, think ThinkMode) error {
	return engine.ChatAppendThinkPrefix(tokens, think)
}

func buildChatPrompt(engine *Engine, images *ImageEncoder, system string, tools []dsml.Tool, history []ChatMessage, think ThinkMode, render chatMessageRenderer) (*Prompt, error) {
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
	if err := appendThinkPrefix(engine, tokens, think); err != nil {
		tokens.Free()
		return nil, err
	}
	syntax := ToolSyntax(engine)
	toolsSection, err := dsml.RenderToolsSectionSyntax(syntax, tools)
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
		thinking:    engine.ThinkModeEnabled(think),
		toolContext: len(tools) > 0 || historyUsesToolContext(history),
		syntax:      syntax,
	})
	if err != nil {
		tokens.Free()
		return nil, err
	}
	prompt := &Prompt{Tokens: tokens}
	for _, msg := range rendered {
		if len(msg.images) > 0 {
			if images == nil {
				prompt.Free()
				return nil, errors.New("ds4go: history contains images but no ImageEncoder was supplied")
			}
			embs := make([]*ds4api.VisionEmbedding, 0, len(msg.images))
			for _, img := range msg.images {
				emb, err := images.Encode(img)
				if err != nil {
					for _, e := range embs {
						e.Free()
					}
					prompt.Free()
					return nil, err
				}
				embs = append(embs, emb)
			}
			spans, err := engine.ChatAppendMultimodalMessage(tokens, msg.role, msg.parts, embs)
			if err != nil {
				for _, e := range embs {
					e.Free()
				}
				prompt.Free()
				return nil, err
			}
			prompt.Images = append(prompt.Images, spans...)
			continue
		}
		if msg.prerendered {
			turn, err := engine.TokenizeRenderedChat(msg.content)
			if err != nil {
				prompt.Free()
				return nil, err
			}
			for _, id := range turn.Slice() {
				tokens.Push(id)
			}
			turn.Free()
			continue
		}
		if err := engine.ChatAppendMessage(tokens, msg.role, msg.content); err != nil {
			prompt.Free()
			return nil, err
		}
	}
	if err := engine.ChatAppendAssistantPrefix(tokens, think); err != nil {
		prompt.Free()
		return nil, err
	}
	return prompt, nil
}

// ToolSyntax reports the tool-call markup grammar an engine's model uses,
// mirroring upstream ds4's agent_tool_syntax_for_engine: GLM DSA shapes emit
// <tool_call> markup, everything else emits DeepSeek DSML.
func ToolSyntax(engine *Engine) dsml.Syntax {
	if engine != nil && engine.IsGLMDSA() {
		return dsml.SyntaxGLM
	}
	if engine != nil && engine.IsDeepSeek41() {
		return dsml.SyntaxDSML41
	}
	return dsml.SyntaxDSML
}

// ParseAssistant parses one assistant completion and assigns stable tool-call IDs.
func (r *ToolRegistry) ParseAssistant(text string, thinking bool) (ChatMessage, error) {
	return r.ParseAssistantSyntax(dsml.SyntaxDSML, text, thinking)
}

// ParseAssistantSyntax is [ToolRegistry.ParseAssistant] for an explicit
// tool-call markup syntax; see [ToolSyntax].
func (r *ToolRegistry) ParseAssistantSyntax(syntax dsml.Syntax, text string, thinking bool) (ChatMessage, error) {
	parsed, err := dsml.ParseCompletionSyntax(syntax, text, thinking)
	if err != nil {
		return ChatMessage{}, err
	}
	if len(parsed.ToolCalls) == 0 && syntax != dsml.SyntaxGLM {
		// A completion truncated by the token limit mid-stanza degrades to
		// plain content under the strict parse. Repair the missing closers
		// and adopt the result only when it actually recovers a call.
		if repaired, ok := dsml.RepairCompletionSyntax(syntax, text); ok {
			if reparsed, rerr := dsml.ParseCompletionSyntax(syntax, repaired, thinking); rerr == nil && len(reparsed.ToolCalls) > 0 {
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
		arguments := call.Arguments
		if syntax == dsml.SyntaxGLM {
			arguments = r.coerceGLMArguments(call.Name, arguments)
		}
		msg.ToolCalls[i] = ToolCall{
			ID:        id,
			Name:      call.Name,
			Arguments: arguments,
		}
		if replay != nil && call.Exact != "" {
			if err := replay.Remember(id, call.Exact); err != nil {
				return ChatMessage{}, err
			}
		}
	}
	return msg, nil
}

// coerceGLMArguments restores the JSON types declared by a registered tool's
// schema. Native GLM markup has no type bit, so dsml correctly parses every
// <arg_value> as a string; ToolRegistry is the first layer that also knows the
// function schema and can distinguish, for example, integer 3 from string "3".
func (r *ToolRegistry) coerceGLMArguments(name, arguments string) string {
	handler, err := r.lookup(name)
	if err != nil {
		return arguments
	}
	var params struct {
		Properties map[string]struct {
			Type json.RawMessage `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(handler.Schema().Parameters, &params); err != nil {
		return arguments
	}
	pairs, ok := orderedRawObject(arguments)
	if !ok {
		return arguments
	}
	changed := false
	for i := range pairs {
		property, exists := params.Properties[pairs[i].key]
		if !exists {
			continue
		}
		var text string
		if json.Unmarshal(pairs[i].value, &text) != nil {
			continue
		}
		if value, ok := coerceJSONString(text, property.Type); ok {
			pairs[i].value = json.RawMessage(value)
			changed = true
		}
	}
	if !changed {
		return arguments
	}
	var out strings.Builder
	out.WriteByte('{')
	for i, pair := range pairs {
		if i > 0 {
			out.WriteString(", ")
		}
		key, _ := json.Marshal(pair.key)
		out.Write(key)
		out.WriteString(": ")
		out.Write(pair.value)
	}
	out.WriteByte('}')
	return out.String()
}

type rawObjectPair struct {
	key   string
	value json.RawMessage
}

func orderedRawObject(raw string) ([]rawObjectPair, bool) {
	if !json.Valid([]byte(raw)) {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var pairs []rawObjectPair
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, false
		}
		name, ok := key.(string)
		if !ok {
			return nil, false
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		pairs = append(pairs, rawObjectPair{key: name, value: value})
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, false
	}
	return pairs, true
}

func coerceJSONString(value string, rawTypes json.RawMessage) (string, bool) {
	var one string
	var types []string
	if err := json.Unmarshal(rawTypes, &one); err == nil {
		types = []string{one}
	} else if json.Unmarshal(rawTypes, &types) != nil {
		return "", false
	}
	// A schema that explicitly permits strings is ambiguous; preserving the
	// model's string is safer than guessing another member of the union.
	if slices.Contains(types, "string") {
		return "", false
	}
	trimmed := strings.TrimSpace(value)
	for _, typ := range types {
		switch typ {
		case "integer":
			if trimmed != "" && json.Valid([]byte(trimmed)) &&
				!strings.ContainsAny(trimmed, ".eE") {
				var n json.Number
				if json.Unmarshal([]byte(trimmed), &n) == nil {
					return trimmed, true
				}
			}
		case "number":
			if trimmed != "" && json.Valid([]byte(trimmed)) {
				var n json.Number
				if json.Unmarshal([]byte(trimmed), &n) == nil {
					return trimmed, true
				}
			}
		case "boolean":
			if trimmed == "true" || trimmed == "false" {
				return trimmed, true
			}
		case "null":
			if trimmed == "null" {
				return trimmed, true
			}
		case "object":
			if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
				return trimmed, true
			}
		case "array":
			if strings.HasPrefix(trimmed, "[") && json.Valid([]byte(trimmed)) {
				return trimmed, true
			}
		}
	}
	return "", false
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
		msg := ChatMessage{Role: "tool", ToolCallID: call.ID}
		if mm, ok := handler.(MultimodalToolHandler); ok {
			res, err := mm.InvokeParts(ctx, json.RawMessage(call.Arguments))
			if err != nil {
				return nil, fmt.Errorf("ds4go: tool %q failed: %w", call.Name, err)
			}
			if res.hasImage() {
				msg.Parts = res.Parts
			} else {
				msg.Content, _ = res.Text()
			}
		} else {
			result, err := handler.Invoke(ctx, json.RawMessage(call.Arguments))
			if err != nil {
				return nil, fmt.Errorf("ds4go: tool %q failed: %w", call.Name, err)
			}
			msg.Content = result
		}
		out = append(out, msg)
	}
	return out, nil
}

func (r *ToolRegistry) renderMessage(msg ChatMessage, turn turnRenderInfo) (renderedChatMessage, error) {
	if msg.Role != "assistant" {
		return renderChatMessage(msg, turn)
	}
	if len(msg.Parts) > 0 {
		// messageParts rejects an image on an assistant message; a text-only
		// Parts collapses into Content the same way renderChatMessage does,
		// so an assistant turn built from Parts still reaches the tool-call
		// rendering below instead of being silently dropped.
		texts, _, err := messageParts(msg)
		if err != nil {
			return renderedChatMessage{}, err
		}
		msg.Content = texts[0]
		msg.Parts = nil
	}
	renderedCalls, err := r.renderAssistantToolCalls(turn.syntax, msg.ToolCalls)
	if err != nil {
		return renderedChatMessage{}, err
	}
	return assistantRendered(msg, renderedCalls, turn), nil
}

func renderChatMessage(msg ChatMessage, turn turnRenderInfo) (renderedChatMessage, error) {
	if len(msg.Parts) > 0 {
		texts, images, err := messageParts(msg)
		if err != nil {
			return renderedChatMessage{}, err
		}
		if len(images) == 0 {
			msg.Content = texts[0]
			msg.Parts = nil
			return renderChatMessage(msg, turn)
		}
		if msg.Role == "tool" && turn.syntax != dsml.SyntaxGLM {
			// DSML: keep the tool-result wrapper around the text so the model
			// still sees a tool observation, escaping each segment exactly as
			// the text-only RenderToolResult path does so tool output cannot
			// close the wrapper early.
			texts = dsml.RenderToolResultParts(texts)
		}
		// Image-bearing turns render under the user role for both families:
		// GLM grounds image tokens in user turns (upstream
		// agent_tool_observation_build), and DeepSeek's tool results are user
		// turns already.
		return renderedChatMessage{role: "user", parts: texts, images: images}, nil
	}
	switch msg.Role {
	case "", "system", "user":
		return renderedChatMessage{role: msg.Role, content: msg.Content}, nil
	case "assistant":
		renderedCalls, err := renderToolCallsSyntax(turn.syntax, msg.ToolCalls)
		if err != nil {
			return renderedChatMessage{}, err
		}
		return assistantRendered(msg, renderedCalls, turn), nil
	case "tool":
		if turn.syntax == dsml.SyntaxGLM {
			// libds4 wraps a GLM tool role in <|observation|><tool_response>,
			// including escaping the closing sentinel, so hand it the raw
			// content under its real role (ds4_chat_append_message).
			return renderedChatMessage{role: "tool", content: msg.Content}, nil
		}
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
	if turn.syntax == dsml.SyntaxGLM {
		// libds4 owns the GLM assistant envelope, including the <think></think>
		// pair it inserts for a non-thinking turn (ds4_chat_append_message), so
		// the content is handed over under its real role rather than
		// pre-rendered as chat-template text.
		content := msg.Content
		if renderedCalls != "" {
			if content != "" {
				content += "\n"
			}
			content += renderedCalls
		}
		return renderedChatMessage{role: "assistant", content: content}
	}
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
	// parts and images are set for a message carrying image parts:
	// len(parts) == len(images)+1, appended with ChatAppendMultimodalMessage.
	parts  []string
	images []ImageInput
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
	if opts.syntax == dsml.SyntaxDSML41 {
		history = orderToolResultsByCall(history)
	}
	out := make([]renderedChatMessage, 0, len(history))
	for i := 0; i < len(history); {
		if history[i].Role == "tool" {
			// DSML coalesces consecutive tool results into one user turn
			// carrying their <tool_result> blocks. GLM keeps each result a
			// separate "tool" turn, because libds4 wraps every one in its own
			// <|observation|><tool_response> element.
			if opts.syntax == dsml.SyntaxGLM {
				rendered, err := render(history[i], turnRenderInfo{syntax: opts.syntax})
				if err != nil {
					return nil, err
				}
				out = append(out, rendered)
				i++
				continue
			}
			var content strings.Builder
			for i < len(history) && history[i].Role == "tool" {
				rendered, err := render(history[i], turnRenderInfo{syntax: opts.syntax})
				if err != nil {
					return nil, err
				}
				if rendered.role != "user" {
					return nil, fmt.Errorf("ds4go: tool message rendered as %q", rendered.role)
				}
				if len(rendered.images) > 0 {
					if content.Len() > 0 {
						out = append(out, renderedChatMessage{role: "user", content: content.String()})
						content.Reset()
					}
					out = append(out, rendered)
					i++
					continue
				}
				content.WriteString(rendered.content)
				i++
			}
			if content.Len() > 0 {
				out = append(out, renderedChatMessage{role: "user", content: content.String()})
			}
			continue
		}

		rendered, err := render(history[i], turnRenderInfo{
			thinking:        opts.thinking,
			replayReasoning: opts.toolContext || i > lastUser,
			syntax:          opts.syntax,
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
	return renderToolCallsSyntax(dsml.SyntaxDSML, calls)
}

func renderToolCallsSyntax(syntax dsml.Syntax, calls []ToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	if syntax == dsml.SyntaxGLM {
		out := make([]dsml.ToolCall, len(calls))
		for i, c := range calls {
			out[i] = dsml.ToolCall{Name: c.Name, Arguments: c.Arguments}
		}
		return dsml.RenderToolCallsSyntax(dsml.SyntaxGLM, out)
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

func (r *ToolRegistry) renderAssistantToolCalls(syntax dsml.Syntax, calls []ToolCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	if syntax == dsml.SyntaxGLM {
		// GLM has no Exact replay block: parsing records no raw stanza, so
		// calls always re-render canonically.
		return renderToolCallsSyntax(syntax, calls)
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
	return renderToolCallsSyntax(syntax, calls)
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

// orderToolResultsByCall returns history with each run of consecutive tool
// results sorted into the order the preceding assistant turn issued its
// calls, mirroring ds4-server's V4.1 renderer (ds41_order_messages): parallel
// tool replies can arrive out of order, and V4.1 expects them in call order.
// Results whose call ID is unknown rank first, as upstream ranks them.
func orderToolResultsByCall(history []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, len(history))
	copy(out, history)
	rank := map[string]int{}
	for i := 0; i < len(out); {
		if out[i].Role == "assistant" && len(out[i].ToolCalls) > 0 {
			rank = map[string]int{}
			for j, call := range out[i].ToolCalls {
				if call.ID != "" {
					rank[call.ID] = j
				}
			}
		}
		if out[i].Role != "tool" {
			i++
			continue
		}
		start := i
		for i < len(out) && out[i].Role == "tool" {
			i++
		}
		run := out[start:i]
		sort.SliceStable(run, func(a, b int) bool {
			return rank[run[a].ToolCallID] < rank[run[b].ToolCallID]
		})
	}
	return out
}
