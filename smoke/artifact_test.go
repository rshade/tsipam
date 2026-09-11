//go:build smoke

package smoke_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// shortCommitLen is how many hex digits --version prints of the VCS revision.
const shortCommitLen = 12

// releaseTargets mirrors .goreleaser.yaml's goos/goarch matrix.
func releaseTargets() []string {
	return []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"}
}

// distDir returns the goreleaser output directory or skips the test.
func distDir(t *testing.T) string {
	t.Helper()
	configured := os.Getenv(envDist)
	if configured == "" {
		t.Skipf("set %s to a goreleaser dist directory to check release artifacts", envDist)
	}
	abs, err := filepath.Abs(configured)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

type releaseMetadata struct {
	Tag     string `json:"tag"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func readMetadata(t *testing.T, dist string) releaseMetadata {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dist, "metadata.json"))
	if err != nil {
		t.Fatalf("reading metadata.json: %v", err)
	}
	var meta releaseMetadata
	if err = json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("decoding metadata.json: %v", err)
	}
	return meta
}

func archiveName(version, target string) string {
	ext := ".tar.gz"
	if strings.HasPrefix(target, "windows_") {
		ext = ".zip"
	}
	return "tail-ipam_" + version + "_" + target + ext
}

func TestArtifacts(t *testing.T) {
	t.Parallel()
	dist := distDir(t)
	meta := readMetadata(t, dist)
	if meta.Version == "" || strings.Contains(meta.Version, "SNAPSHOT") || meta.Commit == "none" {
		t.Fatalf("metadata %+v is a snapshot; build with the release tag (see make smoke-release)", meta)
	}

	t.Run("checksums cover every archive and verify", func(t *testing.T) {
		t.Parallel()
		assertChecksums(t, dist, meta.Version)
	})
	t.Run("archives carry the binary and the notices", func(t *testing.T) {
		t.Parallel()
		assertArchiveMembers(t, dist, meta.Version)
	})
	t.Run("host binary reports the release version and commit", func(t *testing.T) {
		t.Parallel()
		assertHostBinaryVersion(t, dist, meta)
	})
}

func assertChecksums(t *testing.T, dist, version string) {
	t.Helper()
	sums := readChecksums(t, dist)
	for _, target := range releaseTargets() {
		name := archiveName(version, target)
		want, ok := sums[name]
		if !ok {
			t.Errorf("checksums.txt lacks %s", name)
			continue
		}
		if got := fileSHA256(t, filepath.Join(dist, name)); got != want {
			t.Errorf("%s: sha256 %s, checksums.txt says %s", name, got, want)
		}
	}
}

func assertArchiveMembers(t *testing.T, dist, version string) {
	t.Helper()
	for _, target := range releaseTargets() {
		name := archiveName(version, target)
		members := archiveMembers(t, filepath.Join(dist, name))
		binaryName := "tail-ipam"
		if strings.HasPrefix(target, "windows_") {
			binaryName += ".exe"
		}
		for _, want := range []string{"LICENSE", "NOTICE", "README.md", binaryName} {
			if !slices.Contains(members, want) {
				t.Errorf("%s lacks %s (has %v)", name, want, members)
			}
		}
	}
}

func assertHostBinaryVersion(t *testing.T, dist string, meta releaseMetadata) {
	t.Helper()
	target := runtime.GOOS + "_" + runtime.GOARCH
	if !slices.Contains(releaseTargets(), target) {
		t.Skipf("no release target for %s", target)
	}
	bin := extractBinary(t, filepath.Join(dist, archiveName(meta.Version, target)))
	out, err := exec.CommandContext(t.Context(), bin, "--version").Output()
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	version := strings.TrimSpace(string(out))
	shortCommit := meta.Commit
	if len(shortCommit) > shortCommitLen {
		shortCommit = shortCommit[:shortCommitLen]
	}
	containsAll(t, version, "tail-ipam "+meta.Version, shortCommit)
	containsNone(t, version, "dirty", "dev")
}

func readChecksums(t *testing.T, dist string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dist, "checksums.txt"))
	if err != nil {
		t.Fatalf("reading checksums.txt: %v", err)
	}
	sums := map[string]string{}
	for _, line := range lines(string(data)) {
		sum, name, ok := strings.Cut(line, "  ")
		if ok {
			sums[name] = sum
		}
	}
	return sums
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// archiveMembers lists the base names inside a tar.gz or zip archive.
func archiveMembers(t *testing.T, path string) []string {
	t.Helper()
	var names []string
	if strings.HasSuffix(path, ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			t.Fatalf("opening %s: %v", path, err)
		}
		defer zr.Close()
		for _, f := range zr.File {
			names = append(names, filepath.Base(f.Name))
		}
		return names
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip %s: %v", path, err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("reading %s: %v", path, nextErr)
		}
		names = append(names, filepath.Base(hdr.Name))
	}
	return names
}

// extractBinary pulls the tail-ipam executable out of a tar.gz archive into a
// temporary directory and returns its path.
func extractBinary(t *testing.T, path string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "tail-ipam")
	if err := extractTarMember(path, "tail-ipam", out); err != nil {
		t.Fatal(err)
	}
	return out
}

// extractHostBinary locates this platform's release archive under dist by the
// version in metadata.json and unpacks its binary to out. Windows hosts are
// not supported because the archive is a zip and the binary name differs.
func extractHostBinary(dist, out string) error {
	data, err := os.ReadFile(filepath.Join(dist, "metadata.json"))
	if err != nil {
		return err
	}
	var meta releaseMetadata
	if err = json.Unmarshal(data, &meta); err != nil {
		return err
	}
	target := runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" || !slices.Contains(releaseTargets(), target) {
		return fmt.Errorf("no extractable release archive for %s; set %s", target, envBinary)
	}
	return extractTarMember(filepath.Join(dist, archiveName(meta.Version, target)), "tail-ipam", out)
}

// extractTarMember copies the named entry of a tar.gz archive to an
// executable file at out.
func extractTarMember(archive, member, out string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			return fmt.Errorf("%s does not contain %s", archive, member)
		}
		if nextErr != nil {
			return nextErr
		}
		if filepath.Base(hdr.Name) != member {
			continue
		}
		w, openErr := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if openErr != nil {
			return openErr
		}
		if _, err = io.Copy(w, tr); err != nil {
			_ = w.Close()
			return err
		}
		return w.Close()
	}
}
