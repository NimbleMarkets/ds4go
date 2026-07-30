package workspacetool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
)

type listArgs struct {
	Path string `json:"path"`
}

// ListTool returns a tool that lists one directory compactly.
func (w *Workspace) ListTool() ds4.ToolHandler {
	return newTool(schema("list", "List one directory compactly.", listParams),
		func(ctx context.Context, a listArgs) (string, error) {
			_ = ctx
			if a.Path == "" {
				a.Path = "."
			}
			target, err := w.resolvePath(a.Path, accessRead)
			if err != nil {
				return "", err
			}
			entries, err := target.readDir()
			if err != nil {
				return "", err
			}
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].IsDir() != entries[j].IsDir() {
					return entries[i].IsDir()
				}
				return entries[i].Name() < entries[j].Name()
			})
			var out strings.Builder
			fmt.Fprintf(&out, "%s:\n", target.abs)
			limit := w.cfg.MaxListEntries
			for i, ent := range entries {
				if i >= limit {
					fmt.Fprintf(&out, "... %d more entries omitted ...\n", len(entries)-limit)
					break
				}
				info, err := ent.Info()
				if err != nil {
					continue
				}
				name := ent.Name()
				typ := "-"
				if ent.IsDir() {
					typ = "d"
					name += string(filepath.Separator)
				} else if info.Mode()&os.ModeSymlink != 0 {
					typ = "l"
				}
				fmt.Fprintf(&out, "%s %10d %s\n", typ, info.Size(), name)
			}
			return out.String(), nil
		})
}
