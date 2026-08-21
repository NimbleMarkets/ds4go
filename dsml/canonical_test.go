package dsml

import (
	"strings"
	"testing"
)

// Tool schemas are part of the prompt, so forwarding the caller's JSON
// spelling verbatim makes semantically identical tools tokenize differently
// and needlessly miss the KV cache. Ported from upstream ds4's
// test_openai_tool_schema_json_spelling_is_canonical (ds4_server.c).

const (
	canonCompact = `{"type":"object","properties":{"command":{"type":"string",` +
		`"description":"line\nrocket 🚀"}},"required":["command"],"additionalProperties":false}`
	canonSpaced = `{ "type" : "object", "properties" : { "command" : { "type" : "string", ` +
		`"description" : "line\nrocket 🚀" } }, "required" : [ "command" ], ` +
		`"additionalProperties" : false }`
	canonPrettyEscaped = "{\n  \"type\": \"object\",\n  \"properties\": {\n" +
		"    \"command\": {\n      \"type\": \"string\",\n" +
		"      \"description\": \"line\\nrocket \\ud83d\\ude80\"\n    }\n  },\n" +
		"  \"required\": [\"command\"],\n  \"additionalProperties\": false\n}"

	canonExpected = `{"type": "object", "properties": {"command": {"type": "string", ` +
		`"description": "line\nrocket 🚀"}}, "required": ["command"], ` +
		`"additionalProperties": false}`
)

func TestCanonicalJSONSpelling(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
	}{
		{"compact", canonCompact},
		{"spaced", canonSpaced},
		{"pretty with \\u escapes", canonPrettyEscaped},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := canonicalJSON(c.in)
			if !ok {
				t.Fatalf("canonicalJSON(%q) reported failure", c.name)
			}
			if got != canonExpected {
				t.Errorf("canonicalJSON =\n%s\nwant\n%s", got, canonExpected)
			}
		})
	}
}

// Escaped non-ASCII is decoded to UTF-8 rather than kept as the caller's
// \u spelling, control characters use the short forms, and a decoded embedded
// NUL must not truncate the value (upstream's controls case).
func TestCanonicalJSONDecodesEscapesAndKeepsControls(t *testing.T) {
	got, ok := canonicalJSON("{\"d\":\"a\\u0000b\\b\\f\\u2014\"}")
	if !ok {
		t.Fatal("canonicalJSON reported failure")
	}
	want := "{\"d\": \"a\\u0000b\\b\\f\u2014\"}"
	if got != want {
		t.Errorf("canonicalJSON = %q, want %q", got, want)
	}
}

// Numbers keep their exact source lexeme rather than being reformatted.
func TestCanonicalJSONPreservesNumberLexemes(t *testing.T) {
	got, ok := canonicalJSON(`{"a":1,"b":1.50,"c":2e3,"d":-0.0}`)
	if !ok {
		t.Fatal("canonicalJSON reported failure")
	}
	want := `{"a": 1, "b": 1.50, "c": 2e3, "d": -0.0}`
	if got != want {
		t.Errorf("canonicalJSON = %q, want %q", got, want)
	}
}

// Object key order is insertion order, never sorted.
func TestCanonicalJSONPreservesKeyOrder(t *testing.T) {
	got, ok := canonicalJSON(`{"z":1,"a":2,"m":3}`)
	if !ok {
		t.Fatal("canonicalJSON reported failure")
	}
	if want := `{"z": 1, "a": 2, "m": 3}`; got != want {
		t.Errorf("canonicalJSON = %q, want %q", got, want)
	}
}

func TestCanonicalJSONRejectsInvalid(t *testing.T) {
	for _, in := range []string{
		`{"a":}`,
		`{"a":1} trailing`,
		`{"a":1e999}`, // non-finite
		``,
	} {
		if got, ok := canonicalJSON(in); ok {
			t.Errorf("canonicalJSON(%q) = %q, want failure", in, got)
		}
	}
}

// The rendered tools section must be identical for schemas that differ only in
// JSON spelling -- the property that actually protects the prompt cache.
func TestRenderToolsSectionSpellingIndependent(t *testing.T) {
	for _, syntax := range []Syntax{SyntaxDSML, SyntaxGLM} {
		t.Run(syntax.String(), func(t *testing.T) {
			a, err := RenderToolsSectionSyntax(syntax, []Tool{
				{Name: "bash", Description: "Run — now", Parameters: []byte(canonCompact)}})
			if err != nil {
				t.Fatalf("compact: %v", err)
			}
			b, err := RenderToolsSectionSyntax(syntax, []Tool{
				{Name: "bash", Description: "Run — now", Parameters: []byte(canonPrettyEscaped)}})
			if err != nil {
				t.Fatalf("pretty: %v", err)
			}
			if a != b {
				t.Error("tools section differs for schemas that differ only in JSON spelling")
			}
			if !strings.Contains(a, canonExpected) {
				t.Errorf("tools section does not carry the canonical schema\n%s", a)
			}
		})
	}
}

// ds4 keeps decoded UTF-8 and does not HTML-escape, where Go's encoding/json
// emits \u0026 / \u003c for & and <.
func TestRenderToolsSectionDoesNotHTMLEscape(t *testing.T) {
	section, err := RenderToolsSection([]Tool{{
		Name:        "sh",
		Description: `run a && b < c`,
		Parameters:  []byte(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("RenderToolsSection: %v", err)
	}
	if !strings.Contains(section, `run a && b < c`) {
		t.Errorf("description was escaped away:\n%s", section)
	}
	for _, bad := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(section, bad) {
			t.Errorf("tools section contains HTML escape %s", bad)
		}
	}
}
