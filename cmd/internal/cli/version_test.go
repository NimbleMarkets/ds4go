package cli

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(mainVersion string, settings map[string]string) *debug.BuildInfo {
	info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/NimbleMarkets/ds4go/cmd", Version: mainVersion}}
	for k, v := range settings {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return info
}

// GoReleaser stamps the tag through ldflags; that always wins.
func TestVersionStringPrefersLdflags(t *testing.T) {
	got := versionString("v0.6.0", buildInfo("(devel)", map[string]string{"vcs.revision": "abcdef0123456789"}))
	if got != "v0.6.0" {
		t.Fatalf("versionString = %q, want v0.6.0", got)
	}
}

// `go install github.com/NimbleMarkets/ds4go/cmd/ds4go@latest` has no ldflags
// but a real module version.
func TestVersionStringFallsBackToModuleVersion(t *testing.T) {
	got := versionString("", buildInfo("v0.6.0", nil))
	if got != "v0.6.0" {
		t.Fatalf("versionString = %q, want the module version", got)
	}
	// A pseudo-version from a nested-module VCS stamp is still informative.
	pseudo := "v0.0.0-20260911015113-46d9bae0f02f+dirty"
	if got := versionString("", buildInfo(pseudo, nil)); got != pseudo {
		t.Fatalf("versionString = %q, want %q", got, pseudo)
	}
}

// A plain `go build` in a checkout reports (devel); show the commit instead.
func TestVersionStringUsesVCSRevisionForDevelBuilds(t *testing.T) {
	got := versionString("", buildInfo("(devel)", map[string]string{"vcs.revision": "46d9bae0f02fa7cdc70087a500e3595c10ce7e38", "vcs.modified": "true"}))
	if got != "devel (46d9bae, modified)" {
		t.Fatalf("versionString = %q, want devel with short revision and modified marker", got)
	}
	got = versionString("", buildInfo("(devel)", map[string]string{"vcs.revision": "46d9bae0f02fa7cdc70087a500e3595c10ce7e38", "vcs.modified": "false"}))
	if got != "devel (46d9bae)" {
		t.Fatalf("versionString = %q, want devel with short revision", got)
	}
	if got := versionString("", buildInfo("(devel)", nil)); got != "devel" {
		t.Fatalf("versionString = %q, want devel without VCS info", got)
	}
	if got := versionString("", nil); got != "devel" {
		t.Fatalf("versionString(nil build info) = %q, want devel", got)
	}
}

func TestRootCommandHasVersionFlag(t *testing.T) {
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.HasPrefix(out.String(), "ds4go version ") {
		t.Fatalf("--version printed %q, want a 'ds4go version ...' line", out.String())
	}
	if strings.TrimSpace(strings.TrimPrefix(out.String(), "ds4go version ")) == "" {
		t.Fatalf("--version printed an empty version: %q", out.String())
	}
}
