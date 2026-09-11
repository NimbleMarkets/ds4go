package cli

import (
	"runtime/debug"
	"strings"
)

// version is stamped by GoReleaser through ldflags
// (-X github.com/NimbleMarkets/ds4go/cmd/internal/cli.version=v0.6.0).
// It stays empty for `go install` and plain `go build`, which fall back to
// the build's module version or VCS revision.
var version string

// Version reports the CLI version shown by --version.
func Version() string {
	info, _ := debug.ReadBuildInfo()
	return versionString(version, info)
}

// versionString resolves the version from, in order: the ldflags stamp, the
// main module's version when it is a real one (a tagged `go install` or the
// pseudo-version Go derives from VCS state), and otherwise "devel" with the
// short VCS revision and a modified marker when the build recorded them.
func versionString(stamped string, info *debug.BuildInfo) string {
	if stamped != "" {
		return stamped
	}
	if info == nil {
		return "devel"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	revision, modified := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return "devel"
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	var b strings.Builder
	b.WriteString("devel (")
	b.WriteString(revision)
	if modified {
		b.WriteString(", modified")
	}
	b.WriteString(")")
	return b.String()
}
