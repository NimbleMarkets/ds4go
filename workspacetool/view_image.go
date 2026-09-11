package workspacetool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/NimbleMarkets/ds4go"
)

type viewImageArgs struct {
	Path string `json:"path"`
}

// isImageFile reports whether data starts with a PNG or JPEG signature.
func isImageFile(data []byte) bool {
	return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) || bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff})
}

// ViewImageTool returns view_image, upstream ds4-agent's tool for opening a
// local PNG or JPEG as a visual observation. Paths are confined like read.
// Without a vision encoder it returns a text observation rather than an
// error, so the model can carry on, matching upstream.
func (w *Workspace) ViewImageTool() ds4.ToolHandler {
	textResult := func(format string, args ...any) ds4.ToolResult {
		return ds4.ToolResult{Parts: []ds4.ContentPart{{Text: fmt.Sprintf(format, args...)}}}
	}
	return ds4.MultimodalTool{
		ToolSchema: schema("view_image", "Open a local PNG or JPEG as a visual observation.", viewImageParams),
		Handler: func(ctx context.Context, raw json.RawMessage) (ds4.ToolResult, error) {
			if ctx != nil {
				if err := ctx.Err(); err != nil {
					return ds4.ToolResult{}, err
				}
			}
			var a viewImageArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return textResult("ERROR: view_image: bad args: %v\n", err), nil
				}
			}
			if a.Path == "" {
				return textResult("ERROR: view_image requires path\n"), nil
			}
			target, err := w.resolvePath(a.Path, accessRead)
			if err != nil {
				return textResult("ERROR: view_image: %v\n", err), nil
			}
			data, err := target.readFile()
			if err != nil {
				return textResult("ERROR: view_image: %v\n", err), nil
			}
			if !isImageFile(data) {
				return textResult("ERROR: view_image: %s is not a PNG or JPEG\n", a.Path), nil
			}
			if w.cfg.VisionAvailable == nil || !w.cfg.VisionAvailable() {
				return textResult("Tool error: view_image requires a vision encoder; start with --vision FILE\n"), nil
			}
			return ds4.ToolResult{Parts: []ds4.ContentPart{
				{Text: fmt.Sprintf("\n[tool:view_image] %s\n", filepath.Base(target.abs))},
				{Image: &ds4.ImageInput{Path: target.abs}},
			}}, nil
		},
	}
}
