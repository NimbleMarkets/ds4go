package workspacetool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ds4 "github.com/NimbleMarkets/ds4go"
)

func TestRecoverableFailuresAreObservations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello\n"), 0666); err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t, Config{Root: root, AllowWrite: true})

	out, err := invoke(t, w.EditTool(), `{"path":"f.txt","old":"nope","new":"x"}`)
	if err != nil {
		t.Fatalf("edit anchor miss must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "old text not found") {
		t.Fatalf("edit anchor miss observation = %q", out)
	}

	out, err = invoke(t, w.ReadTool(), `{"path":"missing.txt"}`)
	if err != nil {
		t.Fatalf("read of missing file must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") {
		t.Fatalf("read of missing file observation = %q", out)
	}

	out, err = invoke(t, w.MoreTool(), `{}`)
	if err != nil {
		t.Fatalf("more without prior read must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "no previous output") {
		t.Fatalf("more without prior read observation = %q", out)
	}

	out, err = invoke(t, w.ReadTool(), `{"path":1234}`)
	if err != nil {
		t.Fatalf("bad args must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "bad args") {
		t.Fatalf("bad args observation = %q", out)
	}
}

func TestContextCancellationIsFatal(t *testing.T) {
	w := newTestWorkspace(t, Config{Root: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.ReadTool().Invoke(ctx, json.RawMessage(`{"path":"x"}`)); err == nil {
		t.Fatal("expected fatal error for canceled context")
	}
}

func TestConfirmDenialIsObservation(t *testing.T) {
	w := newTestWorkspace(t, Config{
		Root:       t.TempDir(),
		AllowWrite: true,
		Confirm: func(ctx context.Context, a Action) (bool, error) {
			return false, nil
		},
	})
	out, err := invoke(t, w.WriteTool(), `{"path":"x.txt","content":"x"}`)
	if err != nil {
		t.Fatalf("confirm denial must be an observation, got error: %v", err)
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "write denied") {
		t.Fatalf("confirm denial observation = %q", out)
	}
}

func TestConfirmCallbackErrorIsFatal(t *testing.T) {
	sentinel := errors.New("app broke")
	w := newTestWorkspace(t, Config{
		Root:       t.TempDir(),
		AllowWrite: true,
		Confirm: func(ctx context.Context, a Action) (bool, error) {
			return false, sentinel
		},
	})
	_, err := invoke(t, w.WriteTool(), `{"path":"x.txt","content":"x"}`)
	if !errors.Is(err, sentinel) {
		t.Fatalf("confirm callback error must abort with the app error, got %v", err)
	}
}

func TestRegisterEditingRequiresAllowWrite(t *testing.T) {
	w := newTestWorkspace(t, Config{Root: t.TempDir()})
	if err := w.RegisterEditing(ds4.NewToolRegistry()); err == nil {
		t.Fatal("expected RegisterEditing to fail without AllowWrite")
	}
}

func TestRegisterShellRequiresAllowShell(t *testing.T) {
	w := newTestWorkspace(t, Config{Root: t.TempDir()})
	if err := w.RegisterShell(ds4.NewToolRegistry()); err == nil {
		t.Fatal("expected RegisterShell to fail without AllowShell")
	}
}

func TestRegisterAllRegistersOnlyPermittedTools(t *testing.T) {
	registered := func(w *Workspace) map[string]bool {
		t.Helper()
		reg := ds4.NewToolRegistry()
		if err := w.RegisterAll(reg); err != nil {
			t.Fatal(err)
		}
		names := make(map[string]bool)
		for _, s := range reg.Schemas() {
			names[s.Name] = true
		}
		return names
	}

	readOnly := registered(newTestWorkspace(t, Config{Root: t.TempDir()}))
	for _, name := range []string{"read", "more", "list", "search"} {
		if !readOnly[name] {
			t.Fatalf("read-only RegisterAll missing %q: %v", name, readOnly)
		}
	}
	for _, name := range []string{"write", "edit", "bash", "bash_status", "bash_stop"} {
		if readOnly[name] {
			t.Fatalf("read-only RegisterAll must not advertise %q: %v", name, readOnly)
		}
	}

	full := registered(newTestWorkspace(t, Config{Root: t.TempDir(), AllowWrite: true, AllowShell: true}))
	for _, name := range []string{"read", "more", "list", "search", "write", "edit", "bash", "bash_status", "bash_stop"} {
		if !full[name] {
			t.Fatalf("fully-enabled RegisterAll missing %q: %v", name, full)
		}
	}
}
