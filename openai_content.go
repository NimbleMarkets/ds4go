package ds4

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MaxHTTPImages is the per-request image cap upstream ds4-server applies to
// its HTTP APIs. Servers built on ds4go enforce it over the whole message
// list; ParseOpenAIContent only parses one message.
const MaxHTTPImages = 16

// ParseOpenAIContent decodes an OpenAI chat message's "content" field. A JSON
// string (or null/absent) is returned as text. An array of parts yields
// text when every part is text (joined by newlines), or ordered ContentParts
// when any part is an image. As in upstream ds4-server, images must be
// inline PNG or JPEG data URIs; remote URLs and file paths are rejected.
func ParseOpenAIContent(raw json.RawMessage) (text string, parts []ContentPart, err error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil, nil
	}
	if trimmed[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", nil, fmt.Errorf("ds4go: content: %w", err)
		}
		return text, nil, nil
	}
	var items []openAIContentPart
	if err := json.Unmarshal(raw, &items); err != nil {
		return "", nil, fmt.Errorf("ds4go: content must be a string or an array of parts: %w", err)
	}
	images := 0
	parts = make([]ContentPart, 0, len(items))
	for i, item := range items {
		switch item.Type {
		case "text":
			parts = append(parts, ContentPart{Text: item.Text})
		case "image_url":
			data, err := decodeImageDataURI(item.imageURL())
			if err != nil {
				return "", nil, fmt.Errorf("ds4go: content part %d: %w", i, err)
			}
			parts = append(parts, ContentPart{Image: &ImageInput{Data: data}})
			images++
		default:
			return "", nil, fmt.Errorf("ds4go: content part %d: unsupported type %q", i, item.Type)
		}
	}
	if images == 0 {
		texts := make([]string, len(parts))
		for i, p := range parts {
			texts[i] = p.Text
		}
		return strings.Join(texts, "\n"), nil, nil
	}
	return "", parts, nil
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
}

// imageURL accepts both the documented {"url": ...} object and the bare
// string some clients send.
func (p openAIContentPart) imageURL() string {
	var s string
	if json.Unmarshal(p.ImageURL, &s) == nil {
		return s
	}
	var obj struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(p.ImageURL, &obj)
	return obj.URL
}

var errImageNotInline = errors.New("image_url must be an inline data:image/png;base64,... or data:image/jpeg;base64,... URI; remote URLs and file paths are not accepted")

func decodeImageDataURI(url string) ([]byte, error) {
	rest, ok := strings.CutPrefix(url, "data:")
	if !ok {
		return nil, errImageNotInline
	}
	mime, payload, ok := strings.Cut(rest, ";base64,")
	if !ok {
		return nil, errImageNotInline
	}
	switch mime {
	case "image/png", "image/jpeg", "image/jpg":
	default:
		return nil, fmt.Errorf("unsupported image type %q: %w", mime, errImageNotInline)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("image_url base64: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("image_url data URI is empty")
	}
	return data, nil
}
