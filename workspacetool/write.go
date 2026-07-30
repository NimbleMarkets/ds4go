package workspacetool

import (
	"context"
	"fmt"

	ds4 "github.com/NimbleMarkets/ds4go"
)

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteTool returns a tool that creates or overwrites a text file.
func (w *Workspace) WriteTool() ds4.ToolHandler {
	return newTool(schema("write", "Create or overwrite a text file.", writeParams),
		func(ctx context.Context, a writeArgs) (string, error) {
			target, err := w.resolvePath(a.Path, accessWrite)
			if err != nil {
				return "", err
			}
			if err := w.confirm(ctx, Action{Kind: ActionWrite, Path: target.abs}); err != nil {
				return "", err
			}
			if err := target.writeFile([]byte(a.Content), 0666); err != nil {
				return "", err
			}
			return fmt.Sprintf("Wrote %d bytes to %s\n", len(a.Content), target.abs), nil
		})
}
