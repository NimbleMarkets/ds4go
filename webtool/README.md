# webtool

`webtool` provides browser-backed web tools for the `ds4` agent loop, matching the design of the upstream `ds4` inference engine's browser helper (`ds4_web.c`).

It enables DeepSeek agent loops to search Google and visit web pages using a local Google Chrome or Chromium installation, and to fetch an image from the web as a visual observation for a vision model (`fetch_image`, which needs no browser).

## How it Works

1. **Auto-Detection**: The package automatically searches standard locations on macOS and Linux for a Google Chrome or Chromium executable (or reads the `DS4_CHROME` environment variable).
2. **Process Management**: It starts a visible Google Chrome process in the background configured with remote debugging enabled (`--remote-debugging-port`) and a dedicated user data directory (`~/.ds4/browser`).
3. **CDP over Raw WebSockets**: It connects to Chrome using the Chrome DevTools Protocol (CDP). To remain zero-dependency, it uses a lightweight, custom RFC 6455 WebSocket client implementation built on top of standard Go sockets.
4. **Markdown Extraction**: It navigates to pages, automatically bypasses Google Search consent forms, triggers lazy-loaded content by scrolling, and runs JavaScript-based Markdown extractors to compile clean structured text snapshots of pages.

## Usage

You can initialize `webtool.NewWebHelper` and register its tools (`GoogleSearchTool()`, `VisitPageTool()`, and `FetchImageTool()`) directly into a `ds4.ToolRegistry`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/webtool"
)

func main() {
	// Initialize the helper
	helper := webtool.NewWebHelper(webtool.Config{
		Port: 9333,
		ConfirmApproval: func(message string) (bool, error) {
			// Web tools require explicit user confirmation to launch Chrome for the first time
			fmt.Print(message)
			var input string
			_, err := fmt.Scanln(&input)
			if err != nil {
				return false, err
			}
			input = strings.ToLower(strings.TrimSpace(input))
			return input == "y" || input == "yes", nil
		},
		Log: func(msg string) {
			log.Printf("[Browser Log] %s\n", msg)
		},
	})
	// Keep the browser running across tool execution rounds, but clean up on shutdown
	defer helper.Close()

	// Register tools
	reg := ds4.NewToolRegistry()
	reg.MustRegister(helper.GoogleSearchTool())
	reg.MustRegister(helper.VisitPageTool())

	// Configure and run the ds4 ToolLoop...
}
```

## Available Tools

The following tools are exposed to the DeepSeek model:

### `google_search`
* **Description**: `Search Google in a visible browser and return compact Markdown links.`
* **Arguments**:
  ```json
  {
    "query": "search query string"
  }
  ```

### `visit_page`
* **Description**: `Open a URL in a visible browser and return rendered page Markdown.` Only http(s) URLs are opened; loopback, private, and link-local destinations are refused unless `AllowPrivateFetch`, including the page a redirect lands on.
* **Arguments**:
  ```json
  {
    "url": "https://example.com"
  }
  ```

## Configuration Options

The `webtool.Config` struct accepts the following configuration fields:

| Field | Type | Description |
| :--- | :--- | :--- |
| `HomeDir` | `string` | Directory containing user configuration. Defaults to `$HOME`, or `.` if unset. The Chrome user profile is stored at `HomeDir + "/.ds4/browser"`. |
| `Port` | `int` | Remote debugging port to use for the Chrome session. Defaults to `9333`. |
| `ChromePath` | `string` | Explicit path to the Chrome or Chromium executable. If empty, the helper will search standard paths. |
| `ConfirmApproval` | `func(string) (bool, error)` | Required. Callback invoked to request permission from the user before starting Chrome for the first time. |
| `Log` | `func(string)` | Optional logger callback for receiving status messages from the browser manager. |
| `SearchProvider` | `string` | Search engine to use: `"google"`, `"duckduckgo"`, `"bing"`, `"yahoo"`, `"yandex"`, `"baidu"`, or `"brave"`. Defaults to `"google"`. |
| `VisionAvailable` | `func() bool` | Whether the engine can encode images (`engine.HasVision`). When nil or false, `fetch_image` returns a text observation asking for `--vision`. |
| `AllowPrivateFetch` | `bool` | Let `fetch_image` and `visit_page` reach loopback, private, and link-local addresses, directly or via redirect. Off by default. Non-http(s) schemes such as `file://` are refused either way. |
| `MaxImageBytes` | `int` | Cap on one fetched image. Defaults to 64 MiB, upstream's image limit. |
| `HTTPClient` | `*http.Client` | Client used by `fetch_image`. Defaults to one with a 60 s timeout. |

## fetch_image

`fetch_image` downloads a PNG or JPEG over http(s) and returns it as an image
observation, the web counterpart of `workspacetool`'s `view_image`. The tool
loop must have an `Images` encoder set; the bytes travel in the observation so
history re-renders without refetching. The body is accepted by file signature,
not `Content-Type`, and capped at `MaxImageBytes`. Destinations that resolve
to private, loopback, or link-local addresses are refused on every hop unless
`AllowPrivateFetch` is set, so a model cannot be steered at services behind the
tool's host. The check runs on the address actually dialed, so a name whose
DNS answer changes between lookups (rebinding) gains nothing; a custom
`HTTPClient` must therefore use an `*http.Transport`, or set
`AllowPrivateFetch`. Without a vision encoder the tool answers with a text hint rather
than failing, matching upstream `view_image`.

