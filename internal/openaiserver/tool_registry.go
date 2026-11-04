package openaiserver

import (
	"errors"
	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/dsml"
)

func requestRegistry(req chatRequest, replay *dsml.ReplayStore) (*ds4.ToolRegistry, error) {
	registry := ds4.NewToolRegistry()
	registry.SetReplayStore(replay)
	for _, tool := range req.Tools {
		if tool.Type != "" && tool.Type != "function" {
			return nil, errors.New("only function tools are supported")
		}
		if err := registry.RegisterFunc(ds4.ToolSchema{Name: tool.Function.Name, Description: tool.Function.Description, Parameters: tool.Function.Parameters}, nil); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
func buildRegistryPrompt(engine *ds4.Engine, images *ds4.ImageEncoder, req chatRequest, think ds4.ThinkMode, registry *ds4.ToolRegistry) (*ds4.Prompt, error) {
	system, history, err := convertMessages(req.Messages)
	if err != nil {
		return nil, clientError{err}
	}
	if images == nil && historyHasImages(history) {
		return nil, clientError{errors.New("request contains images but no vision encoder is loaded")}
	}
	return registry.BuildPromptMultimodal(engine, images, system, history, think)
}
func parseRegistryMessage(engine *ds4.Engine, registry *ds4.ToolRegistry, text string, think ds4.ThinkMode) (chatMessage, error) {
	strict, strictErr := dsml.ParseCompletionSyntax(ds4.ToolSyntax(engine), text, engine.ThinkModeEnabled(think))
	if strictErr != nil || len(strict.ToolCalls) == 0 {
		return parseAssistantMessage(engine, text, think)
	}
	parsed, err := registry.ParseAssistantSyntax(ds4.ToolSyntax(engine), text, engine.ThinkModeEnabled(think))
	if err != nil {
		return chatMessage{}, err
	}
	msg := chatMessage{Role: "assistant", Content: parsed.Content, ReasoningContent: parsed.ReasoningContent}
	for _, call := range parsed.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, chatToolCall{ID: call.ID, Type: "function", Function: chatToolFunction{Name: call.Name, Arguments: call.Arguments}})
	}
	return msg, nil
}
