package main

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/rshade/ax-go"
)

// devVersion is the release string of a build that injects no version.
const devVersion = "dev"

const shortRevisionLength = 12

// version is overridden at build time via -ldflags "-X main.version=X.Y.Z";
// goreleaser injects the tag without its leading v.
var version = devVersion

// resolvedVersion is the version every surface reports: --version, __schema,
// the error object, and the MCP handshake. It is never empty or "dev".
func resolvedVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		info = &debug.BuildInfo{}
	}
	return resolveVersion(version, info, ax.ResolveVersion)
}

// resolveVersion prefers the injected release string, then the module version
// of a `go install`, then whatever resolve finds in the running binary. The
// leading v is dropped so every install path prints the same string.
func resolveVersion(injected string, info *debug.BuildInfo, resolve func(string) string) string {
	return strings.TrimPrefix(resolve(moduleVersion(injected, info.Main.Version)), "v")
}

// buildVersion is the --version line: the resolved version and the embedded VCS
// revision, so it reports exact provenance for an auditable binary.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return buildVersionWithSettings(resolvedVersion(), nil)
	}
	return buildVersionWithSettings(resolvedVersion(), info.Settings)
}

// moduleVersion prefers the module version Go recorded for `go install
// module@version` over the dev placeholder, without its leading v so both
// install paths print the same release string. A working-tree build records
// "(devel)" and keeps dev. An injected release string always wins.
func moduleVersion(injected, module string) string {
	if injected != devVersion || module == "" || module == "(devel)" {
		return injected
	}
	return strings.TrimPrefix(module, "v")
}

func buildVersionWithSettings(value string, settings []debug.BuildSetting) string {
	var revision, dirty string
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			if setting.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	// A build with nothing but its revision already reports it as the version.
	if revision != "" && !strings.HasPrefix(value, revision) {
		value = fmt.Sprintf("%s (%s%s)", value, revision[:min(len(revision), shortRevisionLength)], dirty)
	}
	return toolName + " " + value
}
