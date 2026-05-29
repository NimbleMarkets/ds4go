// Package lsp is a small client for Language Server Protocol servers, built for
// driving generation/self-correction loops: start one persistent server, sync
// in-memory documents, and query diagnostics, hover, symbols, and completion.
//
// See the sections below for usage examples, common server configurations
// (gopls, LuaLS), integrating the client as ds4go.ToolHandler values via the
// lsptool subpackage, and important caveats about the push-based diagnostics
// model.
//
// # Usage
//
// Start a server, open an in-memory document, edit it, and read diagnostics:
//
//	ctx := context.Background()
//	c, err := lsp.New(ctx, lsp.ServerConfig{Command: "gopls", RootDir: "/abs/work"})
//	if err != nil { return err }
//	defer c.Shutdown(ctx)
//
//	uri := c.URI("main.go")
//	_ = c.Open(ctx, uri, "go", initialSrc)
//	_, _ = c.Update(ctx, uri, generatedSrc)
//	diags, _ := c.WaitForDiagnostics(ctx, uri, 5*time.Second) // empty == clean
//
// # Common servers
//
// gopls (Go):
//
//	lsp.ServerConfig{Command: "gopls", RootDir: moduleDir}
//
// lua-language-server (LuaLS):
//
//	lsp.ServerConfig{
//	    Command: "lua-language-server",
//	    RootDir: projectDir,
//	    Settings: map[string]any{"Lua": map[string]any{
//	        "diagnostics": map[string]any{"globals": []string{"vim"}},
//	    }},
//	}
//
// # Tool loop
//
// Wrap the client as ds4go tools via the lsptool subpackage and register them
// on a ds4go.ToolRegistry so a model can self-correct.
//
// # Caveats
//
//   - Diagnostics are push-based. WaitForDiagnostics correlates on the document
//     version when the server reports one; otherwise it falls back to a
//     time-settle heuristic (FirstWait then SettleWait). A pathologically slow
//     server may publish after the wait returns. This is best-effort, not a
//     guarantee.
//   - Some servers only emit diagnostics when a workspace is declared and
//     RootDir points at real files on disk. The client always declares a
//     workspace folder from RootDir; for full semantic diagnostics (e.g. LuaLS
//     resolving require()) point RootDir at the real project. Pure in-memory
//     documents reliably yield syntactic diagnostics.
//   - v1 does not auto-restart a crashed server. Query methods return
//     ErrServerDown; recreate the Client to recover.
package lsp
