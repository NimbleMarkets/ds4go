# lsp

`lsp` is a small [Language Server Protocol](https://microsoft.github.io/language-server-protocol/) client for the `ds4` agent loop. It drives a single persistent language-server subprocess with in-memory documents, so a DeepSeek generation/self-correction loop can compile-check its own output: open a document, apply edits, and read back diagnostics, hover, symbols, and completions.

It is built on [`charmbracelet/x/powernap`](https://github.com/charmbracelet/x/tree/main/powernap) and pairs with the [`lsptool` subpackage](#tool-loop), which adapts a `*lsp.Client` into `ds4go.ToolHandler` values.

## How it Works

1. **One persistent server**: `lsp.New` launches the configured language server (which must be on `PATH`), runs the LSP `initialize` handshake, and declares a workspace folder from `RootDir`. The server stays up for the life of the `Client`.
2. **In-memory documents**: `Open` / `Update` / `Close` sync document text to the server via `didOpen` / `didChange` / `didClose` notifications. Documents need not exist on disk — virtual paths under `RootDir` work for pure in-memory analysis. The client tracks a per-document version internally.
3. **Push-based diagnostics**: the server publishes diagnostics asynchronously. The client buffers the latest set per document. `WaitForDiagnostics` correlates on the document version when the server reports one (honoring the full timeout); for servers that don't report a version, it falls back to a time-settle heuristic (`FirstWait` then `SettleWait`). Stale diagnostics from a previous edit or reopen are dropped so they are never returned as current.
4. **URI normalization**: documents are keyed by their normalized (percent-decoded) filesystem path, so a server that echoes back a differently-encoded URI than the client sent still resolves to the same document.

## Usage

Start a server, open an in-memory document, edit it, and read diagnostics:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/NimbleMarkets/ds4go/lsp"
)

func main() {
	ctx := context.Background()

	c, err := lsp.New(ctx, lsp.ServerConfig{Command: "gopls", RootDir: "/abs/work"})
	if err != nil {
		panic(err)
	}
	defer c.Shutdown(ctx)

	uri := c.URI("main.go")
	_ = c.Open(ctx, uri, "go", "package main\nfunc main() {}\n")

	// Apply generated code, then check it.
	_, _ = c.Update(ctx, uri, "package main\nfunc main() { undefinedFunc() }\n")
	diags, _ := c.WaitForDiagnostics(ctx, uri, 5*time.Second) // empty == clean
	for _, d := range diags {
		fmt.Printf("%d:%d [%s] %s\n", d.Line, d.Col, d.Severity, d.Message)
	}
}
```

`Hover`, `Symbols`, and `Completion` provide additional context for a model:

```go
md, _ := c.Hover(ctx, uri, 2, 14)                 // hover markdown at 1-based line:col
syms, _ := c.Symbols(ctx, uri)                    // flattened document outline
labels, more, _ := c.Completion(ctx, uri, 2, 14, 25) // up to 25 labels + count truncated
```

### Common servers

gopls (Go):

```go
lsp.ServerConfig{Command: "gopls", RootDir: moduleDir}
```

lua-language-server (LuaLS):

```go
lsp.ServerConfig{
	Command: "lua-language-server",
	RootDir: projectDir,
	Settings: map[string]any{"Lua": map[string]any{
		"diagnostics": map[string]any{"globals": []string{"vim"}},
	}},
}
```

## Tool loop

The [`lsptool`](./lsptool) subpackage wraps a `*lsp.Client` as `ds4go.ToolHandler` values so a model running in a `ds4go.ToolLoop` can query the language server and self-correct. Register the tools you want on a `ds4go.ToolRegistry`:

```go
import (
	ds4go "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/lsp/lsptool"
)

reg := ds4go.NewToolRegistry()
reg.MustRegister(lsptool.NewDiagnosticsTool(client))
reg.MustRegister(lsptool.NewHoverTool(client))
reg.MustRegister(lsptool.NewSymbolsTool(client))
reg.MustRegister(lsptool.NewCompletionTool(client))
```

Each tool decodes its JSON arguments and enforces that `uri` is present. All positions are **1-based** `line` / `character`.

### Available Tools

#### `lsp_diagnostics`
* **Description**: `Report language-server diagnostics (errors/warnings) for an open document. Optionally pass updated code to re-check first.`
* **Arguments**:
  ```json
  {
    "uri": "document URI",
    "code": "optional new full document text to apply before checking"
  }
  ```
  With `code`, the document is updated and re-checked before reporting; without it, the latest buffered diagnostics are returned (which may lag the most recent edit).

#### `lsp_hover`
* **Description**: `Hover info (type/doc) at a 1-based line and character in a document.`
* **Arguments**:
  ```json
  {
    "uri": "document URI",
    "line": 1,
    "character": 1
  }
  ```

#### `lsp_symbols`
* **Description**: `List the document outline (functions/types) for a document.`
* **Arguments**:
  ```json
  {
    "uri": "document URI"
  }
  ```

#### `lsp_completion`
* **Description**: `Completion suggestions at a 1-based line and character in a document.`
* **Arguments**:
  ```json
  {
    "uri": "document URI",
    "line": 1,
    "character": 1
  }
  ```

## Configuration Options

The `lsp.ServerConfig` struct accepts the following fields:

| Field | Type | Description |
| :--- | :--- | :--- |
| `Command` | `string` | Required. Language-server executable, looked up on `PATH`. |
| `Args` | `[]string` | Process arguments. |
| `RootDir` | `string` | Workspace root (absolute path); `""` is allowed but some servers only emit diagnostics when a workspace is declared and points at real files. |
| `InitOptions` | `map[string]any` | LSP `initializationOptions`. |
| `Settings` | `map[string]any` | `workspace/configuration` settings. |
| `Environment` | `map[string]string` | Extra environment variables for the server process. |
| `Timeout` | `time.Duration` | Per-request RPC timeout. Zero means no timeout. |
| `ShutdownTimeout` | `time.Duration` | Bounds the graceful shutdown handshake before the server is force-killed. Zero falls back to `DefaultShutdownTimeout` (5s). |
| `FirstWait` | `time.Duration` | Bounds how long `WaitForDiagnostics` waits for the first publish before assuming a silent server is clean. Zero falls back to `DefaultFirstWait` (5s). Raise it for servers with heavy cold-start latency. |
| `SettleWait` | `time.Duration` | Time-settle window for servers that don't report document versions. Zero falls back to `DefaultSettleWait` (300ms). |

## Caveats

* **Diagnostics are best-effort, not a guarantee.** `WaitForDiagnostics` correlates on the document version when the server reports one; otherwise it uses the time-settle heuristic. A pathologically slow server may publish after the wait returns. Tune `FirstWait` above your server's cold first-publish latency to avoid a premature "clean" result.
* **Workspace required for semantic diagnostics.** Some servers (e.g. LuaLS resolving `require()`) only produce full semantic diagnostics when `RootDir` points at the real project on disk. Pure in-memory documents reliably yield syntactic diagnostics.
* **No auto-restart.** v1 does not restart a crashed server. Query methods return `ErrServerDown`; recreate the `Client` to recover.
