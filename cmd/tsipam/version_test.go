package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

const testRevision = "0123456789abcdef0123456789abcdef01234567"

func TestResolveVersion(t *testing.T) {
	t.Parallel()
	// resolve stands in for the runtime, which reads the running binary.
	fallback := func(found string) func(string) string {
		return func(injected string) string {
			if injected == devVersion || injected == "" {
				return found
			}
			return injected
		}
	}
	revision := []debug.BuildSetting{{Key: "vcs.revision", Value: testRevision}}
	dirty := append([]debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}, revision...)
	tests := []struct {
		name, injected, module, found string
		settings                      []debug.BuildSetting
		version, line                 string
	}{
		{
			name: "injected", injected: "1.2.3", settings: revision,
			version: "1.2.3", line: "tsipam 1.2.3 (0123456789ab)",
		},
		{name: "injected with v", injected: "v1.2.3", version: "1.2.3", line: "tsipam 1.2.3"},
		{name: "module", injected: devVersion, module: "v1.2.3", version: "1.2.3", line: "tsipam 1.2.3"},
		{
			name: "module at a dirty revision", injected: devVersion, module: "v1.2.4-0.20261125-0123456789ab+dirty",
			settings: dirty, version: "1.2.4-0.20261125-0123456789ab+dirty",
			line: "tsipam 1.2.4-0.20261125-0123456789ab+dirty (0123456789ab+dirty)",
		},
		{
			name: "revision only", injected: devVersion, module: "(devel)", found: testRevision, settings: revision,
			version: testRevision, line: "tsipam " + testRevision,
		},
		{
			name: "dirty revision only", injected: devVersion, found: testRevision + "-dirty", settings: dirty,
			version: testRevision + "-dirty", line: "tsipam " + testRevision + "-dirty",
		},
		{
			name: "unstamped", injected: devVersion, found: "0.0.0-unknown",
			version: "0.0.0-unknown", line: "tsipam 0.0.0-unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			info := &debug.BuildInfo{Settings: test.settings}
			info.Main.Version = test.module
			got := resolveVersion(test.injected, info, fallback(test.found))
			if got != test.version {
				t.Errorf("resolveVersion = %q, want %q", got, test.version)
			}
			if line := buildVersionWithSettings(got, test.settings); line != test.line {
				t.Errorf("--version line = %q, want %q", line, test.line)
			}
		})
	}
}

func TestEverySurfaceReportsOneVersion(t *testing.T) {
	t.Parallel()
	want := resolvedVersion()
	if want == "" || want == devVersion || strings.HasPrefix(want, "v") {
		t.Fatalf("resolved version = %q", want)
	}
	_, stdout, _ := runWith(t, nil, "--version")
	if !strings.HasPrefix(stdout, toolName+" "+want) {
		t.Errorf("--version = %q, want %q", stdout, want)
	}
	_, stdout, _ = runWith(t, nil, "__schema")
	if !strings.Contains(stdout, `"version":"`+want+`"`) {
		t.Errorf("__schema does not report version %q", want)
	}
	_, _, stderr := runWith(t, nil, "bogus")
	if failure := decodeFailure(t, stderr); failure.Version != want {
		t.Errorf("error object version = %q, want %q", failure.Version, want)
	}
}
