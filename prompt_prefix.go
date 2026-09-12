package ds4

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

// PromptPrefixTurn is one preloaded conversation turn from a --prefix-file:
// Role is "user" or "assistant".
type PromptPrefixTurn struct {
	Role    string
	Content string
}

// ParsePromptPrefix parses upstream ds4's prefix-file format
// (ds4_prompt_prefix.c): lines starting with "USER:" or "ASSISTANT:" open a
// turn, turns strictly alternate starting with USER, a turn's content runs
// until the next marker line, and the file must end with an ASSISTANT turn.
// A leading UTF-8 BOM is skipped; CRLF line ends are accepted.
func ParsePromptPrefix(data []byte) ([]PromptPrefixTurn, error) {
	if len(data) == 0 {
		return nil, errors.New("prefix file is empty")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("prefix file contains a NUL byte")
	}
	pos := 0
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		pos = 3
	}
	line := 1
	var turns []PromptPrefixTurn
	for pos < len(data) {
		role, markerLen, ok := prefixMarkerAt(data, pos)
		if !ok {
			return nil, fmt.Errorf("line %d must start with USER: or ASSISTANT:", line)
		}
		expected := "user"
		if len(turns)%2 == 1 {
			expected = "assistant"
		}
		if role != expected {
			return nil, fmt.Errorf("line %d: expected %s:", line, strings.ToUpper(expected))
		}
		contentStart := pos + markerLen
		if contentStart < len(data) && (data[contentStart] == ' ' || data[contentStart] == '\t') {
			contentStart++
		}
		// The turn runs to the next line that opens a marker.
		next, nextLine, scanLine := len(data), line, line
		for i := contentStart; i < len(data); i++ {
			if data[i] != '\n' {
				continue
			}
			scanLine++
			if i+1 < len(data) {
				if _, _, ok := prefixMarkerAt(data, i+1); ok {
					next, nextLine = i+1, scanLine
					break
				}
			}
		}
		contentEnd := next
		if contentEnd > contentStart && data[contentEnd-1] == '\n' {
			contentEnd--
			if contentEnd > contentStart && data[contentEnd-1] == '\r' {
				contentEnd--
			}
		}
		if contentEnd == contentStart {
			return nil, fmt.Errorf("line %d has an empty %s turn", line, strings.ToUpper(role))
		}
		turns = append(turns, PromptPrefixTurn{Role: role, Content: string(data[contentStart:contentEnd])})
		pos, line = next, nextLine
	}
	if len(turns) == 0 {
		return nil, errors.New("prefix file contains no turns")
	}
	if turns[len(turns)-1].Role != "assistant" {
		return nil, errors.New("prefix file must end with an ASSISTANT turn")
	}
	return turns, nil
}

func prefixMarkerAt(data []byte, pos int) (role string, markerLen int, ok bool) {
	rest := data[pos:]
	switch {
	case bytes.HasPrefix(rest, []byte("USER:")):
		return "user", len("USER:"), true
	case bytes.HasPrefix(rest, []byte("ASSISTANT:")):
		return "assistant", len("ASSISTANT:"), true
	}
	return "", 0, false
}

// LoadPromptPrefix reads and parses a prefix file; errors name the path.
func LoadPromptPrefix(path string) ([]PromptPrefixTurn, error) {
	if path == "" {
		return nil, errors.New("invalid prefix file path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open prefix file %s: %w", path, err)
	}
	turns, err := ParsePromptPrefix(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return turns, nil
}

// AppendPromptPrefix renders turns into tokens the way upstream's inline
// ds4_prompt_prefix_append does: each turn through ChatAppendMessage, and a
// DeepSeek assistant turn closed with EOS (GLM's template closes its own).
func AppendPromptPrefix(engine *Engine, tokens *Tokens, turns []PromptPrefixTurn) error {
	glm := engine.IsGLMDSA()
	for _, turn := range turns {
		if err := engine.ChatAppendMessage(tokens, turn.Role, turn.Content); err != nil {
			return err
		}
		if turn.Role == "assistant" && !glm {
			tokens.Push(engine.TokenEOS())
		}
	}
	return nil
}
