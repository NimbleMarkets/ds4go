package openaiserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/dsml"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
type result struct {
	StopSequence string
	ID           string
	Created      int64
	Model        string
	Message      chatMessage
	Finish       string
	Usage        usage
}
type delta struct {
	Role      string      `json:"role,omitempty"`
	Content   string      `json:"content,omitempty"`
	Reasoning string      `json:"reasoning_content,omitempty"`
	ToolCalls []toolDelta `json:"tool_calls,omitempty"`
}
type toolDelta struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function chatToolFunction `json:"function"`
}
type server struct {
	replay    *dsml.ReplayStore
	engine    *ds4.Engine
	images    *ds4.ImageEncoder
	cfg       *cliopts.ServerConfig
	slots     chan *ds4.Session
	batcher   *decodeScheduler
	handler   http.Handler
	lifetime  context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closing   bool
	requests  sync.WaitGroup
	closeOnce sync.Once
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }
func (s *server) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.cancel()
		s.mu.Unlock()
		s.requests.Wait()
		for i := 0; i < cap(s.slots); i++ {
			session := <-s.slots
			if session != nil {
				session.Close()
			}
		}
	})
}

func newHandler(engine *ds4.Engine, images *ds4.ImageEncoder, cfg *cliopts.ServerConfig) *server {
	lifetime, cancel := context.WithCancel(context.Background())
	s := &server{engine: engine, images: images, cfg: cfg, slots: make(chan *ds4.Session, max(1, cfg.BatchedSession)), lifetime: lifetime, cancel: cancel}
	if !cfg.DisableExactDSMLToolReplay {
		s.replay = dsml.NewReplayStore(cfg.ToolMemoryMaxIDs)
	}
	for i := 0; i < cap(s.slots); i++ {
		s.slots <- nil
	}
	if cfg.BatchedSession > 0 {
		s.batcher = &decodeScheduler{engine: engine, maxBatch: cfg.BatchedSession}
	}

	mux := http.NewServeMux()
	registerModelRoutes(mux, engine, cfg.Ctx, cfg.Tokens, cfg.CORS)
	mux.HandleFunc("/v1/chat/completions", s.chat)
	mux.HandleFunc("/v1/completions", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "completion") })
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "responses") })
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "anthropic") })
	s.handler = mux
	return s
}
func (s *server) chat(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "chat") }
func (s *server) handle(w http.ResponseWriter, r *http.Request, protocol string) {
	if s.cfg.CORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, 405, "method not allowed")
		return
	}
	req, err := decodeProtocolRequest(w, r, protocol)
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			writeError(w, 413, err.Error())
		} else {
			writeError(w, 400, err.Error())
		}
		return
	}
	if _, _, err := requestSampling(req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err := selectedTools(req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err := thinkModeForRequest(req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err := resolveMaxTokens(req, s.cfg.Tokens); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	id, err := newToolCallID()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	out := result{ID: "chatcmpl-" + strings.TrimPrefix(id, "call_"), Created: time.Now().Unix(), Model: req.Model}
	if protocol == "responses" {
		out.ID = "resp_" + strings.TrimPrefix(id, "call_")
	} else if protocol == "anthropic" {
		out.ID = "msg_" + strings.TrimPrefix(id, "call_")
	}
	if out.Model == "" {
		out.Model = defaultModelID(s.engine)
	}
	var emit func(delta) error
	var adapter *protocolStream
	if protocol != "chat" && protocol != "completion" {
		adapter = newProtocolStream(w, protocol, &out)
	}
	started := false
	if req.Stream {
		if _, ok := w.(http.Flusher); !ok {
			writeError(w, 500, "streaming unsupported by response writer")
			return
		}
		emit = func(d delta) error {
			if !started {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("X-Accel-Buffering", "no")
				started = true
			}
			if adapter != nil {
				return adapter.emit(d)
			}
			if protocol == "completion" {
				return writeSSE(w, "", completionChunk(out, d.Content, nil, nil))
			}
			return writeSSE(w, "", chatChunk(out, d, nil, nil))
		}
	}
	err = s.generate(r.Context(), req, &out, emit)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		status := 500
		var ce clientError
		if errors.As(err, &ce) {
			status = 400
		}
		if started {
			if adapter != nil {
				_ = adapter.event("error", map[string]any{"error": map[string]string{"type": "api_error", "message": err.Error()}})
				return
			}
			_ = writeSSE(w, "", map[string]any{"error": map[string]string{"message": err.Error(), "type": "server_error"}})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			w.(http.Flusher).Flush()
		} else {
			writeError(w, status, err.Error())
		}
		return
	}
	if req.Stream {
		if adapter != nil {
			_ = adapter.finish(out)
			return
		}
		if protocol == "completion" {
			_ = writeSSE(w, "", completionChunk(out, "", &out.Finish, nil))
		} else {
			_ = writeSSE(w, "", chatChunk(out, delta{}, &out.Finish, nil))
		}
		if req.StreamOptions.IncludeUsage && protocol == "completion" {
			_ = writeSSE(w, "", completionChunk(out, "", nil, &out.Usage))
		}
		if req.StreamOptions.IncludeUsage && protocol == "chat" {
			_ = writeSSE(w, "", map[string]any{"id": out.ID, "object": "chat.completion.chunk", "created": out.Created, "model": out.Model, "choices": []any{}, "usage": out.Usage})
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
		return
	}
	if protocol != "chat" {
		writeJSON(w, protocolResponse(protocol, out))
		return
	}
	writeJSON(w, map[string]any{"id": out.ID, "object": "chat.completion", "created": out.Created, "model": out.Model, "choices": []any{map[string]any{"index": 0, "message": out.Message, "finish_reason": out.Finish}}, "usage": out.Usage})
}
func defaultModelID(engine *ds4.Engine) string {
	if engine.IsDeepSeek41() || engine.IsQwen4() || engine.IsGLMDSA() {
		return modelIDs(engine)[0]
	}
	if engine.ModelID() == 1 {
		return "deepseek-v4-pro"
	}
	return "deepseek-v4-flash"
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": message, "type": "invalid_request_error"}})
}
func writeSSE(w http.ResponseWriter, event string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err = fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}
func chatChunk(out result, d delta, finish *string, u *usage) any {
	return struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []any  `json:"choices"`
		Usage   *usage `json:"usage,omitempty"`
	}{out.ID, "chat.completion.chunk", out.Created, out.Model, []any{struct {
		Index  int     `json:"index"`
		Delta  delta   `json:"delta"`
		Finish *string `json:"finish_reason"`
	}{0, d, finish}}, u}
}

func (s *server) generate(ctx context.Context, req chatRequest, out *result, emit func(delta) error) error {
	opts, stops, err := requestSampling(req)
	if err != nil {
		return clientError{err}
	}
	tools, err := selectedTools(req)
	if err != nil {
		return clientError{err}
	}
	req.Tools = tools
	think, err := thinkModeForRequest(req)
	if err != nil {
		return clientError{err}
	}
	limit, err := resolveMaxTokens(req, s.cfg.Tokens)
	if err != nil {
		return clientError{err}
	}

	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return context.Canceled
	}
	s.requests.Add(1)
	s.mu.Unlock()
	defer s.requests.Done()
	var session *ds4.Session
	select {
	case session = <-s.slots:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.lifetime.Done():
		return context.Canceled
	}
	reusable := false
	defer func() {
		if !reusable && session != nil {
			session.Close()
			session = nil
		}
		s.slots <- session
	}()
	if session == nil {
		session, err = s.engine.NewSession(s.cfg.Ctx)
		if err != nil {
			return err
		}
	}

	registry, registryErr := requestRegistry(req, s.replay)
	if registryErr != nil {
		return clientError{registryErr}
	}
	var prompt *ds4.Prompt
	if req.RawPrompt != nil {
		tokens, tokenErr := s.engine.TokenizeText(*req.RawPrompt)
		err = tokenErr
		prompt = &ds4.Prompt{Tokens: tokens}
	} else {
		prompt, err = buildRegistryPrompt(s.engine, s.images, req, think, registry)
	}
	if err != nil {
		return err
	}
	defer prompt.Free()
	if prompt.Tokens.Len() >= s.cfg.Ctx-1 {
		return clientError{errors.New("prompt exceeds context length")}
	}
	genCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopShutdown := context.AfterFunc(s.lifetime, cancel)
	defer stopShutdown()
	opts.Context = genCtx
	opts.ThinkMode = think
	opts.MaxTokens = limit
	decoder := dsml.NewStreamDecoderSyntax(ds4.ToolSyntax(s.engine), s.engine.ThinkModeEnabled(think))
	opts.SampleControl = decoder.WantsGreedySampling
	if req.RawPrompt != nil {
		opts.SampleControl = nil
	}
	filter := stopFilter{stops: stops}
	var text strings.Builder
	var outputErr error
	ids := map[int]string{}
	send := func(d delta) {
		if emit != nil && outputErr == nil {
			outputErr = emit(d)
			if outputErr != nil {
				cancel()
			}
		}
	}
	events := func(es []dsml.StreamEvent) {
		for _, e := range es {
			switch e.Type {
			case dsml.EventContentDelta:
				send(delta{Content: e.Delta})
			case dsml.EventReasoningDelta:
				send(delta{Reasoning: e.Delta})
			case dsml.EventToolCallEnd:
				parsed, err := registry.ParseAssistantSyntax(ds4.ToolSyntax(s.engine), text.String(), s.engine.ThinkModeEnabled(think))
				if err != nil || e.Index >= len(parsed.ToolCalls) {
					outputErr = errors.New("streamed tool call could not be decoded")
					cancel()
					return
				}
				call := parsed.ToolCalls[e.Index]
				ids[e.Index] = call.ID
				send(delta{ToolCalls: []toolDelta{{Index: e.Index, ID: call.ID, Type: "function", Function: chatToolFunction{Name: call.Name, Arguments: call.Arguments}}}})

			}
		}
	}
	feed := func(part string) {
		text.WriteString(part)
		if req.RawPrompt != nil {
			send(delta{Content: part})
		} else {
			events(decoder.Write(part))
		}
	}
	opts.OnToken = func(token int) {
		part, err := s.engine.TokenText(token)
		if err != nil {
			outputErr = err
			cancel()
			return
		}
		feed(filter.push(part))
		if filter.stopped {
			cancel()
		}
	}
	out.Usage.PromptTokens = prompt.Tokens.Len()
	send(delta{Role: "assistant"})
	var generated []int
	if req.IgnoreEOS || s.batcher != nil {
		err = s.syncPrompt(genCtx, session, prompt)
		if err == nil {
			for len(generated) < min(limit, s.cfg.Ctx-prompt.Tokens.Len()-1) {
				if genCtx.Err() != nil {
					err = genCtx.Err()
					break
				}

				var token int
				switch {
				case req.IgnoreEOS:
					token = session.ArgmaxIgnoringEOS(think)
				case opts.Temperature <= 0 || opts.SampleControl != nil && opts.SampleControl():
					token = session.Argmax()
				default:
					token = session.Sample(opts.Temperature, opts.TopK, opts.TopP, opts.MinP, &opts.Seed)
				}
				if token < 0 {
					err = errors.New("could not select a token")
					break
				}
				if !req.IgnoreEOS && s.engine.TokenIsStopForThinkMode(token, think) {
					break
				}
				if s.batcher != nil {
					err = s.batcher.eval(genCtx, session, token)
				} else {
					err = session.Eval(token)
				}
				if err != nil {
					break
				}

				generated = append(generated, token)
				opts.OnToken(token)
			}
		}
	} else {
		generated, err = (ds4.Generator{Engine: s.engine, Session: session}).GeneratePrompt(prompt, opts)
	}
	if outputErr != nil {
		return outputErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && !errors.Is(err, ds4.ErrContextFull) && !(filter.stopped && errors.Is(err, context.Canceled)) {
		return err
	}
	feed(filter.close())
	tail, _, parseErr := decoder.Close()
	if parseErr != nil {
		return parseErr
	}
	events(tail)
	if outputErr != nil {
		return outputErr
	}
	contextFull := errors.Is(err, ds4.ErrContextFull)
	err = nil
	if req.RawPrompt != nil {
		out.Message = chatMessage{Role: "assistant", Content: text.String()}
	} else {
		out.Message, err = parseRegistryMessage(s.engine, registry, text.String(), think)
	}
	if err != nil {
		return err
	}
	for i := range out.Message.ToolCalls {
		if id := ids[i]; id != "" {
			if exact, ok := s.replay.Lookup(out.Message.ToolCalls[i].ID); ok {
				if err := s.replay.Remember(id, exact); err != nil {
					return err
				}
			}
			out.Message.ToolCalls[i].ID = id
		}
	}
	out.StopSequence = filter.matched
	out.Finish = "stop"
	if !filter.stopped && (len(generated) >= limit || contextFull || len(generated) >= s.cfg.Ctx-prompt.Tokens.Len()-1) {
		out.Finish = "length"
	}
	if len(out.Message.ToolCalls) > 0 {
		out.Finish = "tool_calls"
	}
	out.Usage = usage{prompt.Tokens.Len(), len(generated), prompt.Tokens.Len() + len(generated)}
	reusable = true
	return nil
}
