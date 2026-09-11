package workspacetool

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go"
)

func viewImageWorkspace(t *testing.T, vision bool) (*Workspace, string) {
	t.Helper()
	root := t.TempDir()
	png := filepath.Join(root, "shot.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\npixels"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(Config{Root: root, VisionAvailable: func() bool { return vision }})
	if err != nil {
		t.Fatal(err)
	}
	return w, png
}

func invokeParts(t *testing.T, tool ds4.ToolHandler, args string) ds4.ToolResult {
	t.Helper()
	mm, ok := tool.(ds4.MultimodalToolHandler)
	if !ok {
		t.Fatal("view_image is not a MultimodalToolHandler")
	}
	res, err := mm.InvokeParts(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("InvokeParts: %v", err)
	}
	return res
}

func TestViewImageReturnsImagePart(t *testing.T) {
	w, png := viewImageWorkspace(t, true)
	res := invokeParts(t, w.ViewImageTool(), `{"path":"shot.png"}`)
	if len(res.Parts) != 2 || res.Parts[1].Image == nil {
		t.Fatalf("parts = %+v, want a caption then the image", res.Parts)
	}
	// The bytes are read once through the confined handle, not handed back as
	// a path the tool loop would re-read outside the os.Root guard.
	want, err := os.ReadFile(png)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Parts[1].Image; !bytes.Equal(got.Data, want) || got.Path != "" {
		t.Fatalf("image part = (%d bytes, path %q), want the %d file bytes and no path", len(got.Data), got.Path, len(want))
	}
	if !strings.Contains(res.Parts[0].Text, "shot.png") {
		t.Errorf("caption %q does not name the file", res.Parts[0].Text)
	}
}

func TestViewImageRejectsOversizeImage(t *testing.T) {
	root := t.TempDir()
	w, err := New(Config{Root: root, VisionAvailable: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "huge.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatal(err)
	}
	// Sparse: one byte past the bound without writing 64 MiB.
	if err := f.Truncate(maxImageBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	res := invokeParts(t, w.ViewImageTool(), `{"path":"huge.png"}`)
	if len(res.Parts) != 1 || res.Parts[0].Image != nil || !strings.HasPrefix(res.Parts[0].Text, "ERROR") {
		t.Fatalf("parts = %+v, want a text error for an oversize image", res.Parts)
	}
	if !strings.Contains(res.Parts[0].Text, "too large") {
		t.Errorf("observation %q does not say the file is too large", res.Parts[0].Text)
	}
}

func TestViewImageRejectsNonImages(t *testing.T) {
	w, _ := viewImageWorkspace(t, true)
	res := invokeParts(t, w.ViewImageTool(), `{"path":"notes.txt"}`)
	if len(res.Parts) != 1 || res.Parts[0].Image != nil || !strings.Contains(res.Parts[0].Text, "not a PNG or JPEG") {
		t.Fatalf("parts = %+v, want a text error", res.Parts)
	}
	res = invokeParts(t, w.ViewImageTool(), `{"path":"../outside.png"}`)
	if len(res.Parts) != 1 || !strings.HasPrefix(res.Parts[0].Text, "ERROR") {
		t.Fatalf("path escape: parts = %+v", res.Parts)
	}
	res = invokeParts(t, w.ViewImageTool(), `{}`)
	if len(res.Parts) != 1 || !strings.HasPrefix(res.Parts[0].Text, "ERROR") {
		t.Fatalf("missing path: parts = %+v", res.Parts)
	}
}

func TestViewImageWithoutVisionIsATextObservation(t *testing.T) {
	w, _ := viewImageWorkspace(t, false)
	res := invokeParts(t, w.ViewImageTool(), `{"path":"shot.png"}`)
	if len(res.Parts) != 1 || res.Parts[0].Image != nil || !strings.Contains(res.Parts[0].Text, "--vision") {
		t.Fatalf("parts = %+v, want a text hint naming --vision", res.Parts)
	}
}

func TestViewImageRejectsLargeNonImage(t *testing.T) {
	root := t.TempDir()
	w, err := New(Config{Root: root, VisionAvailable: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 1<<20)
	copy(big, []byte("not an image, just a large text-ish prefix"))
	if err := os.WriteFile(filepath.Join(root, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	res := invokeParts(t, w.ViewImageTool(), `{"path":"big.bin"}`)
	if len(res.Parts) != 1 || res.Parts[0].Image != nil || !strings.Contains(res.Parts[0].Text, "not a PNG or JPEG") {
		t.Fatalf("parts = %+v, want a text error for a large non-image file", res.Parts)
	}
}

func TestViewImageContextCancelled(t *testing.T) {
	w, _ := viewImageWorkspace(t, true)
	mm, ok := w.ViewImageTool().(ds4.MultimodalToolHandler)
	if !ok {
		t.Fatal("view_image is not a MultimodalToolHandler")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mm.InvokeParts(ctx, json.RawMessage(`{"path":"shot.png"}`))
	if err == nil || err != context.Canceled {
		t.Fatalf("InvokeParts with cancelled ctx: err = %v, want context.Canceled", err)
	}
}

func TestRegisterReadOnlyIncludesViewImage(t *testing.T) {
	w, _ := viewImageWorkspace(t, true)
	reg := ds4.NewToolRegistry()
	if err := w.RegisterReadOnly(reg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range reg.Schemas() {
		if s.Name == "view_image" {
			found = true
		}
	}
	if !found {
		t.Fatal("view_image not registered by RegisterReadOnly")
	}
}
