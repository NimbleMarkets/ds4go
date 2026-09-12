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

// checkFetchURL applies the scheme and destination policy to one URL.
func (w *WebHelper) checkFetchURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("only http(s) URLs with a host are accepted")
	}
	if w.cfg.AllowPrivateFetch {
		return nil
	}
	host := u.Hostname()
	ips := []net.IP{}
	if ip := net.ParseIP(host); ip != nil {
		ips = append(ips, ip)
	} else if host == "localhost" {
		ips = append(ips, net.IPv4(127, 0, 0, 1))
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("cannot resolve %s: %w", host, err)
		}
		ips = resolved
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("%s resolves to a private address, which fetch_image refuses (AllowPrivateFetch)", host)
		}
	}
	return nil
}

// fetchImageBytes GETs the URL with the destination policy re-applied on
// every redirect and the body capped at the configured limit.
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
