package openaiserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

// modelMetadata follows ds4-server's discovery schema. Capabilities describe
// the loaded engine, including for compatibility aliases; aliases never load
// another model. Parameter discovery describes this HTTP implementation.
type modelMetadata struct {
	ID                  string            `json:"id"`
	Object              string            `json:"object"`
	Created             int64             `json:"created"`
	OwnedBy             string            `json:"owned_by"`
	Name                string            `json:"name"`
	ContextLength       int               `json:"context_length"`
	Architecture        modelArchitecture `json:"architecture"`
	TopProvider         modelProvider     `json:"top_provider"`
	SupportedParameters []string          `json:"supported_parameters"`
}
type modelArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}
type modelProvider struct {
	ContextLength       int  `json:"context_length"`
	MaxCompletionTokens int  `json:"max_completion_tokens"`
	IsModerated         bool `json:"is_moderated"`
}
type modelList struct {
	Object string          `json:"object"`
	Data   []modelMetadata `json:"data"`
}

func modelIDs(engine *ds4api.Engine) []string {
	switch {
	case engine.IsDeepSeek41():
		return []string{"deepseek-v4.1-flash"}
	case engine.IsQwen4():
		return modelVariants("qwen3.8-flash-next")
	case engine.IsGLM53():
		return modelVariants("glm-5.3-flash")
	case engine.IsGLMDSA():
		return modelVariants("glm-5.2")
	default:
		return []string{"deepseek-v4-flash", "deepseek-v4-pro"}
	}
}
func modelVariants(base string) []string { return []string{base, base + "-chat", base + "-reasoner"} }

// Upstream allows individual lookup of known aliases from any family, all
// reporting the one loaded engine. Namespaced aliases contain a literal slash.
func knownModelAlias(id string) bool {
	switch id {
	case "deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4.1-flash":
		return true
	}
	for _, family := range []struct{ base, namespace string }{
		{"qwen3.8-flash-next", "qwen/"}, {"glm-5.2", "zai/"}, {"glm-5.3-flash", "zai/"},
	} {
		for _, suffix := range []string{"", "-chat", "-reasoner"} {
			if id == family.base+suffix || id == family.namespace+family.base+suffix {
				return true
			}
		}
		if id == family.base+"-no-think" || id == family.base+"-nothink" {
			return true
		}
	}
	return false
}

func modelAliasDisablesThinking(id string) bool {
	return id == "deepseek-chat" || knownModelAlias(id) &&
		(strings.HasSuffix(id, "-chat") || strings.HasSuffix(id, "-no-think") || strings.HasSuffix(id, "-nothink"))
}

func registerModelRoutes(mux *http.ServeMux, engine *ds4api.Engine, ctx, tokens int, cors bool) {
	// Snapshot immutable startup metadata so discovery doesn't wait on the
	// process-global FFI lock while a generation call is running.
	input := []string{"text"}
	if engine.HasVision() {
		input = append(input, "image")
	}
	template := modelMetadata{
		Object: "model", Created: 1767225600, OwnedBy: "ds4go", Name: engine.ModelName(), ContextLength: ctx,
		Architecture:        modelArchitecture{InputModalities: input, OutputModalities: []string{"text"}},
		TopProvider:         modelProvider{ContextLength: ctx, MaxCompletionTokens: min(ctx, tokens)},
		SupportedParameters: []string{"tools", "tool_choice", "max_tokens", "max_completion_tokens", "temperature", "top_p", "top_k", "min_p", "ignore_eos", "stop", "seed", "stream", "reasoning_effort", "chat_template_kwargs"},
	}
	list := modelList{Object: "list"}
	for _, id := range modelIDs(engine) {
		entry := template
		entry.ID = id
		list.Data = append(list.Data, entry)
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		if cors {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET, OPTIONS")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var response any = list
		if r.URL.Path != "/v1/models" {
			id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
			if !knownModelAlias(id) {
				http.NotFound(w, r)
				return
			}
			entry := template
			entry.ID = id
			response = entry
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
	mux.HandleFunc("/v1/models", handler)
	mux.HandleFunc("/v1/models/", handler)
}
