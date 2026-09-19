# scratchtool

`scratchtool` provides a session-scoped key/value scratchpad for `ds4go.ToolLoop`: small, durable working memory for the model that is neither the chat log nor the user's project tree.

A pad is a flat map of key → text blob, persisted one file per key under `$DS4_DIR/scratch/<session>/`. The model lists keys, reads a value (optionally only its first or last bytes), replaces a value, appends to a value, and deletes a key. There are no directories, nested keys, or search; a queryable store would go behind this same API.

## Usage

Register the full tool set:

```go
package main

import (
	"log"

	ds4go "github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/scratchtool"
)

func main() {
	store, err := scratchtool.New(scratchtool.Config{Session: "mytask"})
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	reg := ds4go.NewToolRegistry()
	if err := store.Register(reg); err != nil {
		log.Fatal(err)
	}

	// Use reg with ds4go.ToolLoop. Optionally prepend
	// scratchtool.SystemHint to the system prompt.
}
```

Register read-only access to an existing pad:

```go
store, err := scratchtool.New(scratchtool.Config{
	Session:  "mytask",
	ReadOnly: true,
})
if err != nil {
	return err
}
defer store.Close()

reg := ds4go.NewToolRegistry()
if err := store.RegisterReadOnly(reg); err != nil {
	return err
}
```

## Tool Sets

`Register` registers:

| Tool | Purpose |
| --- | --- |
| `scratch_list` | List keys with sizes and modification times. |
| `scratch_get` | Read one value, optionally only its head or tail bytes. |
| `scratch_set` | Replace one value, creating the key if missing. |
| `scratch_append` | Append to one value, creating the key if missing. |
| `scratch_delete` | Delete one key. |

`RegisterReadOnly` registers only `scratch_list` and `scratch_get`. When `Config.ReadOnly` is set, `Register` returns an error instead of advertising write tools, so a registered tool is always usable.

## Keys and Limits

Keys are flat names validated on every call:

- 1–64 characters from `[a-z0-9._-]`
- no leading `.` or `-`, no `..`
- `index.json` is reserved

Suggested starter keys (not enforced): `plan`, `findings`, `open`, `user`, `session`.

Limits are enforced before every write, and exceeding one returns an `ERROR: ...` observation naming the limit:

| Limit | Default |
| --- | --- |
| `MaxKeys` | 32 |
| `MaxValueBytes` | 16 KiB |
| `MaxTotalBytes` | 128 KiB |

## Persistence

Each key is one file in the session directory, written atomically: a temp file in the same directory renamed over the destination, so a mid-write failure never leaves a truncated value. Per-key file I/O goes through an `os.Root` handle confined to the session directory, and reads reject symlinks and other non-regular files, so a planted symlink can neither leak an outside file into the model's context nor be copied into the pad. A `Store` is safe for concurrent use within one process. There is no cross-process locking: two CLIs sharing a session name can race each other's writes.

Routine tool failures — an unknown key, a bad key name, an exceeded limit — are returned to the model as `ERROR: ...` tool results, so a `ToolLoop` run continues and the model can correct itself. Only context cancellation aborts the loop.

## Available Tools

### `scratch_list`

List keys, one per line: key, size in bytes, last-modified time (UTC RFC 3339). An empty pad returns `OK: no keys`.

Arguments:

```json
{}
```

Output:

```
findings  2081  2026-09-19T15:04:02Z
plan  412  2026-09-19T15:02:11Z
```

### `scratch_get`

Read one value. `head` returns only the first N bytes, `tail` only the last N bytes (bytes, not runes); set at most one. A header line precedes the value:

```
key=plan bytes=412 truncated=false
```

Arguments:

```json
{
  "key": "plan",
  "head": 0,
  "tail": 0
}
```

### `scratch_set`

Replace one value, creating the key if missing.

Arguments:

```json
{
  "key": "plan",
  "value": "..."
}
```

### `scratch_append`

Append to one value, creating the key if missing. Limits apply to the combined size.

Arguments:

```json
{
  "key": "findings",
  "value": "\n- foo"
}
```

### `scratch_delete`

Delete one key. Deleting a missing key is an error, not a silent success.

Arguments:

```json
{
  "key": "plan"
}
```

## Configuration

| Field | Description |
| --- | --- |
| `Dir` | Session directory. Defaults to `DefaultDir(Session)`: `$DS4_DIR/scratch/<session>/`. |
| `Session` | Pad namespace. Defaults to `"default"`; must pass the same rules as pad keys. |
| `MaxKeys` | Maximum number of keys. Defaults to 32. |
| `MaxValueBytes` | Maximum bytes per value. Defaults to 16 KiB. |
| `MaxTotalBytes` | Maximum bytes across all values. Defaults to 128 KiB. |
| `ReadOnly` | Makes `Register` fail; use `RegisterReadOnly`. |
