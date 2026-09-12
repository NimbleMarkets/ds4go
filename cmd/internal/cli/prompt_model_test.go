package cli

import (
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

func TestPreflightPromptModelHintsTheAliasTyped(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	err := preflightPromptModel("vision-q2")
	if err == nil {
		t.Fatal("preflight passed for an uninstalled alias")
	}
	if !strings.Contains(err.Error(), "ds4go model download vision-q2") {
		t.Errorf("hint = %q, want it to name the alias typed", err)
	}
	if strings.Contains(err.Error(), models.RecommendedModelAlias) {
		t.Errorf("hint = %q, should not fall back to the recommended alias", err)
	}
}

func TestPreflightPromptModelKeepsGenericHintForPaths(t *testing.T) {
	t.Setenv("DS4_DIR", t.TempDir())
	err := preflightPromptModel("/nowhere/custom.gguf")
	if err == nil || !strings.Contains(err.Error(), "ds4go model download "+models.RecommendedModelAlias) {
		t.Errorf("hint = %v, want the recommended alias for a plain path", err)
	}
}
