package cli

import (
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go"
	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

func TestIsImageFile(t *testing.T) {
	if !isImageFile([]byte("\x89PNG\r\n\x1a\nrest")) || !isImageFile([]byte("\xff\xd8\xff\xe0JFIF")) {
		t.Error("PNG/JPEG magic not detected")
	}
	if isImageFile([]byte("hello world")) || isImageFile(nil) {
		t.Error("text detected as an image")
	}
}

func TestReadInputBecomesImagePart(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nxx")
	msg := readInputMessage("shot.png", data)
	if len(msg.parts) != 2 || msg.parts[1].Image == nil || !strings.Contains(msg.parts[0].Text, "shot.png") {
		t.Fatalf("image /read message = %+v", msg)
	}
	// The message carries the bytes, not the path: history is re-rendered
	// every turn, and the file may be gone or changed by then.
	if img := msg.parts[1].Image; string(img.Data) != string(data) || img.Path != "" {
		t.Fatalf("image part = {Data:%q Path:%q}, want the bytes and no path", img.Data, img.Path)
	}
	text := readInputMessage("notes.txt", []byte("plain notes"))
	if text.parts != nil || text.content != "plain notes" {
		t.Fatalf("text /read message = %+v", text)
	}
}

func TestChatPromptWithImagesUsesMultimodalBuilder(t *testing.T) {
	lib, ctl := ds4api.NewMockLibraryWithControls()
	ctl.SetVision(true)
	eng, err := lib.NewEngine(ds4.EngineOptions{VisionPath: "enc"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	enc := ds4.NewImageEncoder(eng)
	history := []cliMessage{{role: "user", parts: []ds4.ContentPart{{Text: "see"}, {Image: &ds4.ImageInput{Data: []byte("img")}}}}}
	p, err := buildChatPrompt(eng, enc, "sys", history, ds4.ThinkNone)
	if err != nil {
		t.Fatalf("buildChatPrompt: %v", err)
	}
	defer p.Free()
	if len(p.Images) != 1 {
		t.Errorf("got %d spans, want 1", len(p.Images))
	}
}

func TestVisionHintNamesFlagAndAlias(t *testing.T) {
	cfg := &cliopts.CLIConfig{Model: "/models/GLM-5.3-Flash-Q2.gguf"}
	hint := visionHint(cfg)
	if !strings.Contains(hint, "--vision") || !strings.Contains(hint, "glm53-vision") {
		t.Fatalf("hint = %q, want --vision and the encoder alias", hint)
	}
}
