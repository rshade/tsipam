package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// releaseTags reads the build tags of the release archives.
func releaseTags(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?m)^    tags:\n((?:      - \S+\n)+)`).FindSubmatch(raw)
	if block == nil {
		t.Fatal(".goreleaser.yaml declares no build tags")
	}
	return strings.ReplaceAll(strings.TrimSpace(strings.ReplaceAll(string(block[1]), "- ", "")), "\n      ", ",")
}

func linkedPackages(t *testing.T, tags string) string {
	t.Helper()
	list := exec.CommandContext(t.Context(), "go", "list", "-deps", "-tags", tags, "./cmd/tsipam")
	list.Dir = moduleRoot
	out, err := list.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	return string(out)
}

// TestReleaseBuildLinksNoExporter keeps code that could send telemetry, and the
// gRPC stack it pulls in, out of the archives.
func TestReleaseBuildLinksNoExporter(t *testing.T) {
	t.Parallel()
	tags := releaseTags(t)
	if tags != "ax_no_grpc,ax_no_otlp" {
		t.Errorf("release tags = %q", tags)
	}
	unwanted := []string{
		"google.golang.org/grpc", "go.opentelemetry.io/otel/exporters/otlp", "go.opentelemetry.io/proto/otlp",
		"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc",
	}
	release, untagged := linkedPackages(t, tags), linkedPackages(t, "")
	for _, prefix := range unwanted {
		if strings.Contains(release, "\n"+prefix) {
			t.Errorf("a release build links %s", prefix)
		}
		if !strings.Contains(untagged, "\n"+prefix) {
			t.Errorf("no build links %s, so this check proves nothing", prefix)
		}
	}
}

func TestNoticeUsesTheReleaseTags(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(moduleRoot, "scripts", "notice.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "\ntags=" + releaseTags(t) + "\n"; !strings.Contains(string(raw), want) {
		t.Errorf("scripts/notice.sh does not set %q", strings.TrimSpace(want))
	}
}
