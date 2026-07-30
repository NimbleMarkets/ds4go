package workspacetool

import (
	"context"
	"fmt"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
)

type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// EditTool returns a tool that replaces exactly one old text match. The old
// text may contain one [upto] marker between unique head and tail anchors.
func (w *Workspace) EditTool() ds4.ToolHandler {
	return newTool(schema("edit", "Replace exactly one old text match; old may contain [upto] between unique head and tail anchors.", editParams),
		func(ctx context.Context, a editArgs) (string, error) {
			target, err := w.resolvePath(a.Path, accessWrite)
			if err != nil {
				return "", err
			}
			if a.Old == "" {
				return "", fmt.Errorf("edit requires non-empty old text")
			}
			if err := w.confirm(ctx, Action{Kind: ActionEdit, Path: target.abs}); err != nil {
				return "", err
			}
			data, err := target.readFile()
			if err != nil {
				return "", err
			}
			if isBinary(data) {
				return "", fmt.Errorf("refusing to edit binary file: %s", target.abs)
			}
			start, end, anchored, err := findEditSpan(string(data), a.Old)
			if err != nil {
				return "", err
			}
			var out strings.Builder
			text := string(data)
			out.WriteString(text[:start])
			out.WriteString(a.New)
			out.WriteString(text[end:])
			if err := target.writeFile([]byte(out.String()), 0666); err != nil {
				return "", err
			}
			kind := "old/new replacement"
			if anchored {
				kind = "anchored old/new replacement"
			}
			return fmt.Sprintf("Applied %s to %s\n", kind, target.abs), nil
		})
}

func findEditSpan(data, old string) (start, end int, anchored bool, err error) {
	const marker = "[upto]"
	if !strings.Contains(old, marker) {
		first := strings.Index(data, old)
		if first < 0 {
			return 0, 0, false, fmt.Errorf("old text not found")
		}
		if strings.Contains(data[first+len(old):], old) {
			return 0, 0, false, fmt.Errorf("old text is not unique")
		}
		return first, first + len(old), false, nil
	}
	if strings.Count(old, marker) != 1 {
		return 0, 0, false, fmt.Errorf("old text may contain only one [upto] marker")
	}
	parts := strings.SplitN(old, marker, 2)
	head, tail := parts[0], parts[1]
	tail = strings.TrimLeft(tail, "\r\n")
	if strings.TrimSpace(head) == "" {
		return 0, 0, false, fmt.Errorf("old text before [upto] must include a unique head anchor")
	}
	if strings.TrimSpace(tail) == "" {
		return 0, 0, false, fmt.Errorf("old text after [upto] must include a unique tail anchor")
	}
	headPos, err := findUnique(data, head, "old head")
	if err != nil {
		return 0, 0, false, err
	}
	afterHead := headPos + len(head)
	tailRel, err := findUnique(data[afterHead:], tail, "old tail")
	if err != nil {
		return 0, 0, false, err
	}
	tailPos := afterHead + tailRel
	return headPos, tailPos + len(tail), true, nil
}

func findUnique(data, needle, label string) (int, error) {
	first := strings.Index(data, needle)
	if first < 0 {
		return 0, fmt.Errorf("%s anchor not found", label)
	}
	if strings.Contains(data[first+len(needle):], needle) {
		return 0, fmt.Errorf("%s anchor is not unique", label)
	}
	return first, nil
}
