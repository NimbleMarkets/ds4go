package ds4

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const testPNG = "\x89PNG\r\n\x1a\nfake"

func dataURI(mime, payload string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString([]byte(payload))
}

func TestParseOpenAIContentString(t *testing.T) {
	text, parts, err := ParseOpenAIContent(json.RawMessage(`"hello there"`))
	if err != nil || text != "hello there" || parts != nil {
		t.Fatalf("string content = (%q, %v, %v)", text, parts, err)
	}
	text, parts, err = ParseOpenAIContent(nil)
	if err != nil || text != "" || parts != nil {
		t.Fatalf("absent content = (%q, %v, %v)", text, parts, err)
	}
	text, parts, err = ParseOpenAIContent(json.RawMessage(`null`))
	if err != nil || text != "" || parts != nil {
		t.Fatalf("null content = (%q, %v, %v)", text, parts, err)
	}
}

func TestParseOpenAIContentTextPartsCollapseToText(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`)
	text, parts, err := ParseOpenAIContent(raw)
	if err != nil || parts != nil {
		t.Fatalf("text-only array = (%q, %v, %v), want plain text and no parts", text, parts, err)
	}
	if text != "one\ntwo" {
		t.Errorf("text = %q, want the parts joined by newlines", text)
	}
}

func TestParseOpenAIContentImageDataURIs(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"text","text":"compare"},
		{"type":"image_url","image_url":{"url":"` + dataURI("image/png", testPNG) + `","detail":"auto"}},
		{"type":"image_url","image_url":"` + dataURI("image/jpeg", "\xff\xd8\xffjpg") + `"},
		{"type":"text","text":"which is brighter?"}
	]`)
	text, parts, err := ParseOpenAIContent(raw)
	if err != nil {
		t.Fatalf("ParseOpenAIContent: %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty when parts are returned", text)
	}
	if len(parts) != 4 || parts[0].Text != "compare" || parts[3].Text != "which is brighter?" {
		t.Fatalf("parts = %+v, want text, image, image, text in order", parts)
	}
	if parts[1].Image == nil || string(parts[1].Image.Data) != testPNG || parts[1].Image.Path != "" {
		t.Errorf("object-form image part = %+v, want decoded PNG bytes and no path", parts[1].Image)
	}
	if parts[2].Image == nil || string(parts[2].Image.Data) != "\xff\xd8\xffjpg" {
		t.Errorf("string-form image part = %+v, want decoded JPEG bytes", parts[2].Image)
	}
}

func TestParseOpenAIContentRejectsNonInlineImages(t *testing.T) {
	cases := map[string]string{
		"remote URL":     `[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]`,
		"file path":      `[{"type":"image_url","image_url":{"url":"/tmp/a.png"}}]`,
		"unsupported":    `[{"type":"image_url","image_url":{"url":"` + dataURI("image/gif", "GIF89a") + `"}}]`,
		"not base64":     `[{"type":"image_url","image_url":{"url":"data:image/png;base64,***"}}]`,
		"empty payload":  `[{"type":"image_url","image_url":{"url":"data:image/png;base64,"}}]`,
		"unknown part":   `[{"type":"input_audio","input_audio":{}}]`,
		"missing url":    `[{"type":"image_url"}]`,
		"malformed json": `[{"type":"text"`,
	}
	for name, raw := range cases {
		if _, _, err := ParseOpenAIContent(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: accepted %s", name, raw)
		}
	}
}

func TestParseOpenAIContentErrorNamesTheRejectedURL(t *testing.T) {
	_, _, err := ParseOpenAIContent(json.RawMessage(`[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]`))
	if err == nil || !strings.Contains(err.Error(), "data:image/png;base64") {
		t.Errorf("err = %v, want it to say inline data URIs are required", err)
	}
}

func TestMaxHTTPImagesMatchesUpstream(t *testing.T) {
	if MaxHTTPImages != 16 {
		t.Errorf("MaxHTTPImages = %d, want upstream ds4-server's 16", MaxHTTPImages)
	}
}
