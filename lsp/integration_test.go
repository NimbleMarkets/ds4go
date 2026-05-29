package lsp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go/lsp"
)

// requireServer skips the test unless the named binary is on PATH.
func requireServer(t *testing.T, bin string) {
	t.Helper()
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s not on PATH; skipping integration test", bin)
	}
}

func TestIntegration_GoplsDiagnostics(t *testing.T) {
	requireServer(t, "gopls")

	// Real workspace on disk: gopls needs go.mod + a file to give diagnostics.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/tmp\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.go")
	// Deliberately broken: undefined symbol.
	if err := os.WriteFile(src, []byte("package main\nfunc main() { undefinedFunc() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := lsp.New(ctx, lsp.ServerConfig{Command: "gopls", RootDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Shutdown(context.Background())

	uri := c.URI("main.go")
	body, _ := os.ReadFile(src)
	if err := c.Open(ctx, uri, "go", string(body)); err != nil {
		t.Fatalf("Open: %v", err)
	}

	diags, _ := c.WaitForDiagnostics(ctx, uri, 15*time.Second)
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic for undefined symbol")
	}
	t.Logf("gopls diagnostics: %+v", diags)
}

func TestIntegration_LuaLSDiagnostics(t *testing.T) {
	requireServer(t, "lua-language-server")

	dir := t.TempDir()
	src := filepath.Join(dir, "broken.lua")
	// Syntax error: unterminated assignment. LuaLS reports this reliably,
	// independent of workspace/semantic config.
	if err := os.WriteFile(src, []byte("local x =\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// LuaLS preloads meta on first run and can be slow to initialize.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := lsp.New(ctx, lsp.ServerConfig{Command: "lua-language-server", RootDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Shutdown(context.Background())

	uri := c.URI("broken.lua")
	body, _ := os.ReadFile(src)
	if err := c.Open(ctx, uri, "lua", string(body)); err != nil {
		t.Fatalf("Open: %v", err)
	}

	diags, _ := c.WaitForDiagnostics(ctx, uri, 30*time.Second)
	if len(diags) == 0 {
		t.Fatal("expected at least one diagnostic for the Lua syntax error")
	}
	t.Logf("luals diagnostics: %+v", diags)
}
