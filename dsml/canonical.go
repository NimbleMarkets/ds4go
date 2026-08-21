package dsml

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// canonicalJSONMaxDepth bounds nesting, mirroring ds4's JSON_MAX_NESTING.
const canonicalJSONMaxDepth = 64

// canonicalJSON rewrites a JSON value into the lexical form the DS4 tokenizer
// sees, ported from upstream ds4's json_prompt_value (ds4_server.c):
//
//   - object insertion order is preserved, never sorted;
//   - the default Python separators ", " and ": " are used;
//   - strings keep decoded UTF-8 rather than the caller's \u spelling, with
//     only ", \, the short control forms, and C0 controls escaped;
//   - numbers keep their exact source lexeme rather than being reformatted.
//
// Tool schemas are part of the prompt, so forwarding a caller's JSON spelling
// verbatim makes semantically identical tools tokenize differently and miss the
// prompt cache. Reports false for input that is not exactly one valid JSON
// value; callers fall back to the original text, as ds4-server does.
func canonicalJSON(raw string) (string, bool) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()

	var b strings.Builder
	if err := writeCanonicalValue(&b, dec, 0); err != nil {
		return "", false
	}
	// Exactly one value: reject trailing content.
	if _, err := dec.Token(); err != io.EOF {
		return "", false
	}
	return b.String(), true
}

func writeCanonicalValue(b *strings.Builder, dec *json.Decoder, depth int) error {
	if depth >= canonicalJSONMaxDepth {
		return fmt.Errorf("dsml: JSON nested deeper than %d", canonicalJSONMaxDepth)
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return writeCanonicalToken(b, dec, tok, depth)
}

func writeCanonicalToken(b *strings.Builder, dec *json.Decoder, tok json.Token, depth int) error {
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return writeCanonicalObject(b, dec, depth)
		case '[':
			return writeCanonicalArray(b, dec, depth)
		}
		return fmt.Errorf("dsml: unexpected %v", v)
	case string:
		writeCanonicalString(b, v)
		return nil
	case json.Number:
		// Keep the caller's lexeme, but reject values no JSON serializer
		// should have produced (1e999 and friends decode to infinity).
		f, err := strconv.ParseFloat(v.String(), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return fmt.Errorf("dsml: non-finite JSON number %q", v.String())
		}
		b.WriteString(v.String())
		return nil
	case bool:
		b.WriteString(boolStr(v))
		return nil
	case nil:
		b.WriteString("null")
		return nil
	}
	return fmt.Errorf("dsml: unsupported JSON token %T", tok)
}

func writeCanonicalObject(b *strings.Builder, dec *json.Decoder, depth int) error {
	b.WriteByte('{')
	first := true
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("dsml: object key is not a string")
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		writeCanonicalString(b, key)
		b.WriteString(": ")
		if err := writeCanonicalValue(b, dec, depth+1); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // consume '}'
		return err
	}
	b.WriteByte('}')
	return nil
}

func writeCanonicalArray(b *strings.Builder, dec *json.Decoder, depth int) error {
	b.WriteByte('[')
	first := true
	for dec.More() {
		if !first {
			b.WriteString(", ")
		}
		first = false
		if err := writeCanonicalValue(b, dec, depth+1); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // consume ']'
		return err
	}
	b.WriteByte(']')
	return nil
}

// writeCanonicalString escapes s the way ds4's json_prompt_escape_n does:
// only ", \, the short control forms, and remaining C0 controls. Everything
// else, including all non-ASCII, is emitted as decoded UTF-8 -- notably
// without encoding/json's HTML escaping of &, < and >.
func writeCanonicalString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\u%04x`, c)
				continue
			}
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

// canonicalJSONString renders s as a canonical JSON string literal, replacing
// toJSONString on prompt paths where encoding/json's HTML escaping would
// change the tokens the model sees.
func canonicalJSONString(s string) string {
	var b strings.Builder
	writeCanonicalString(&b, s)
	return b.String()
}

// canonicalSchemaParams canonicalizes a tool's parameters blob, falling back to
// the original bytes when it is not exactly one JSON value (ds4-server does the
// same rather than dropping the schema).
func canonicalSchemaParams(params []byte) string {
	if canonical, ok := canonicalJSON(string(params)); ok {
		return canonical
	}
	return string(params)
}
