package cli

import (
	"runtime/debug"
	"strings"
)

// version is the release version, injected at link time from the git tag:
//
//	go build -ldflags "-X github.com/AaronSaikovski/entra-appreg/internal/cli.version=v0.0.1" ./cmd/entra-appreg
//
// GoReleaser and the Taskfile set it; source code never hard-codes a version.
var version string

// Version reports the version for --version and the general help heading,
// without a leading "v". It prefers the link-time tag, then the module version
// Go stamps into the binary (a tag for `go install ...@v0.0.1`, or a VCS-derived
// pseudo-version for a plain `go build` in a git checkout), and finally "dev".
func Version() string {
	return resolveVersion(version, debug.ReadBuildInfo)
}

func resolveVersion(linked string, buildInfo func() (*debug.BuildInfo, bool)) string {
	if linked != "" {
		return strings.TrimPrefix(linked, "v")
	}
	if info, ok := buildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}
