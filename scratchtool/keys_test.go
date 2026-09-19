package scratchtool

import (
	"strings"
	"testing"
)

func TestValidateKey(t *testing.T) {
	valid := []string{
		"plan",
		"findings",
		"open",
		"user",
		"session",
		"a",
		"0",
		"a1",
		"key-with-dash",
		"key_with_underscore",
		"dotted.name",
		"trailing.",
		"trailing-",
		"_leading-underscore",
		strings.Repeat("k", 64),
	}
	for _, key := range valid {
		if err := validateKey(key); err != nil {
			t.Errorf("validateKey(%q) = %v, want nil", key, err)
		}
	}

	invalid := []string{
		"",
		"UPPER",
		"MixedCase",
		"has space",
		"has\ttab",
		"has\nnewline",
		"a/b",
		`a\b`,
		"a\x00b",
		".hidden",
		".",
		"-leading-dash",
		"a..b",
		"..",
		"index.json",
		"café",
		"日本語",
		strings.Repeat("k", 65),
	}
	for _, key := range invalid {
		if err := validateKey(key); err == nil {
			t.Errorf("validateKey(%q) = nil, want error", key)
		}
	}
}
