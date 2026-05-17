package models

import "testing"

func TestCuratedModelsHavePinnedSHA256(t *testing.T) {
	for _, model := range Curated() {
		if !sha256Re.MatchString(model.SHA256) {
			t.Fatalf("%s SHA256 = %q, want 64 hex chars", model.Alias, model.SHA256)
		}
	}
}
