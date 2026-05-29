package webtool

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NimbleMarkets/ds4go"
)

// Config configures the WebHelper.
type Config struct {
	// HomeDir is the directory containing user configuration.
	// Defaults to $HOME, or "." if unset.
	HomeDir string
	// Port is the debugging port to run Chrome on. Defaults to 9333.
	Port int
	// ChromePath is the path to the Chrome or Chromium executable.
	// If empty, auto-detection searches standard paths.
	ChromePath string
	// ConfirmApproval is called to prompt the user before starting Chrome for the first time.
	// If nil, starting Chrome will fail with an error.
	ConfirmApproval func(message string) (bool, error)
	// Log receives status messages from the helper.
	Log func(message string)
	// SearchProvider specifies the search engine to use.
	// Supported values: "google" (default), "duckduckgo", "bing".
	SearchProvider string
}

// WebHelper implements browser-backed web tools using Chrome DevTools Protocol.
type WebHelper struct {
	cfg            Config
	port           int
	profileDir     string
	chromeCmd      *exec.Cmd
	chromePID      int
	browserAllowed bool
	mu             sync.Mutex
}

// NewWebHelper creates a new WebHelper.
func NewWebHelper(cfg Config) *WebHelper {
	port := cfg.Port
	if port <= 0 {
		port = 9333
	}
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "."
	}
	profileDir := filepath.Join(home, ".ds4", "browser")
	return &WebHelper{
		cfg:        cfg,
		port:       port,
		profileDir: profileDir,
	}
}

// Close terminates the spawned Chrome browser if it was started by this helper.
func (w *WebHelper) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.chromeCmd != nil && w.chromeCmd.Process != nil {
		_ = w.chromeCmd.Process.Kill()
		w.chromeCmd = nil
		w.chromePID = 0
	}
	return nil
}

// Search executes a web search using the specified provider and returns Markdown links.
// Supported providers: "google", "duckduckgo", "bing". If provider is empty, the default
// configured SearchProvider is used (falling back to "google").
func (w *WebHelper) Search(ctx context.Context, query string, provider string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("search requires query")
	}
	if provider == "" {
		provider = w.cfg.SearchProvider
	}
	searchURL, js := w.getSearchConfig(provider, query)
	return w.runPageJS(ctx, searchURL, js, false)
}

// GoogleSearch searches Google (or the default configured SearchProvider) and returns search result Markdown links.
func (w *WebHelper) GoogleSearch(ctx context.Context, query string) (string, error) {
	return w.Search(ctx, query, "")
}

func (w *WebHelper) getSearchConfig(provider string, query string) (string, string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "google"
	}

	var searchURL string
	var badRegex string

	switch provider {
	case "duckduckgo":
		searchURL = "https://duckduckgo.com/?q=" + url.QueryEscape(query)
		badRegex = `(/(^|\.)duckduckgo\./.test(h))`
	case "bing":
		searchURL = "https://www.bing.com/search?q=" + url.QueryEscape(query)
		badRegex = `(/(^|\.)bing\./.test(h)||/(^|\.)bingj\./.test(h)||/(^|\.)microsoft\./.test(h)||/(^|\.)live\./.test(h))`
	case "yahoo":
		searchURL = "https://search.yahoo.com/search?p=" + url.QueryEscape(query)
		badRegex = `(/(^|\.)yahoo\./.test(h)||/(^|\.)yimg\./.test(h))`
	case "yandex":
		searchURL = "https://yandex.com/search/?text=" + url.QueryEscape(query)
		badRegex = `(/(^|\.)yandex\./.test(h)||/(^|\.)yastatic\./.test(h))`
	case "baidu":
		searchURL = "https://www.baidu.com/s?wd=" + url.QueryEscape(query)
		badRegex = `(/(^|\.)baidu\./.test(h)||/(^|\.)bdstatic\./.test(h))`
	case "brave":
		searchURL = "https://search.brave.com/search?q=" + url.QueryEscape(query)
		badRegex = `(/(^|\.)brave\./.test(h))`
	case "google":
		fallthrough
	default:
		searchURL = "https://www.google.com/search?q=" + url.QueryEscape(query)
		badRegex = `(/(^|\.)google\./.test(h)||/(^|\.)gstatic\./.test(h)||/(^|\.)googleusercontent\./.test(h))`
	}

	js := fmt.Sprintf(webExtractSearchTemplateJS, badRegex)

	return searchURL, js
}


// VisitPage navigates to a URL, optionally scrolls, and extracts its text layout.
func (w *WebHelper) VisitPage(ctx context.Context, targetURL string) (string, error) {
	if targetURL == "" {
		return "", fmt.Errorf("visit_page requires url")
	}
	return w.runPageJS(ctx, targetURL, webExtractPageJS, true)
}

const GoogleSearchParametersSchema = `{
	"type": "object",
	"properties": {
		"query": {
			"type": "string"
		}
	},
	"required": [
		"query"
	]
}`

const VisitPageParametersSchema = `{
	"type": "object",
	"properties": {
		"url": {
			"type": "string"
		}
	},
	"required": [
		"url"
	]
}`

// GoogleSearchTool returns the Tool schema and handler for Google Search.
func (w *WebHelper) GoogleSearchTool() ds4.Tool {
	return ds4.Tool{
		ToolSchema: ds4.ToolSchema{
			Name:        "google_search",
			Description: "Search Google in a visible browser and return compact Markdown links.",
			Parameters:  json.RawMessage(GoogleSearchParametersSchema),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var params struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", err
			}
			return w.GoogleSearch(ctx, params.Query)
		},
	}
}

// VisitPageTool returns the Tool schema and handler for visiting web pages.
func (w *WebHelper) VisitPageTool() ds4.Tool {
	return ds4.Tool{
		ToolSchema: ds4.ToolSchema{
			Name:        "visit_page",
			Description: "Open a URL in a visible browser and return rendered page Markdown.",
			Parameters:  json.RawMessage(VisitPageParametersSchema),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var params struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", err
			}
			return w.VisitPage(ctx, params.URL)
		},
	}
}

func (w *WebHelper) runPageJS(ctx context.Context, pageURL string, js string, dynamicScroll bool) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.ensureBrowser(ctx); err != nil {
		return "", err
	}

	browserWS, err := w.getBrowserWSURL(ctx)
	if err != nil {
		return "", err
	}

	bConn, err := dialWS(ctx, browserWS)
	if err != nil {
		return "", fmt.Errorf("failed to connect to browser websocket: %w", err)
	}

	targetID, err := w.createTarget(ctx, bConn)
	bConn.conn.Close()
	if err != nil {
		return "", err
	}

	defer w.closeTab(ctx, targetID)

	tabWS := fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/%s", w.port, targetID)
	var tConn *wsConn
	var dialErr error
	for attempts := 0; attempts < 10; attempts++ {
		tConn, dialErr = dialWS(ctx, tabWS)
		if dialErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(100*(attempts+1)) * time.Millisecond):
		}
	}
	if dialErr != nil {
		return "", fmt.Errorf("failed to connect to tab websocket: %w", dialErr)
	}
	defer tConn.conn.Close()

	if _, err := tConn.call(ctx, "Page.enable", nil); err != nil {
		return "", err
	}
	if _, err := tConn.call(ctx, "Runtime.enable", nil); err != nil {
		return "", err
	}

	_, _ = tConn.call(ctx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true})
	_, _ = tConn.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             1365,
		"height":            900,
		"deviceScaleFactor": 1,
		"mobile":            false,
	})

	if err := w.waitReady(ctx, tConn); err != nil {
		return "", err
	}

	if _, err := tConn.call(ctx, "Page.navigate", map[string]any{"url": pageURL}); err != nil {
		return "", err
	}

	if err := w.waitNavigatedReady(ctx, tConn, pageURL); err != nil {
		return "", err
	}

	// Bypassing search consent
	clickedRaw, err := tConn.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":            webClickGoogleConsentJS,
		"returnByValue":         true,
		"awaitPromise":          true,
		"includeCommandLineAPI": true,
	})
	if err == nil {
		var evalRes cdpEvalResult
		if json.Unmarshal(clickedRaw, &evalRes) == nil {
			clicked := evalRes.Result.Value
			if clicked != "" {
				if w.cfg.Log != nil {
					w.cfg.Log(clicked)
				}
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(1500 * time.Millisecond):
				}
				_ = w.waitNavigatedReady(ctx, tConn, pageURL)
			}
		}
	}

	if dynamicScroll {
		_, _ = tConn.call(ctx, "Runtime.evaluate", map[string]any{
			"expression":            webScrollDynamicPageJS,
			"returnByValue":         true,
			"awaitPromise":          true,
			"includeCommandLineAPI": true,
		})
	}

	extractRaw, err := tConn.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":            js,
		"returnByValue":         true,
		"awaitPromise":          true,
		"includeCommandLineAPI": true,
	})
	if err != nil {
		return "", err
	}

	var evalRes cdpEvalResult
	if err := json.Unmarshal(extractRaw, &evalRes); err != nil {
		return "", err
	}
	if evalRes.ExceptionDetails != nil {
		return "", fmt.Errorf("javascript extraction failed: %v", evalRes.ExceptionDetails)
	}
	return evalRes.Result.Value, nil
}

func (w *WebHelper) ensureBrowser(ctx context.Context) error {
	if isCDPAlive(w.port) {
		return nil
	}

	if !w.browserAllowed {
		if w.cfg.ConfirmApproval == nil {
			return fmt.Errorf("starting a visible Chrome browser requires interactive approval")
		}
		approved, err := w.cfg.ConfirmApproval("The web tool wants to start a visible Chrome browser. Allow? (y/n) ")
		if err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("user denied Chrome browser start")
		}
		w.browserAllowed = true
	}

	if err := os.MkdirAll(w.profileDir, 0700); err != nil {
		return fmt.Errorf("failed to create profile directory %q: %w", w.profileDir, err)
	}

	if err := w.spawnChrome(ctx); err != nil {
		return err
	}

	for i := 0; i < 80; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if isCDPAlive(w.port) {
			if w.cfg.Log != nil {
				w.cfg.Log("Chrome browser session is ready")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("Chrome did not expose CDP on port %d", w.port)
}

func (w *WebHelper) spawnChrome(ctx context.Context) error {
	chromePath := w.cfg.ChromePath
	if chromePath == "" {
		chromePath = findChromePath()
	}

	portArg := fmt.Sprintf("--remote-debugging-port=%d", w.port)
	profileArg := fmt.Sprintf("--user-data-dir=%s", w.profileDir)

	args := []string{
		portArg,
		"--remote-allow-origins=*",
		profileArg,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--mute-audio",
		"about:blank",
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" && strings.Contains(chromePath, "Google Chrome.app") {
		appName := "Google Chrome"
		if strings.Contains(chromePath, "Chromium.app") {
			appName = "Chromium"
		}
		openArgs := append([]string{"-g", "-na", appName, "--args"}, args...)
		openArgs = append(openArgs, "--use-mock-keychain", "--password-store=basic")
		cmd = exec.CommandContext(ctx, "/usr/bin/open", openArgs...)
	} else {
		var extraArgs []string
		if runtime.GOOS == "darwin" {
			extraArgs = []string{"--use-mock-keychain", "--password-store=basic"}
		} else if runtime.GOOS == "linux" {
			if os.Geteuid() == 0 {
				extraArgs = []string{"--no-sandbox", "--password-store=basic"}
			} else {
				extraArgs = []string{"--password-store=basic"}
			}
		}
		cmd = exec.CommandContext(ctx, chromePath, append(args, extraArgs...)...)
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	w.chromeCmd = cmd
	w.chromePID = cmd.Process.Pid
	return nil
}

func (w *WebHelper) getBrowserWSURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/json/version", w.port), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}
	ws, _ := data["webSocketDebuggerUrl"].(string)
	if ws == "" {
		return "", fmt.Errorf("no webSocketDebuggerUrl in /json/version response")
	}
	return ws, nil
}

func (w *WebHelper) createTarget(ctx context.Context, conn *wsConn) (string, error) {
	res, err := conn.call(ctx, "Target.createTarget", map[string]any{
		"url":        "about:blank",
		"background": true,
		"newWindow":  false,
	})
	if err != nil {
		return "", err
	}
	var targetRes cdpCreateTargetResult
	if err := json.Unmarshal(res, &targetRes); err != nil {
		return "", err
	}
	return targetRes.TargetID, nil
}

func (w *WebHelper) closeTab(ctx context.Context, targetID string) {
	if targetID == "" {
		return
	}
	u := fmt.Sprintf("http://127.0.0.1:%d/json/close/%s", w.port, url.PathEscape(targetID))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (w *WebHelper) waitReady(ctx context.Context, conn *wsConn) error {
	for i := 0; i < 80; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		raw, err := conn.call(ctx, "Runtime.evaluate", map[string]any{
			"expression":    "document.readyState",
			"returnByValue": true,
			"awaitPromise":  true,
		})
		if err == nil {
			var evalRes cdpEvalResult
			if json.Unmarshal(raw, &evalRes) == nil {
				val := evalRes.Result.Value
				if val == "complete" || val == "interactive" {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(800 * time.Millisecond):
					}
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

func (w *WebHelper) waitNavigatedReady(ctx context.Context, conn *wsConn, targetURL string) error {
	lastLen := int64(-1)
	stable := 0
	sawRealURL := false

	expr := "location.href+'\\n'+document.readyState+'\\n'+((document.body&&document.body.innerText)||'').length"

	for i := 0; i < 100; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		raw, err := conn.call(ctx, "Runtime.evaluate", map[string]any{
			"expression":    expr,
			"returnByValue": true,
			"awaitPromise":  true,
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}

		var evalRes cdpEvalResult
		if json.Unmarshal(raw, &evalRes) == nil {
			val := evalRes.Result.Value
			parts := strings.Split(val, "\n")
			if len(parts) >= 3 {
				href := parts[0]
				ready := parts[1]
				textLen, _ := strconv.ParseInt(parts[2], 10, 64)

				realURL := href != "" && href != "about:blank" && !strings.HasPrefix(href, "chrome://")
				readyState := ready == "complete" || ready == "interactive"

				if realURL {
					sawRealURL = true
				}

				if textLen > 0 && textLen == lastLen {
					stable++
				} else {
					stable = 0
				}
				lastLen = textLen

				if sawRealURL && readyState && textLen > 0 && stable >= 2 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(500 * time.Millisecond):
					}
					return nil
				}
				if sawRealURL && readyState && i >= 24 {
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

func isCDPAlive(port int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/json/version", port), nil)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	return strings.Contains(string(body), "webSocketDebuggerUrl")
}

func findChromePath() string {
	if env := os.Getenv("DS4_CHROME"); env != "" {
		return env
	}
	if runtime.GOOS == "darwin" {
		candidates := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	linuxPaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/snap/bin/chromium",
		"/opt/google/chrome/chrome",
	}
	for _, p := range linuxPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	names := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return "google-chrome"
}

// wsConn wraps a TCP/websocket connection to Chrome.
type wsConn struct {
	conn   net.Conn
	nextID int
}

type cdpRequest struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type cdpResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cdpEvalResult struct {
	Result struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"result"`
	ExceptionDetails any `json:"exceptionDetails,omitempty"`
}

type cdpCreateTargetResult struct {
	TargetID string `json:"targetId"`
}

func dialWS(ctx context.Context, u string) (*wsConn, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "ws" {
		return nil, fmt.Errorf("unsupported scheme: %q", parsed.Scheme)
	}

	host := parsed.Host
	if !strings.Contains(host, ":") {
		host = host + ":80"
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}

	rnd := make([]byte, 16)
	if _, err := rand.Read(rnd); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(rnd)

	req := fmt.Sprintf("GET %s HTTP/1.1\r\n"+
		"Host: %s\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\n"+
		"Sec-WebSocket-Version: 13\r\n\r\n",
		parsed.RequestURI(), parsed.Host, key)

	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, err
	}
	resp.Body.Close()

	if resp.StatusCode != 101 {
		conn.Close()
		return nil, fmt.Errorf("websocket handshake failed: status %d", resp.StatusCode)
	}

	bufConn := &bufferedConn{
		Conn: conn,
		r:    reader,
	}

	return &wsConn{conn: bufConn, nextID: 1}, nil
}

type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

func (ws *wsConn) writeMessage(text string) error {
	payload := []byte(text)
	var header []byte

	header = append(header, 0x81)

	length := len(payload)
	if length < 126 {
		header = append(header, 0x80|byte(length))
	} else if length <= 0xffff {
		header = append(header, 0x80|126)
		header = append(header, byte(length>>8), byte(length))
	} else {
		header = append(header, 0x80|127)
		for i := 7; i >= 0; i-- {
			header = append(header, byte(length>>(i*8)))
		}
	}

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)

	maskedPayload := make([]byte, length)
	for i := 0; i < length; i++ {
		maskedPayload[i] = payload[i] ^ mask[i%4]
	}

	if _, err := ws.conn.Write(header); err != nil {
		return err
	}
	if _, err := ws.conn.Write(maskedPayload); err != nil {
		return err
	}
	return nil
}

func (ws *wsConn) readMessage(ctx context.Context) (string, error) {
	var msg []byte
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		header := make([]byte, 2)
		if _, err := io.ReadFull(ws.conn, header); err != nil {
			return "", err
		}

		fin := (header[0] & 0x80) != 0
		opcode := header[0] & 0x0f
		masked := (header[1] & 0x80) != 0
		length := uint64(header[1] & 0x7f)

		if length == 126 {
			lenBytes := make([]byte, 2)
			if _, err := io.ReadFull(ws.conn, lenBytes); err != nil {
				return "", err
			}
			length = uint64(lenBytes[0])<<8 | uint64(lenBytes[1])
		} else if length == 127 {
			lenBytes := make([]byte, 8)
			if _, err := io.ReadFull(ws.conn, lenBytes); err != nil {
				return "", err
			}
			length = 0
			for i := 0; i < 8; i++ {
				length = (length << 8) | uint64(lenBytes[i])
			}
		}

		var mask []byte
		if masked {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(ws.conn, mask); err != nil {
				return "", err
			}
		}

		payload := make([]byte, length)
		if _, err := io.ReadFull(ws.conn, payload); err != nil {
			return "", err
		}

		if masked {
			for i := uint64(0); i < length; i++ {
				payload[i] ^= mask[i%4]
			}
		}

		switch opcode {
		case 0x8:
			return "", fmt.Errorf("websocket closed")
		case 0x9:
			if err := ws.writeControlFrame(0x0A, payload); err != nil {
				return "", err
			}
		case 0x1, 0x0:
			msg = append(msg, payload...)
			if fin {
				return string(msg), nil
			}
		}
	}
}

func (ws *wsConn) writeControlFrame(opcode byte, payload []byte) error {
	if len(payload) > 125 {
		payload = payload[:125]
	}
	var header []byte
	header = append(header, 0x80|opcode)
	header = append(header, 0x80|byte(len(payload)))
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := ws.conn.Write(header); err != nil {
		return err
	}
	_, err := ws.conn.Write(masked)
	return err
}

func (ws *wsConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := ws.nextID
	ws.nextID++

	req := cdpRequest{
		ID:     id,
		Method: method,
	}
	if params != nil {
		pBytes, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		req.Params = pBytes
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	if err := ws.writeMessage(string(reqBytes)); err != nil {
		return nil, err
	}

	for {
		msg, err := ws.readMessage(ctx)
		if err != nil {
			return nil, err
		}
		var resp cdpResponse
		if err := json.Unmarshal([]byte(msg), &resp); err != nil {
			continue
		}
		if resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("cdp error: %s (code %d)", resp.Error.Message, resp.Error.Code)
			}
			return resp.Result, nil
		}
	}
}
const webExtractSearchTemplateJS = `(() => {
	const clean = s => (s || '').replace(/\s+/g, ' ').trim();
	const esc = s => clean(s).replace(/\\/g, '\\\\').replace(/\[/g, '\\[').replace(/\]/g, '\\]').replace(/\n/g, ' ');
	const visible = el => {
		const r = el.getBoundingClientRect();
		const st = getComputedStyle(el);
		return r.width > 0 && r.height > 0 && st.display !== 'none' && st.visibility !== 'hidden' && st.opacity !== '0';
	};
	const bad = h => %s;
	const lines = ['# Search results', '', ` + "`URL: ${location.href}`" + `, '', '## Visible links'];
	const seen = new Set();
	for (const a of document.querySelectorAll('a[href]')) {
		if (!visible(a)) continue;
		let href = a.href || '';
		try {
			const u = new URL(href);
			if (u.pathname === '/url' && u.searchParams.get('q')) {
				href = u.searchParams.get('q');
			}
		} catch {}
		let u;
		try {
			u = new URL(href);
		} catch {
			continue;
		}
		if (!/^https?:$/.test(u.protocol)) continue;
		if (bad(u.hostname)) continue;
		const text = esc(a.innerText || a.textContent);
		if (text.length < 3) continue;
		if (seen.has(u.href)) continue;
		seen.add(u.href);
		lines.push(` + "`- [${text.slice(0,180)}](${u.href})`" + `);
		if (seen.size >= 20) break;
	}
	lines.push('', '## Text snapshot', clean(document.body.innerText).slice(0, 1200));
	return lines.join('\n');
})()`

const webClickGoogleConsentJS = `(() => {
const clean=s=>(s||'').replace(/\s+/g,' ').trim();
const pats=[/accept all/i,/i agree/i,/agree/i,/accetta tutto/i,/tout accepter/i,/aceptar todo/i,/alle akzeptieren/i];
const els=[...document.querySelectorAll('button,[role=button],input[type=submit],a')];
for (const el of els){const t=clean(el.innerText||el.value||el.textContent);
if(!t)continue; if(pats.some(p=>p.test(t))){el.click(); return 'clicked '+t;}}
return '';
})()`


const webExtractPageJS = `(() => {
const clean=s=>(s||'').replace(/\s+/g,' ').trim();
const esc=s=>clean(s).replace(/\\/g,'\\\\').replace(/\[/g,'\\[').replace(/\]/g,'\\]').replace(/\n/g,' ');
const visible=el=>{const r=el.getBoundingClientRect();const st=getComputedStyle(el);return r.width>0&&r.height>0&&st.display!=='none'&&st.visibility!=='hidden'&&st.opacity!=='0';};
const inline=n=>{if(!n)return'';if(n.nodeType===3)return n.nodeValue;if(n.nodeType!==1)return'';const el=n;
if(el.tagName==='SCRIPT'||el.tagName==='STYLE'||el.tagName==='NOSCRIPT')return'';
if(el.tagName==='A'){const t=esc(el.innerText||el.textContent);const h=el.href||'';return t&&h?` + "`[${t}](${h})`" + `:t;}
if(el.tagName==='CODE')return '` + "`" + `'+clean(el.innerText||el.textContent).replace(/` + "`" + `/g,'\\\\` + "`" + `')+'` + "`" + `';
return [...el.childNodes].map(inline).join('');};
const lines=[` + "`# ${clean(document.title)||location.href}`" + `,'',` + "`URL: ${location.href}`" + `,'','## Content'];
const blocks=[...document.body.querySelectorAll('h1,h2,h3,h4,h5,h6,p,li,pre,blockquote,td,th,[id="content-text"],[class*="comment-body"],[class*="comment-content"],[data-testid*="comment-text"]')];
const seen=new Set();
for(const el of blocks){if(!visible(el))continue;let s='';const tag=el.tagName;
if(/^H[1-6]$/.test(tag)){s='#'.repeat(Number(tag[1]))+' '+inline(el);}
else if(tag==='LI'){s='- '+inline(el);}
else if(tag==='PRE'){s='` + "```\\n" + `'+(el.innerText||el.textContent||'').trimEnd()+'\\n` + "```" + `';}
else if(tag==='BLOCKQUOTE'){s='> '+clean(el.innerText||el.textContent);}
else{s=inline(el);}s=s.trim();if(!s||seen.has(s))continue;seen.add(s);lines.push('',s);
if(lines.join('\n').length>900000){lines.push('','[Content truncated by browser extractor.]');break;}}
lines.push('','## Visible links');let n=0;const linkSeen=new Set();
for(const a of document.querySelectorAll('a[href]')){if(!visible(a))continue;const t=esc(a.innerText||a.textContent);if(t.length<3)continue;
let u;try{u=new URL(a.href);}catch{continue;}if(!/^https?:$/.test(u.protocol)||linkSeen.has(u.href))continue;linkSeen.add(u.href);
lines.push(` + "`- [${t.slice(0,160)}](${u.href})`" + `);if(++n>=80)break;}
return lines.join('\n');
})()`

const webScrollDynamicPageJS = `(() => new Promise(resolve => {
const root=()=>document.scrollingElement||document.documentElement||document.body;
const blockSel='h1,h2,h3,h4,h5,h6,p,li,pre,blockquote,td,th,[id="content-text"],[class*="comment-body"],[class*="comment-content"],[data-testid*="comment-text"]';
const lazySel='[onscroll],[loading="lazy"],[data-src],[data-lazy],[class*="lazy"],[class*="infinite"],[class*="virtual"],[role="feed"],[id*="comment"],[class*="comment"],[data-testid*="comment"]';
const hookCount=()=>{let n=0;try{if(window.onscroll)n++;if(document.onscroll)n++;if(document.body&&document.body.onscroll)n++;}catch(e){}
try{if(typeof getEventListeners==='function'){for(const o of [window,document,document.body]){if(!o)continue;const ev=getEventListeners(o);if(ev&&ev.scroll)n+=ev.scroll.length;}}}catch(e){}
try{n+=document.querySelectorAll(lazySel).length;}catch(e){}return n;};
const metrics=()=>{const r=root();return {
height:r?r.scrollHeight:0,
view:innerHeight||900,
y:scrollY||(r&&r.scrollTop)||0,
text:((document.body&&document.body.innerText)||'').length,
links:document.links?document.links.length:0,
blocks:document.body?document.body.querySelectorAll(blockSel).length:0,
hooks:hookCount()};};
const sig=m=>[m.height,m.text,m.links,m.blocks].join('|');
const grew=(a,b)=>b.height>a.height+20||b.text>a.text+200||b.links>a.links+2||b.blocks>a.blocks+2;
const scrollOnce=()=>{const r=root();if(!r)return;
const h=Math.max(700,Math.floor((innerHeight||900)*0.85));
window.scrollTo(0,Math.min(r.scrollHeight,(scrollY||r.scrollTop||0)+h));};
let last=metrics(),lastSig=sig(last),same=0,steps=0;
const scrollable=last.height>last.view*1.35;
if(!scrollable||last.hooks===0){resolve('scroll skipped hooks='+last.hooks+' text='+last.text);return;}
const tick=()=>{
if(steps>=28){resolve('scrolled '+steps+' text='+last.text);return;}
const before=last;
scrollOnce();steps++;
setTimeout(()=>{const now=metrics(),nowSig=sig(now);
if(nowSig===lastSig)same++;else same=0;
const loaded=grew(before,now);
last=now;lastSig=nowSig;
if(steps===1&&!loaded){resolve('scroll probe unchanged text='+now.text);return;}
const atBottom=now.y+now.view+20>=now.height;
if(same>=4||(atBottom&&same>=1)){resolve('scrolled '+steps+' text='+now.text);return;}
tick();},900);
};tick();
})()`
