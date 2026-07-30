# workspacetool

`workspacetool` provides local workspace tools for `ds4go.ToolLoop`. It mirrors the practical tool set used by the upstream native `ds4-agent`: file reads, directory listing, search, exact edits, whole-file writes, and opt-in shell jobs.

The package is separate from the root `ds4go` package so applications decide what local-machine access the model receives.

## Usage

Register read-only tools:

```go
package main

import (
	"log"

	ds4go "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/workspacetool"
)

func main() {
	workspace, err := workspacetool.New(workspacetool.Config{
		Root: "/path/to/project",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer workspace.Close()

	reg := ds4go.NewToolRegistry()
	if err := workspace.RegisterReadOnly(reg); err != nil {
		log.Fatal(err)
	}

	// Use reg with ds4go.ToolLoop.
}
```

Enable editing tools explicitly:

```go
workspace, err := workspacetool.New(workspacetool.Config{
	Root:       "/path/to/project",
	AllowWrite: true,
})
if err != nil {
	return err
}

reg := ds4go.NewToolRegistry()
if err := workspace.RegisterEditing(reg); err != nil {
	return err
}
```

Enable shell tools explicitly:

```go
workspace, err := workspacetool.New(workspacetool.Config{
	Root:       "/path/to/project",
	AllowShell: true,
	Confirm: func(ctx context.Context, action workspacetool.Action) (bool, error) {
		return action.Kind != workspacetool.ActionShell || strings.HasPrefix(action.Command, "go test "), nil
	},
})
```

## Tool Sets

`RegisterReadOnly` registers:

| Tool | Purpose |
| --- | --- |
| `read` | Read a text file or line range. |
| `more` | Continue the previous chunked read. |
| `list` | List one directory compactly. |
| `search` | Search text files with literal or regexp matching. |

`RegisterEditing` registers the read-only tools plus:

| Tool | Purpose |
| --- | --- |
| `write` | Create or overwrite a text file. |
| `edit` | Replace exactly one old text match, with optional `[upto]` anchoring. |

`RegisterShell` registers:

| Tool | Purpose |
| --- | --- |
| `bash` | Run a shell command in the workspace root. |
| `bash_status` | Poll a running shell job. |
| `bash_stop` | Terminate a running shell job. |

`RegisterAll` registers the read-only tools plus every tool group the configuration permits: `write` / `edit` when `AllowWrite` is set, and the shell tools when `AllowShell` is set. Disabled groups are not advertised to the model. `RegisterEditing` and `RegisterShell` return an error unless their capability is enabled, so a registered tool is always usable.

## Safety Model

Default policy is conservative:

- `Root` defaults to the current working directory.
- Relative paths resolve under `Root`.
- Absolute paths and `..` traversal are rejected if they escape `Root`.
- In-root file access goes through an `os.Root` handle, so operations cannot
  escape `Root` even if a path component is swapped for a symlink concurrently.
- Symlink components are rejected unless `FollowSymlinks` is true; with
  `FollowSymlinks`, symlinks are followed but still confined to `Root`.
- Writes are rejected unless `AllowWrite` is true.
- Shell commands are rejected unless `AllowShell` is true.
- Shell stop, timeout, and close operations stop child processes by default on Unix-like platforms.
- Recursive search skips `.git`.
- Binary files are skipped by search and rejected by read/edit.
- Large reads, searched files, shell observations, and directory listings are capped.

Routine tool failures — a missing file, a wrong edit anchor, a denied `Confirm` — are returned to the model as `ERROR: ...` tool results, so a `ToolLoop` run continues and the model can correct itself. Only context cancellation and errors returned by the `Confirm` callback abort the loop.

Use `AllowOutsideRoot` only for trusted callers. It disables root confinement for path arguments.

Use `Confirm` to add application-specific approval for side effects:

```go
Confirm: func(ctx context.Context, action workspacetool.Action) (bool, error) {
	switch action.Kind {
	case workspacetool.ActionWrite, workspacetool.ActionEdit:
		return strings.HasSuffix(action.Path, ".go"), nil
	case workspacetool.ActionShell:
		return false, nil
	default:
		return true, nil
	}
}
```

## Available Tools

### `read`

Read a text file or a range of lines.

Arguments:

```json
{
  "path": "file.go",
  "start_line": 1,
  "max_lines": 500,
  "whole": false,
  "raw": false
}
```

`read` defaults to a bounded chunk. Non-raw output includes line numbers and a `continue_offset` when more content is available. Raw output is the exact file bytes; when a raw read is truncated, a single `[Read truncated: ...]` notice line leads the output and the file bytes follow unmodified.

### `more`

Continue the previous chunked read.

Arguments:

```json
{
  "count": 500
}
```

### `list`

List one directory compactly.

Arguments:

```json
{
  "path": "."
}
```

### `search`

Search files and return compact, edit-friendly matches.

Arguments:

```json
{
  "query": "needle",
  "path": ".",
  "mode": "literal",
  "glob": "*.go",
  "context": 2,
  "max_results": 50,
  "case_sensitive": true
}
```

Set `"mode": "regex"` to use Go regular expressions. `glob` matches the file basename or the path relative to the searched directory, so `sub/*.go` works; an invalid glob is reported as an error.

A search that stops before exhausting its scope says so, so a partial result set is never mistaken for a complete one. Reaching `max_results` appends `[Search stopped at max_results=N; more matches may exist.]`, and reaching the directory recursion cap of 24 appends `[Search stopped at directory depth 24; deeper subdirectories were not searched.]`. Both notices can appear, including when there are no matches at all.

### `write`

Create or overwrite a text file. Requires `AllowWrite`.

Arguments:

```json
{
  "path": "new.txt",
  "content": "hello\n"
}
```

### `edit`

Replace exactly one old text match. Requires `AllowWrite`.

Arguments:

```json
{
  "path": "file.go",
  "old": "return oldValue",
  "new": "return newValue"
}
```

For large replacements, `old` may contain one `[upto]` marker between unique head and tail anchors:

```json
{
  "path": "file.go",
  "old": "func parse() error {\n[upto]\n\treturn nil\n}",
  "new": "func parse() error {\n\treturn parseImpl()\n}"
}
```

The head and tail anchors must each identify one span safely. Ambiguous edits fail.

### `bash`

Run a shell command in the workspace root. Requires `AllowShell`.

Arguments:

```json
{
  "command": "go test ./...",
  "timeout_sec": 60,
  "refresh_sec": 1
}
```

Output is captured to a temporary file. Tool results include job metadata, status, exit code when done, an `output_path`, and a bounded head or tail snippet. The `output_path` file can be read with the `read` tool, and finished jobs remain observable with `bash_status` until the workspace is closed.

The command runs in a non-login, non-interactive shell (`-c`, or `/C` on Windows). It inherits `Config.Env`, so it already has the caller's environment; a login shell would add only its profile's side effects, whose banners and echoes would be captured as if they were command output.

Stopping a job — via `bash_stop`, a timeout, or `Close` — sends `SIGTERM`, escalates to `SIGKILL` after one second, and then waits up to five seconds for the process to be reaped. Teardown is always bounded: a process that outlives that grace period (one wedged in uninterruptible I/O survives even `SIGKILL`) is reported rather than waited on forever. `bash_stop` renders such a job with its true `status=running`, and `Close` returns an error naming the job IDs.

### `bash_status`

Poll a shell job.

Arguments:

```json
{
  "job": 1,
  "pid": 12345,
  "refresh_sec": 1
}
```

### `bash_stop`

Terminate a shell job and return its final observation.

Arguments:

```json
{
  "job": 1,
  "pid": 12345,
  "kill_children": true
}
```

By default on Unix-like platforms, `bash_stop` also stops child processes started by the shell. Pass `"kill_children": false` to stop only the shell process, or `"kill_children": true` to force it even when `LeaveChildProcesses` makes single-process stopping the workspace default.

Killing children works by signaling the shell's process group, and this is only done while the shell is still running: an unreaped shell keeps its process-group id reserved, so the signal is guaranteed to target the shell's own group. This covers the common cases — stopping, timing out, or cancelling a running command terminates the whole tree. Once a shell has already exited on its own, its process-group id may have been reused by an unrelated process, so the group is not signaled; a backgrounded child that outlives its shell (for example `sleep 30 &` in a command whose shell then exits) is therefore left running rather than risk a mis-targeted kill. Reaping such a process reliably would require a supervisor process or platform process handle, which this package does not use. On Windows, process stopping is best-effort and targets the shell process only.

## Configuration

| Field | Description |
| --- | --- |
| `Root` | Workspace root. Defaults to the current working directory. |
| `AllowWrite` | Enables `write` and `edit`. |
| `AllowShell` | Enables `bash`, `bash_status`, and `bash_stop`. |
| `AllowOutsideRoot` | Allows paths outside `Root`. |
| `FollowSymlinks` | Allows symlink components in tool paths. |
| `LeaveChildProcesses` | Makes shell stop/timeout/close target only the shell process by default. |
| `MaxReadBytes` | Maximum bytes returned by whole-file reads. |
| `MaxReadLines` | Maximum lines per chunked read. |
| `MaxSearchResults` | Maximum search matches. |
| `MaxSearchFileBytes` | Maximum file size searched. |
| `MaxOutputBytes` | Maximum shell output bytes included in one observation. |
| `MaxListEntries` | Maximum directory entries listed. |
| `Confirm` | Optional callback before writes, edits, and shell commands. |
| `Env` | Environment for shell commands. Defaults to `os.Environ()`. |
| `Shell` | Shell executable. Defaults to `$SHELL`, `/bin/sh`, or `cmd.exe`. |
