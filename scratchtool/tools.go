package scratchtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ds4 "github.com/NimbleMarkets/ds4go"
)

// newTool wraps a typed handler following workspacetool's contract: routine
// failures become "ERROR: ..." observations so the tool loop can continue,
// and only context cancellation aborts the loop.
func newTool[A any](schema ds4.ToolSchema, run func(context.Context, A) (string, error)) ds4.ToolHandler {
	return ds4.Tool{ToolSchema: schema, Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		var a A
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &a); err != nil {
				return fmt.Sprintf("ERROR: %s: bad args: %v\n", schema.Name, err), nil
			}
		}
		out, err := run(ctx, a)
		if ctx != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			return "ERROR: " + err.Error() + "\n", nil
		}
		return out, nil
	}}
}

type listArgs struct{}

func (s *Store) listTool() ds4.ToolHandler {
	return newTool(schema("scratch_list", listDesc, listParams),
		func(ctx context.Context, a listArgs) (string, error) {
			_, _ = ctx, a
			entries, err := s.list()
			if err != nil {
				return "", err
			}
			if len(entries) == 0 {
				return "OK: no keys\n", nil
			}
			var out strings.Builder
			for _, ent := range entries {
				fmt.Fprintf(&out, "%s  %d  %s\n", ent.key, ent.size, ent.modified.UTC().Format(time.RFC3339))
			}
			return out.String(), nil
		})
}

type getArgs struct {
	Key  string `json:"key"`
	Head int    `json:"head"`
	Tail int    `json:"tail"`
}

func (s *Store) getTool() ds4.ToolHandler {
	return newTool(schema("scratch_get", getDesc, getParams),
		func(ctx context.Context, a getArgs) (string, error) {
			_ = ctx
			if a.Head > 0 && a.Tail > 0 {
				return "", fmt.Errorf("set only one of head or tail")
			}
			if a.Head < 0 || a.Tail < 0 {
				return "", fmt.Errorf("head and tail must be non-negative")
			}
			value, err := s.get(a.Key)
			if err != nil {
				return "", err
			}
			part := value
			switch {
			case a.Head > 0 && a.Head < len(value):
				part = value[:a.Head]
			case a.Tail > 0 && a.Tail < len(value):
				part = value[len(value)-a.Tail:]
			}
			header := fmt.Sprintf("key=%s bytes=%d truncated=%v\n", a.Key, len(value), len(part) < len(value))
			return header + string(part), nil
		})
}

// setArgs carries value as a pointer so a missing or null value is
// distinguishable from an explicit empty string: the registry does not
// enforce the schema's required fields, and treating an absent value as ""
// would let a malformed scratch_set silently erase an existing key.
type setArgs struct {
	Key   string  `json:"key"`
	Value *string `json:"value"`
}

func (s *Store) setTool() ds4.ToolHandler {
	return newTool(schema("scratch_set", setDesc, setParams),
		func(ctx context.Context, a setArgs) (string, error) {
			_ = ctx
			if a.Value == nil {
				return "", fmt.Errorf(`missing "value"; pass an empty string to store empty text`)
			}
			if err := s.set(a.Key, []byte(*a.Value)); err != nil {
				return "", err
			}
			return fmt.Sprintf("OK: set %q (%d bytes)\n", a.Key, len(*a.Value)), nil
		})
}

func (s *Store) appendTool() ds4.ToolHandler {
	return newTool(schema("scratch_append", appendDesc, appendParams),
		func(ctx context.Context, a setArgs) (string, error) {
			_ = ctx
			if a.Value == nil {
				return "", fmt.Errorf(`missing "value"; pass an empty string to append nothing`)
			}
			if err := s.appendValue(a.Key, []byte(*a.Value)); err != nil {
				return "", err
			}
			return fmt.Sprintf("OK: appended %d bytes to %q\n", len(*a.Value), a.Key), nil
		})
}

type deleteArgs struct {
	Key string `json:"key"`
}

func (s *Store) deleteTool() ds4.ToolHandler {
	return newTool(schema("scratch_delete", deleteDesc, deleteParams),
		func(ctx context.Context, a deleteArgs) (string, error) {
			_ = ctx
			if err := s.deleteKey(a.Key); err != nil {
				return "", err
			}
			return fmt.Sprintf("OK: deleted %q\n", a.Key), nil
		})
}
