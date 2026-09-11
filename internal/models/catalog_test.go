package models

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestModelForPath(t *testing.T) {
	glm, ok := Lookup("glm-q2")
	if !ok {
		t.Fatal("missing glm-q2 catalog entry")
	}
	if got, ok := ModelForPath(filepath.Join("somewhere", glm.FileName)); !ok || got.Alias != glm.Alias {
		t.Fatalf("direct ModelForPath = (%q, %v), want (%q, true)", got.Alias, ok, glm.Alias)
	}

	if _, ok := ModelForPath(filepath.Join("somewhere", "custom.gguf")); ok {
		t.Fatal("custom model unexpectedly matched the curated catalog")
	}

	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	target := filepath.Join(dir, glm.FileName)
	if err := os.WriteFile(target, []byte("model"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, DefaultModelSymlink)
	if err := os.Symlink(glm.FileName, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got, ok := ModelForPath(link); !ok || !got.GLM {
		t.Fatalf("symlink ModelForPath = (%+v, %v), want GLM model", got, ok)
	}

	hardLink := filepath.Join(dir, "active.gguf")
	if err := os.Link(target, hardLink); err != nil {
		t.Skipf("hard link unavailable: %v", err)
	}
	if got, ok := ModelForPath(hardLink); !ok || !got.GLM {
		t.Fatalf("hard-link ModelForPath = (%+v, %v), want GLM model", got, ok)
	}
}

// Upstream refreshed the Flash and Pro quants under date-stamped names and
// added MXFP4 and DSpark-support builds. The undated entries stay so existing
// installs keep working; the dated ones are what ds4's download_model.sh now
// fetches.
func TestCuratedRefreshedQuants(t *testing.T) {
	want := map[string]struct {
		file   string
		sizeGB float64
		sha    string
	}{
		"q2-imatrix-0731": {
			"DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix-0731.gguf", 80.8,
			"ca22ae2f838e14077c22bc1c1417b71b45b5e5a3687bd96c2ac6e17fdb6261c0"},
		"q2-q4-imatrix-0731": {
			"DeepSeek-V4-Flash-Layers37-42Q4KExperts-OtherExpertLayersIQ2XXSGateUp-Q2KDown-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix-fixed-0731.gguf", 90.9,
			"659e22fbd01c9e13ea37a57c8d9c41e0a8819dffa3473d3c5286ee44b2d3398f"},
		"q4-imatrix-0731": {
			"DeepSeek-V4-Flash-Q4KExperts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2-imatrix-0731.gguf", 153.3,
			"6bb77b5ddcbc2d974c687cfb63d644ecfb295581b4a53fa4c1d810aea538254a"},
		"mxfp4-0731": {
			"DeepSeek-V4-Flash-MXFP4Experts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2-mxfp4-0731.gguf", 145.3,
			"0e3a161b670f686128ec5f92a601dfde616a37bf5e7e48999fa2d32471b57ec6"},
		"pro-q2-imatrix-0813": {
			"DeepSeek-V4-Pro-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-Instruct-imatrix-0813.gguf", 432.7,
			"c4d997ab9894b6c78b759f7869fe1726b6314b6515f6ff82607df3797c5eb193"},
		"dspark-support": {
			"DeepSeek-V4-Flash-DSpark-support-0731.gguf", 5.6,
			"7e319924541db3f7a163ed7e11d7532a70d48228ab59d36cb81e1d4511885360"},
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
	}
	for alias := range want {
		if !found[alias] {
			t.Errorf("curated catalog is missing %q", alias)
		}
	}
}

// The undated aliases must keep their original files, so an existing install is
// still recognised rather than silently orphaned by the refresh.
func TestCuratedUndatedAliasesUnchanged(t *testing.T) {
	want := map[string]string{
		"q2-imatrix":     "DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix.gguf",
		"q4-imatrix":     "DeepSeek-V4-Flash-Q4KExperts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out-chat-v2-imatrix.gguf",
		"pro-q2-imatrix": "DeepSeek-V4-Pro-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-Instruct-imatrix.gguf",
	}
	for _, m := range Curated() {
		if file, ok := want[m.Alias]; ok && m.FileName != file {
			t.Errorf("%s FileName = %q, want the original %q", m.Alias, m.FileName, file)
		}
	}
}

// Catalog sizes feed the pre-flight disk-space check, so a wrong figure makes
// it either over-strict or useless. These are the real object sizes.
func TestCuratedSizesMatchPublishedObjects(t *testing.T) {
	want := map[string]float64{
		"q2-imatrix":             80.8,
		"q2-q4-imatrix":          90.9,
		"q4-imatrix":             153.3,
		"pro-q2-imatrix":         432.7,
		"pro-q4-layers00-30":     426.1,
		"pro-q4-layers31-output": 411.6,
		"mtp":                    3.5,
	}
	for _, m := range Curated() {
		if size, ok := want[m.Alias]; ok && m.SizeGB != size {
			t.Errorf("%s SizeGB = %v, want %v", m.Alias, m.SizeGB, size)
		}
	}
}

// GLM 5.3 Flash (antirez/glm-5.3-flash-gguf) and the full GLM 5.3 checkpoint
// (antirez/glm-5.3-gguf) mirror upstream download_model.sh's glm53-q2,
// glm53-q4, and glm53-full-q2 targets. Sizes are GiB from x-linked-size and
// hashes from x-linked-etag on the resolve URL.
func TestCuratedGLM53Models(t *testing.T) {
	want := map[string]struct {
		file   string
		repo   string
		sizeGB float64
		sha    string
	}{
		"glm53-q2": {"GLM-5.3-Flash-Q2.gguf", glm53FlashRepo, 89.9,
			"e81fd6241c6e55a64e1e14e47a3eab61a173fa8d7e4b5c1d1848827119705b32"},
		"glm53-q4": {"GLM-5.3-Flash-Q4_K.gguf", glm53FlashRepo, 177.8,
			"c7a0d950363238dd7804782c88340d737775aba53a15f8d4fdcc34e984f25221"},
		"glm53-full-q2": {"GLM-5.3-UD-IQ2_XXS_RoutedIQ2XXS_blk78Q2K.gguf", glm53FullRepo, 196.6,
			"059b36accd4c9acf73099da9f703b574d627869d619b7c4c316aa856e33d472e"},
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
		if m.Repo != w.repo {
			t.Errorf("%s Repo = %q, want %q", m.Alias, m.Repo, w.repo)
		}
		if m.SizeGB != w.sizeGB {
			t.Errorf("%s SizeGB = %v, want %v", m.Alias, m.SizeGB, w.sizeGB)
		}
		if m.SHA256 != w.sha {
			t.Errorf("%s SHA256 = %q, want %q", m.Alias, m.SHA256, w.sha)
		}
		if !m.GLM {
			t.Errorf("%s GLM = false, want true (embedded MTP, GLM tool syntax)", m.Alias)
		}
		if got := modelDownloadURL(m); got != "https://huggingface.co/"+w.repo+"/resolve/main/"+w.file {
			t.Errorf("%s download URL = %q", m.Alias, got)
		}
	}
	for alias := range want {
		if !found[alias] {
			t.Errorf("curated catalog is missing %q", alias)
		}
	}
}

func TestCuratedVisionModels(t *testing.T) {
	want := map[string]struct {
		file, repo, encoder string
		sizeGB              float64
		sha                 string
		vision, optional    bool
	}{
		"vision-q2":             {"DeepSeek-V4-Flash-Vision-Exp-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8.gguf", hfRepo, "vision-encoder", 80.8, "8f2d42c0071ccf8a98f391cc2b835fd123f12330690b3059dbb7707920e5ad9e", true, false},
		"vision-q2-q4":          {"DeepSeek-V4-Flash-Vision-Exp-Layers37-42Q4KExperts-OtherExpertLayersIQ2XXSGateUp-Q2KDown-AProjQ8-SExpQ8-OutQ8.gguf", hfRepo, "vision-encoder", 90.9, "cded4517bb9d033e778e8bc4ccf1e79ba96d1c2d2b9f1c071c1d4a9037c51b02", true, false},
		"vision-mxfp4":          {"DeepSeek-V4-Flash-Vision-Exp-MXFP4Experts-F16HC-F16Compressor-F16Indexer-Q8Attn-Q8Shared-Q8Out.gguf", hfRepo, "vision-encoder", 145.3, "fc1efb96fa26e654b3530ce5f4b926b189a936d41d94dc1903c832f1e18eb3e7", true, false},
		"vision-encoder":        {"DeepSeek-V4-Flash-Vision-Encoder.gguf", hfRepo, "", 0.9, "00cd4d81a435364967400a95c42703343e11da6b6f18c5143fe76e1d94d5035f", false, true},
		"vision-dspark-support": {"DeepSeek-V4-Flash-Vision-Exp-DSpark-support.gguf", hfRepo, "", 5.6, "0807a67fd9ce5874bfc60d8d2461f50e11657e3dd94913d3473f85aa679bc877", false, true},
		"glm53-vision":          {"GLM-5.3-Flash-Vision-Encoder.gguf", glm53FlashRepo, "", 1.0, "ae23e14c6979e889051b2e4a39351abcdafb161e18e606fae4d8c40095a4bf3a", false, true},
	}
	found := map[string]bool{}
	for _, m := range Curated() {
		w, ok := want[m.Alias]
		if !ok {
			continue
		}
		found[m.Alias] = true
		if m.FileName != w.file || m.SizeGB != w.sizeGB || m.SHA256 != w.sha || m.Encoder != w.encoder || m.Vision != w.vision || m.Optional != w.optional {
			t.Errorf("%s = %+v, want %+v", m.Alias, m, w)
		}
		if repo := m.Repo; repo == "" {
			repo = hfRepo
		} else if repo != w.repo {
			t.Errorf("%s Repo = %q, want %q", m.Alias, repo, w.repo)
		}
	}
	for alias := range want {
		if !found[alias] {
			t.Errorf("curated catalog is missing %q", alias)
		}
	}
	for _, m := range Curated() {
		switch m.Alias {
		case "glm53-q2", "glm53-q4":
			if !m.Vision || m.Encoder != "glm53-vision" {
				t.Errorf("%s should pair with glm53-vision (Vision=%v Encoder=%q)", m.Alias, m.Vision, m.Encoder)
			}
		case "vision-q2", "vision-q2-q4", "vision-mxfp4":
			if m.DSpark != "vision-dspark-support" {
				t.Errorf("%s DSpark = %q, want vision-dspark-support", m.Alias, m.DSpark)
			}
		}
	}
	// Every Encoder/DSpark alias resolves to a catalog entry.
	aliases := map[string]bool{}
	for _, m := range Curated() {
		aliases[m.Alias] = true
	}
	for _, m := range Curated() {
		if m.Encoder != "" && !aliases[m.Encoder] {
			t.Errorf("%s Encoder %q is not a catalog alias", m.Alias, m.Encoder)
		}
		if m.DSpark != "" && !aliases[m.DSpark] {
			t.Errorf("%s DSpark %q is not a catalog alias", m.Alias, m.DSpark)
		}
	}
}
