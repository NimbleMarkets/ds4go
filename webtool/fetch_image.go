package webtool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/NimbleMarkets/ds4go"
)

// FetchImageParametersSchema is the JSON schema for the fetch_image tool.
const FetchImageParametersSchema = `{
	"type": "object",
	"properties": {
		"url": {
			"type": "string",
			"description": "http(s) URL of a PNG or JPEG image"
		}
	},
	"required": ["url"]
}`

// defaultMaxImageBytes matches upstream ds4's DS4_IMAGE_MAX_ENCODED_BYTES.
const defaultMaxImageBytes = 64 << 20

// FetchImageTool returns fetch_image: download a PNG or JPEG over http(s)
// and hand it to the model as a visual observation, the web counterpart of
// workspacetool's view_image. The bytes travel in the observation, so the
// tool loop encodes them once and re-renders history without refetching.
// The body is accepted by signature, not Content-Type, and capped at
// Config.MaxImageBytes. Private, loopback, and link-local destinations are
// refused on every hop unless Config.AllowPrivateFetch is set. Without a
// vision encoder it returns a text observation rather than an error, so the
// model can carry on, matching view_image.
func (w *WebHelper) FetchImageTool() ds4.ToolHandler {
	textResult := func(format string, args ...any) ds4.ToolResult {
		return ds4.ToolResult{Parts: []ds4.ContentPart{{Text: fmt.Sprintf(format, args...)}}}
	}
	return ds4.MultimodalTool{
		ToolSchema: ds4.ToolSchema{
			Name:        "fetch_image",
			Description: "Download a PNG or JPEG from an http(s) URL as a visual observation.",
			Parameters:  json.RawMessage(FetchImageParametersSchema),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (ds4.ToolResult, error) {
			if ctx == nil {
				ctx = context.Background()
			}
			if err := ctx.Err(); err != nil {
				return ds4.ToolResult{}, err
			}
			var a struct {
				URL string `json:"url"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return textResult("ERROR: fetch_image: bad args: %v\n", err), nil
				}
			}
			if a.URL == "" {
				return textResult("ERROR: fetch_image requires url\n"), nil
			}
			if err := w.checkFetchURL(a.URL); err != nil {
				return textResult("ERROR: fetch_image: %v\n", err), nil
			}
			data, err := w.fetchImageBytes(ctx, a.URL)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return ds4.ToolResult{}, err
				}
				return textResult("ERROR: fetch_image: %v\n", err), nil
			}
			if !isImageBytes(data) {
				return textResult("ERROR: fetch_image: %s is not a PNG or JPEG\n", a.URL), nil
			}
			if w.cfg.VisionAvailable == nil || !w.cfg.VisionAvailable() {
				return textResult("Tool error: fetch_image requires a vision encoder; start with --vision FILE\n"), nil
			}
			return ds4.ToolResult{Parts: []ds4.ContentPart{
				{Text: fmt.Sprintf("\n[tool:fetch_image] %s\n", a.URL)},
				{Image: &ds4.ImageInput{Data: data}},
			}}, nil
		},
	}
}

func isImageBytes(data []byte) bool {
	return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) || bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff})
}

// checkFetchURL applies the scheme and destination policy to one URL. The
// address check here is a fast refusal with a clear message; the check that
// counts is repeated on the address actually dialed (see vettedAddrs and
// pinnedDial), so a name that changes its answer between the two lookups
// (DNS rebinding) gains nothing.
func (w *WebHelper) checkFetchURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("only http(s) URLs with a host are accepted")
	}
	_, err = w.vettedAddrs(u.Hostname())
	return err
}

// vettedAddrs resolves host and returns its addresses, or an error when any
// of them is loopback, private, link-local, or unspecified and
// Config.AllowPrivateFetch is off.
func (w *WebHelper) vettedAddrs(host string) ([]net.IP, error) {
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else if host == "localhost" {
		ips = []net.IP{net.IPv4(127, 0, 0, 1)}
	} else {
		lookup := w.lookupIP
		if lookup == nil {
			lookup = net.LookupIP
		}
		resolved, err := lookup(host)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve %s: %w", host, err)
		}
		if len(resolved) == 0 {
			return nil, fmt.Errorf("cannot resolve %s: no addresses", host)
		}
		ips = resolved
	}
	if w.cfg.AllowPrivateFetch {
		return ips, nil
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return nil, fmt.Errorf("%s resolves to a private address, which is refused (AllowPrivateFetch)", host)
		}
	}
	return ips, nil
}

// pinnedDial resolves the host itself, applies the destination policy to
// the answer, and dials the vetted address, so the address checked is the
// address connected to. TLS still verifies against the hostname, which the
// transport takes from the request.
func (w *WebHelper) pinnedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := w.vettedAddrs(host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// fetchImageBytes GETs the URL with the destination policy applied at dial
// time on every hop and the body capped at the configured limit.
func (w *WebHelper) fetchImageBytes(ctx context.Context, raw string) ([]byte, error) {
	limit := w.cfg.MaxImageBytes
	if limit <= 0 {
		limit = defaultMaxImageBytes
	}
	base := w.cfg.HTTPClient
	if base == nil {
		base = &http.Client{Timeout: 60 * time.Second}
	}
	client := *base
	var transport *http.Transport
	switch rt := base.Transport.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		transport = rt.Clone()
	default:
		if !w.cfg.AllowPrivateFetch {
			return nil, errors.New("HTTPClient.Transport must be an *http.Transport for the private-address policy to pin the dialed address; set AllowPrivateFetch to use it as is")
		}
	}
	if transport != nil {
		transport.DialContext = w.pinnedDial
		client.Transport = transport
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return w.checkFetchURL(req.URL.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ds4go fetch_image")
	req.Header.Set("Accept", "image/png, image/jpeg")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", raw, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s is too large (over %d bytes)", raw, limit)
	}
	return data, nil
}
