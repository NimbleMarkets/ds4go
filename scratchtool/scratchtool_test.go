package scratchtool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ds4 "github.com/NimbleMarkets/ds4go"
)

func registeredNames(t *testing.T, reg *ds4.ToolRegistry) []string {
	t.Helper()
	schemas := reg.Schemas()
	names := make([]string, len(schemas))
	for i, schema := range schemas {
		names[i] = schema.Name
	}
	return names
}

func invoke(t *testing.T, h ds4.ToolHandler, args string) string {
	t.Helper()
	out, err := h.Invoke(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s(%s): %v", h.Schema().Name, args, err)
	}
	return out
}

func TestRegister(t *testing.T) {
	s := newTestStore(t, Config{})
	reg := ds4.NewToolRegistry()
	if err := s.Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	want := []string{"scratch_list", "scratch_get", "scratch_set", "scratch_append", "scratch_delete"}
	got := registeredNames(t, reg)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("registered tools = %v, want %v", got, want)
	}
}

func TestRegisterReadOnly(t *testing.T) {
	s := newTestStore(t, Config{})
	reg := ds4.NewToolRegistry()
	if err := s.RegisterReadOnly(reg); err != nil {
		t.Fatalf("RegisterReadOnly: %v", err)
	}
	want := []string{"scratch_list", "scratch_get"}
	got := registeredNames(t, reg)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("registered tools = %v, want %v", got, want)
	}
}

func TestReadOnlyRefusesRegister(t *testing.T) {
	s := newTestStore(t, Config{ReadOnly: true})
	if err := s.Register(ds4.NewToolRegistry()); err == nil {
		t.Error("Register on ReadOnly store = nil error, want error")
	}
	if err := s.RegisterReadOnly(ds4.NewToolRegistry()); err != nil {
		t.Errorf("RegisterReadOnly on ReadOnly store: %v", err)
	}
}

func TestListTool(t *testing.T) {
	s := newTestStore(t, Config{})
	list := s.listTool()
	if out := invoke(t, list, `{}`); out != "OK: no keys\n" {
		t.Errorf("empty list = %q, want %q", out, "OK: no keys\n")
	}
	invoke(t, s.setTool(), `{"key":"plan","value":"12345"}`)
	invoke(t, s.setTool(), `{"key":"findings","value":"12"}`)
	out := invoke(t, list, `{}`)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("list = %q, want 2 lines", out)
	}
	for i, want := range []string{"findings  2  ", "plan  5  "} {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("list line %d = %q, want prefix %q", i, lines[i], want)
		}
		stamp := lines[i][len(want):]
		when, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			t.Errorf("list line %d time %q: %v", i, stamp, err)
		} else if when.Location() != time.UTC {
			t.Errorf("list line %d time %q is not UTC", i, stamp)
		}
	}
}

func TestGetTool(t *testing.T) {
	s := newTestStore(t, Config{})
	invoke(t, s.setTool(), `{"key":"plan","value":"hello"}`)
	get := s.getTool()

	if out := invoke(t, get, `{"key":"plan"}`); out != "key=plan bytes=5 truncated=false\nhello" {
		t.Errorf("get = %q", out)
	}
	if out := invoke(t, get, `{"key":"plan","head":2}`); out != "key=plan bytes=5 truncated=true\nhe" {
		t.Errorf("get head = %q", out)
	}
	if out := invoke(t, get, `{"key":"plan","tail":2}`); out != "key=plan bytes=5 truncated=true\nlo" {
		t.Errorf("get tail = %q", out)
	}
	// head/tail at or past the value size return the full value untruncated.
	if out := invoke(t, get, `{"key":"plan","head":99}`); out != "key=plan bytes=5 truncated=false\nhello" {
		t.Errorf("get head=99 = %q", out)
	}
	if out := invoke(t, get, `{"key":"plan","head":2,"tail":2}`); !strings.HasPrefix(out, "ERROR: set only one of head or tail") {
		t.Errorf("get head+tail = %q, want both-set error", out)
	}
	if out := invoke(t, get, `{"key":"plan","head":-1}`); !strings.HasPrefix(out, "ERROR: ") {
		t.Errorf("get head=-1 = %q, want error", out)
	}
	if out := invoke(t, get, `{"key":"nope"}`); !strings.HasPrefix(out, `ERROR: unknown key "nope"`) {
		t.Errorf("get missing = %q, want unknown key error", out)
	}
}

func TestSetAppendDeleteTools(t *testing.T) {
	s := newTestStore(t, Config{})
	invoke(t, s.setTool(), `{"key":"findings","value":"- foo"}`)
	invoke(t, s.appendTool(), `{"key":"findings","value":"\n- bar"}`)
	if out := invoke(t, s.getTool(), `{"key":"findings"}`); !strings.HasSuffix(out, "- foo\n- bar") {
		t.Errorf("get after append = %q", out)
	}
	if out := invoke(t, s.deleteTool(), `{"key":"findings"}`); !strings.HasPrefix(out, "OK: ") {
		t.Errorf("delete = %q, want OK", out)
	}
	if out := invoke(t, s.deleteTool(), `{"key":"findings"}`); !strings.HasPrefix(out, `ERROR: unknown key "findings"`) {
		t.Errorf("delete missing = %q, want unknown key error", out)
	}
}

func TestToolLimitErrorsNameTheLimit(t *testing.T) {
	s := newTestStore(t, Config{MaxKeys: 1, MaxValueBytes: 4, MaxTotalBytes: 8})
	if out := invoke(t, s.setTool(), `{"key":"plan","value":"12345"}`); !strings.Contains(out, "MaxValueBytes") {
		t.Errorf("oversized set = %q, want MaxValueBytes named", out)
	}
	invoke(t, s.setTool(), `{"key":"plan","value":"1234"}`)
	if out := invoke(t, s.setTool(), `{"key":"other","value":"1"}`); !strings.Contains(out, "MaxKeys") {
		t.Errorf("set past MaxKeys = %q, want MaxKeys named", out)
	}
	if out := invoke(t, s.appendTool(), `{"key":"plan","value":"5"}`); !strings.Contains(out, "MaxValueBytes") {
		t.Errorf("append past MaxValueBytes = %q, want MaxValueBytes named", out)
	}
}

func TestSetAppendRequireValue(t *testing.T) {
	s := newTestStore(t, Config{})
	invoke(t, s.setTool(), `{"key":"plan","value":"keep me"}`)
	for _, args := range []string{`{"key":"plan"}`, `{"key":"plan","value":null}`} {
		for _, h := range []ds4.ToolHandler{s.setTool(), s.appendTool()} {
			out := invoke(t, h, args)
			if !strings.HasPrefix(out, "ERROR: ") || !strings.Contains(out, "value") {
				t.Errorf("%s(%s) = %q, want ERROR naming value", h.Schema().Name, args, out)
			}
		}
	}
	if out := invoke(t, s.getTool(), `{"key":"plan"}`); !strings.HasSuffix(out, "keep me") {
		t.Errorf("missing value clobbered the key: %q", out)
	}
	// An explicit empty string still stores empty text.
	if out := invoke(t, s.setTool(), `{"key":"plan","value":""}`); !strings.HasPrefix(out, "OK: ") {
		t.Errorf("set empty string = %q, want OK", out)
	}
}

func TestReadsRejectSymlinksAndNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, Config{Dir: dir})
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "leak")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	for _, key := range []string{"leak", "subdir"} {
		out := invoke(t, s.getTool(), `{"key":"`+key+`"}`)
		if !strings.HasPrefix(out, "ERROR: ") || strings.Contains(out, "TOPSECRET") {
			t.Errorf("get %q = %q, want ERROR without target contents", key, out)
		}
	}
	// Append must not copy the symlink target into the pad either.
	if out := invoke(t, s.appendTool(), `{"key":"leak","value":"x"}`); !strings.HasPrefix(out, "ERROR: ") {
		t.Errorf("append to symlink = %q, want ERROR", out)
	}
	if raw, err := os.ReadFile(secret); err != nil || string(raw) != "TOPSECRET" {
		t.Errorf("symlink target modified: %q, %v", raw, err)
	}
}

func TestToolBadArgs(t *testing.T) {
	s := newTestStore(t, Config{})
	out, err := s.getTool().Invoke(context.Background(), json.RawMessage(`{"key":`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.HasPrefix(out, "ERROR: ") {
		t.Errorf("bad args = %q, want ERROR observation", out)
	}
}

func TestToolContextCancelAborts(t *testing.T) {
	s := newTestStore(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.listTool().Invoke(ctx, json.RawMessage(`{}`)); err == nil {
		t.Error("Invoke with canceled context = nil error, want context error")
	}
}

func TestSystemHint(t *testing.T) {
	if !strings.Contains(SystemHint, "scratch_list/get/set/append/delete") {
		t.Errorf("SystemHint does not name the scratch tools: %q", SystemHint)
	}
}
