//go:build smoke

package smoke_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// moduleDir is the tail-ipam module relative to this package, the working
// directory `go test` gives the suite.
const moduleDir = ".."

type releaseMetadata struct {
	Tag     string `json:"tag"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// releaseMetadataFrom describes the release under dist. goreleaser's
// metadata.json is authoritative; a GitHub Release download lacks it, so the
// version then comes from the archive names and the commit from the module's
// release tag in repo. Commit stays empty when that tag is not in repo, which
// is the case on a checkout that never fetched tags.
func releaseMetadataFrom(dist, repo string) (releaseMetadata, error) {
	meta, err := readMetadataFile(filepath.Join(dist, "metadata.json"))
	if !errors.Is(err, os.ErrNotExist) {
		return meta, err
	}
	version, err := archiveVersion(dist)
	if err != nil {
		return releaseMetadata{}, err
	}
	return releaseMetadata{
		Tag:     "v" + version,
		Version: version,
		Commit:  tagCommit(repo, releaseTag(version)),
	}, nil
}

// releaseTag is the monorepo tag release-please cuts for a tail-ipam version.
func releaseTag(version string) string { return "tail-ipam/v" + version }

func readMetadataFile(path string) (releaseMetadata, error) {
	var meta releaseMetadata
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, err
	}
	if err = json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("decoding %s: %w", path, err)
	}
	return meta, nil
}

// archiveVersion returns the one version the release archives under dist are
// named for.
func archiveVersion(dist string) (string, error) {
	entries, err := os.ReadDir(dist)
	if err != nil {
		return "", err
	}
	versions := map[string]bool{}
	for _, entry := range entries {
		if version, ok := archiveVersionOf(entry.Name()); ok {
			versions[version] = true
		}
	}
	found := slices.Sorted(maps.Keys(versions))
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%s has no metadata.json and no tail-ipam release archives", dist)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%s has no metadata.json and archives for several versions: %v", dist, found)
	}
}

// archiveVersionOf parses <version> out of tail-ipam_<version>_<target><ext>.
func archiveVersionOf(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "tail-ipam_")
	if !ok {
		return "", false
	}
	for _, target := range releaseTargets() {
		if version, found := strings.CutSuffix(rest, "_"+target+archiveExt(target)); found {
			return version, true
		}
	}
	return "", false
}

// tagCommit resolves tag to its commit in repo, or to "" when it cannot.
func tagCommit(repo, tag string) string {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", tag+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TestReleaseMetadataFromPrefersMetadataJSON(t *testing.T) {
	t.Parallel()
	dist := fakeDist(t, releaseArchives("0.1.0")...)
	writeFile(t, filepath.Join(dist, "metadata.json"), `{"tag":"v9.9.9","version":"9.9.9","commit":"abc123"}`)

	meta, err := releaseMetadataFrom(dist, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := releaseMetadata{Tag: "v9.9.9", Version: "9.9.9", Commit: "abc123"}
	if meta != want {
		t.Fatalf("metadata = %+v, want %+v", meta, want)
	}
}

func TestReleaseMetadataFromDerivesVersionAndCommit(t *testing.T) {
	t.Parallel()
	dist := fakeDist(t, append(releaseArchives("1.2.3"), "checksums.txt")...)
	repo, commit := taggedRepo(t, "tail-ipam/v1.2.3")

	meta, err := releaseMetadataFrom(dist, repo)
	if err != nil {
		t.Fatal(err)
	}
	want := releaseMetadata{Tag: "v1.2.3", Version: "1.2.3", Commit: commit}
	if meta != want {
		t.Fatalf("metadata = %+v, want %+v", meta, want)
	}
}

func TestReleaseMetadataFromLeavesCommitEmptyWithoutTag(t *testing.T) {
	t.Parallel()
	dist := fakeDist(t, releaseArchives("1.2.3")...)

	meta, err := releaseMetadataFrom(dist, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := releaseMetadata{Tag: "v1.2.3", Version: "1.2.3"}
	if meta != want {
		t.Fatalf("metadata = %+v, want %+v", meta, want)
	}
}

func TestReleaseMetadataFromRejectsMixedVersions(t *testing.T) {
	t.Parallel()
	dist := fakeDist(t, append(releaseArchives("1.2.3"), archiveName("1.2.4", "linux_amd64"))...)

	_, err := releaseMetadataFrom(dist, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for archives of two versions")
	}
	containsAll(t, err.Error(), "1.2.3", "1.2.4")
}

func TestReleaseMetadataFromRequiresArchives(t *testing.T) {
	t.Parallel()
	dist := fakeDist(t, "checksums.txt")

	_, err := releaseMetadataFrom(dist, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a dist without archives")
	}
	containsAll(t, err.Error(), "metadata.json", "no tail-ipam release archives")
}

// releaseArchives names the archive goreleaser writes for every target.
func releaseArchives(version string) []string {
	var names []string
	for _, target := range releaseTargets() {
		names = append(names, archiveName(version, target))
	}
	return names
}

// fakeDist creates a directory holding empty files with the given names.
func fakeDist(t *testing.T, names ...string) string {
	t.Helper()
	dist := t.TempDir()
	for _, name := range names {
		writeFile(t, filepath.Join(dist, name), "")
	}
	return dist
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// taggedRepo creates a repository whose single commit carries tag and
// returns the repository path and that commit's hash.
func taggedRepo(t *testing.T, tag string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{
			"-C", dir,
			"-c", "user.name=smoke", "-c", "user.email=smoke@example.com",
			"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
		}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "release")
	git("tag", tag)
	return dir, git("rev-parse", "HEAD")
}
