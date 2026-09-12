package webtool

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fetchHelper(t *testing.T, vision bool, maxBytes int) *WebHelper {
	t.Helper()
	return NewWebHelper(Config{
		HomeDir:           t.TempDir(),
		VisionAvailable:   func() bool { return vision },
		AllowPrivateFetch: true, // httptest listens on loopback
		MaxImageBytes:     maxBytes,
	})
}

func fetchImage(t *testing.T, w *WebHelper, url string) ds4.ToolResult {
	t.Helper()
	tool, ok := w.FetchImageTool().(ds4.MultimodalTool)
	if !ok {
		t.Fatal("FetchImageTool is not a MultimodalTool")
	}
	args, _ := json.Marshal(map[string]string{"url": url})
	res, err := tool.Handler(context.Background(), args)
	if err != nil {
		t.Fatalf("fetch_image(%s): %v", url, err)
	}
	return res
}

func hasImage(res ds4.ToolResult) bool {
	for _, p := range res.Parts {
		if p.Image != nil {
			return true
		}
	}
	return false
}

func resultText(res ds4.ToolResult) string {
	var b strings.Builder
	for _, p := range res.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func TestFetchImageReturnsTheServedImage(t *testing.T) {
	pngBytes := testPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream") // signature, not header, decides
		_, _ = w.Write(pngBytes)
	}))
	defer srv.Close()
	res := fetchImage(t, fetchHelper(t, true, 0), srv.URL+"/pic.png")
	if len(res.Parts) != 2 || res.Parts[1].Image == nil {
		t.Fatalf("result = %+v, want a caption part and an image part", res)
	}
	if !bytes.Equal(res.Parts[1].Image.Data, pngBytes) || res.Parts[1].Image.Path != "" {
		t.Error("image part does not carry the served bytes")
	}
	if !strings.Contains(res.Parts[0].Text, "[tool:fetch_image]") || !strings.Contains(res.Parts[0].Text, "/pic.png") {
		t.Errorf("caption = %q", res.Parts[0].Text)
	}
}

func TestFetchImageRejectsNonImagesAndOversize(t *testing.T) {
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png") // lying header
		_, _ = w.Write([]byte("<html>not an image</html>"))
	}))
	defer html.Close()
	res := fetchImage(t, fetchHelper(t, true, 0), html.URL+"/a.png")
	if hasImage(res) || !strings.Contains(resultText(res), "not a PNG or JPEG") {
		t.Errorf("html body: %+v", res)
	}

	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append(testPNG(t), bytes.Repeat([]byte{0}, 4096)...))
	}))
	defer big.Close()
	res = fetchImage(t, fetchHelper(t, true, 1024), big.URL+"/big.png")
	if hasImage(res) || !strings.Contains(resultText(res), "too large") {
		t.Errorf("oversize body: %+v", res)
	}

	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	res = fetchImage(t, fetchHelper(t, true, 0), missing.URL+"/gone.png")
	if hasImage(res) || !strings.Contains(resultText(res), "404") {
		t.Errorf("404: %+v", res)
	}
}

// Private, loopback, and link-local destinations are refused by default, on
// the first hop and on redirects, so a model cannot be steered at services
// behind the host running the tool.
func TestFetchImageRefusesPrivateHostsUnlessAllowed(t *testing.T) {
	pngBytes := testPNG(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(pngBytes) }))
	defer target.Close()
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/pic.png", http.StatusFound)
	}))
	defer hop.Close()

	strict := NewWebHelper(Config{HomeDir: t.TempDir(), VisionAvailable: func() bool { return true }})
	for _, u := range []string{target.URL + "/pic.png", hop.URL + "/r", "http://10.0.0.5/x.png", "http://169.254.169.254/latest/meta-data"} {
		res := fetchImage(t, strict, u)
		if hasImage(res) || !strings.Contains(resultText(res), "private") {
			t.Errorf("%s: %+v, want a refusal naming private addresses", u, res)
		}
	}
	for _, u := range []string{"file:///etc/hosts", "ftp://example.com/a.png", "not a url"} {
		res := fetchImage(t, strict, u)
		if hasImage(res) || !strings.Contains(resultText(res), "http") {
			t.Errorf("%s: %+v, want a refusal naming http(s)", u, res)
		}
	}
	// Allowed: the redirect chain is followed and the image comes back.
	res := fetchImage(t, fetchHelper(t, true, 0), hop.URL+"/r")
	if !hasImage(res) {
		t.Errorf("allowed private fetch via redirect: %+v", res)
	}
}

func TestFetchImageWithoutVisionReturnsTheHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(testPNG(t)) }))
	defer srv.Close()
	res := fetchImage(t, fetchHelper(t, false, 0), srv.URL+"/pic.png")
	if hasImage(res) || !strings.Contains(resultText(res), "--vision") {
		t.Errorf("without vision: %+v", res)
	}
}

// The registry accepts the tool and lists it; the multimodal invoke path
// (used by the tool loop) carries the image, the text-only one refuses it.
func TestFetchImageThroughTheRegistry(t *testing.T) {
	pngBytes := testPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(pngBytes) }))
	defer srv.Close()
	tool := fetchHelper(t, true, 0).FetchImageTool()
	reg := ds4.NewToolRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, s := range reg.Schemas() {
		if s.Name == "fetch_image" {
			listed = true
		}
	}
	if !listed {
		t.Fatal("fetch_image not listed by the registry")
	}
	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/pic.png"})
	res, err := tool.(ds4.MultimodalTool).InvokeParts(context.Background(), args)
	if err != nil {
		t.Fatalf("InvokeParts: %v", err)
	}
	if !hasImage(res) || !bytes.Equal(res.Parts[1].Image.Data, pngBytes) {
		t.Errorf("InvokeParts result = %+v", res)
	}
	if _, err := tool.Invoke(context.Background(), args); err == nil {
		t.Error("text-only Invoke accepted an image result")
	}
}
