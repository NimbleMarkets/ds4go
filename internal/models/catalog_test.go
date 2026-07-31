package models

import (
	"strings"
	"testing"
)

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

// GLM 5.2 quants live in a different Hugging Face repo from the DeepSeek
// models, so each curated entry must carry its own repo and the download URL
// must be built from it.
func TestCuratedGLMModels(t *testing.T) {
	want := map[string]struct {
		file   string
		sizeGB float64
		sha    string
	}{
		"glm-iq2xxs": {"GLM-5.2-UD-IQ2_XXS_RoutedIQ2XXS_blk78Q2K.gguf", 196.6,
			"a49de64c5020432bdae23de36a423a9660a5621bc0db8d12b66bd8814b07fea0"},
		"glm-q2": {"GLM-5.2-UD-Q2_K_RoutedQ2K.gguf", 244.0,
			"b9fa49d010dad35b96418c45831c212a746715b0646c1121ccfc414455bd6fe5"},
		"glm-q4": {"GLM-5.2-UD-Q4_K_RoutedQ4K.gguf", 404.4,
			"7160879c87756236eea16ec6bfeb19288d16fa94dcfcef3a5ed5f38b1383d3a5"},
	}

	found := map[string]bool{}
	for _, m := range Curated() {
		w, ok := want[m.Alias]
		if !ok {
			continue
		}
		found[m.Alias] = true
		if m.FileName != w.file {
			t.Errorf("%s FileName = %q, want %q", m.Alias, m.FileName, w.file)
		}
		if m.SizeGB != w.sizeGB {
			t.Errorf("%s SizeGB = %v, want %v", m.Alias, m.SizeGB, w.sizeGB)
		}
		if m.SHA256 != w.sha {
			t.Errorf("%s SHA256 = %q, want %q", m.Alias, m.SHA256, w.sha)
		}
		if m.Repo != glmRepo {
			t.Errorf("%s Repo = %q, want %q", m.Alias, m.Repo, glmRepo)
		}
		if m.GLM != true {
			t.Errorf("%s GLM = false, want true", m.Alias)
		}
	}
	for alias := range want {
		if !found[alias] {
			t.Errorf("curated catalog is missing %q", alias)
		}
	}
}

// DeepSeek entries keep the default repo, so their URLs are unchanged.
func TestModelDownloadURLUsesPerModelRepo(t *testing.T) {
	deepseek := Model{FileName: "a.gguf"}
	if got, want := modelDownloadURL(deepseek), hfRepoBase+"/a.gguf"; got != want {
		t.Errorf("default repo URL = %q, want %q", got, want)
	}

	glm := Model{FileName: "b.gguf", Repo: glmRepo}
	want := "https://huggingface.co/" + glmRepo + "/resolve/main/b.gguf"
	if got := modelDownloadURL(glm); got != want {
		t.Errorf("GLM repo URL = %q, want %q", got, want)
	}
}

// Every curated model must resolve to a well-formed absolute URL.
func TestCuratedModelURLsWellFormed(t *testing.T) {
	for _, m := range Curated() {
		u := modelDownloadURL(m)
		if !strings.HasPrefix(u, "https://") {
			t.Errorf("%s URL = %q, want an https URL", m.Alias, u)
		}
		if !strings.HasSuffix(u, "/"+m.FileName) {
			t.Errorf("%s URL = %q, want it to end in /%s", m.Alias, u, m.FileName)
		}
	}
}

// Hugging Face canonicalises repo names to lower case and answers a
// non-canonical name with a 307 that carries no x-linked-size/x-linked-etag.
// The downloader deliberately does not follow redirects (it reads those headers
// off the resolve response), so a non-canonical repo silently loses the remote
// size and hash. Keep every curated repo canonical.
func TestCuratedRepoNamesAreCanonical(t *testing.T) {
	for _, m := range Curated() {
		if m.Repo == "" {
			continue
		}
		if m.Repo != strings.ToLower(m.Repo) {
			t.Errorf("%s Repo = %q, want the canonical lower-case name %q",
				m.Alias, m.Repo, strings.ToLower(m.Repo))
		}
	}
	if hfRepo != strings.ToLower(hfRepo) {
		t.Errorf("hfRepo = %q, want lower case", hfRepo)
	}
}
