package tui

import (
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

func TestFormatPartialModel(t *testing.T) {
	got := FormatPartialModel(80*1024*1024*1024, 81.2)
	if !strings.Contains(got, "GiB") {
		t.Fatalf("FormatPartialModel() = %q, want GiB", got)
	}
	if !strings.Contains(got, "%") {
		t.Fatalf("FormatPartialModel() = %q, want percent", got)
	}
}

func TestModelFlags(t *testing.T) {
	cases := []struct {
		name  string
		model models.Model
		want  string
	}{
		{"none", models.Model{}, ""},
		{"imatrix", models.Model{Imatrix: true}, "imatrix"},
		{"mtp", models.Model{Optional: true}, "mtp"},
		{"distributed", models.Model{Distributed: true}, "distributed"},
		// GLM models need their own marker: their tool markup, stop tokens, and
		// reasoning-effort prompt differ from DeepSeek's, so the family is not a
		// cosmetic detail when picking a model.
		{"glm", models.Model{GLM: true}, "glm"},
		{"glm imatrix", models.Model{GLM: true, Imatrix: true}, "imatrix, glm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModelFlags(c.model); got != c.want {
				t.Errorf("ModelFlags() = %q, want %q", got, c.want)
			}
		})
	}
}
