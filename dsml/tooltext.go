package dsml

import "strings"

// Tool bodies are not HTML. Mirroring upstream ds4_tool_text.h, only a body's
// own closing delimiter (end) and the spellings that would otherwise collide
// with that escape are rewritten; every other entity is literal data.
//
// Escaping maps a literal end to "&lt;" + end[1:], and an already-escaped
// spelling "&" + ("amp;")* + "lt;" + end[1:] to "&amp;" + the rest.
// Unescaping reverses exactly one level and leaves everything else alone.

// toolTextEscapedClose reports whether s begins with an escaped spelling of
// the closing delimiter end: "&", any number of "amp;", then "lt;" + end[1:].
func toolTextEscapedClose(s, end string) bool {
	if !strings.HasPrefix(s, "&") {
		return false
	}
	s = s[1:]
	for strings.HasPrefix(s, "amp;") {
		s = s[4:]
	}
	return strings.HasPrefix(s, "lt;") && strings.HasPrefix(s[3:], end[1:])
}

// toolTextNeedsEscape reports whether the text at s must have its first byte
// escaped to keep the body inside its end delimiter.
func toolTextNeedsEscape(s, end string) bool {
	return strings.HasPrefix(s, end) || toolTextEscapedClose(s, end)
}

// escapeToolText renders body text for a wrapper closed by end, mirroring
// upstream append_glm_tag_body_text / append_dsml_parameter_text /
// append_tool_result_text.
func escapeToolText(s, end string) string {
	if !strings.Contains(s, end[1:]) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		if toolTextNeedsEscape(s[i:], end) {
			if s[i] == '<' {
				b.WriteString("&lt;")
			} else {
				b.WriteString("&amp;")
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// unescapeToolText reverses escapeToolText, mirroring ds4_tool_text_unescape.
func unescapeToolText(s, end string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if toolTextEscapedClose(s[i:], end) {
			if strings.HasPrefix(s[i:], "&amp;") {
				b.WriteByte('&')
				i += 5
			} else {
				b.WriteByte('<')
				i += 4
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// structuralWrappers are the argument-body wrappers whose contents are data:
// a structural marker such as </think> found inside one of them is not
// executable. Mirrors upstream find_tool_structural_text.
var structuralWrappers = func() [][2]string {
	var w [][2]string
	for _, table := range [][]dsmlSyntax{dsmlSyntaxes, dsml41Syntaxes} {
		for _, syn := range table {
			w = append(w, [2]string{syn.paramStart, syn.paramEnd})
		}
	}
	w = append(w, [2]string{glmArgKeyStart, glmArgKeyEnd}, [2]string{glmArgValueStart, glmArgValueEnd})
	return w
}()

// lastStructuralIndex returns the offset of the last occurrence of needle in
// text that lies outside any parameter, <arg_key>, or <arg_value> body, or
// -1. An unterminated wrapper ends the scan, returning what was found before
// it, mirroring upstream find_tool_structural_text(s, needle, true).
func lastStructuralIndex(text, needle string) int {
	found := -1
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], needle) {
			found = i
		}
		skipped := false
		for _, w := range structuralWrappers {
			if !strings.HasPrefix(text[i:], w[0]) {
				continue
			}
			tagEnd := strings.IndexByte(text[i:], '>')
			if tagEnd < 0 {
				return found
			}
			bodyStart := i + tagEnd + 1
			end := strings.Index(text[bodyStart:], w[1])
			if end < 0 {
				return found
			}
			i = bodyStart + end + len(w[1])
			skipped = true
			break
		}
		if !skipped {
			i++
		}
	}
	return found
}
