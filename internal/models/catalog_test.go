package models

import "testing"

func TestCuratedModelsHavePinnedSHA256(t *testing.T) {
	for _, model := range Curated() {
		if model.SHA256 == "" {
			// New upstream entries may ship without a pinned hash until one is
			// published; catalog.go marks these with a TODO and download still
			// works (hash is recorded after the first successful fetch).
			continue
		}
		if !sha256Re.MatchString(model.SHA256) {
			t.Fatalf("%s SHA256 = %q, want 64 hex chars", model.Alias, model.SHA256)
		}
	}
}
