package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goa "goa.design/goa/v3/pkg"
)

// runSiteID drives the site-id command tree through cobra's Execute — the
// same entry point realMain uses — with separate stdout/stderr buffers, so a
// test can assert AX convention 1 (results on stdout, nothing unexpected on
// stderr) instead of a merged stream that can't distinguish the two. Every
// call goes through the real loopback goa HTTP server/client over a Unix
// socket (see internal/loopback), the same path a live invocation takes.
func runSiteID(t *testing.T, registryDir string, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCommand()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"site-id"}, append(args, "--registry-dir", registryDir)...))
	err := root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestSiteIDReserveThenListHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	out, errOut, err := runSiteID(t, dir, "reserve", "1", "hq-vpc")
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !strings.Contains(out, "reserved site ID 1") {
		t.Errorf("reserve stdout = %q, want it to confirm the reservation", out)
	}
	if errOut != "" {
		t.Errorf("reserve stderr = %q, want empty on success", errOut)
	}

	out, errOut, err = runSiteID(t, dir, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "1\thq-vpc") {
		t.Errorf("list stdout = %q, want it to contain %q", out, "1\thq-vpc")
	}
	if errOut != "" {
		t.Errorf("list stderr = %q, want empty on success", errOut)
	}
}

// TestSiteIDReserveCommandPreservesHandWrittenComment mirrors
// registry_test.go's TestReserveSiteIDPreservesHandWrittenComment, but drives
// the edit through the cobra command layer — and through the real loopback
// HTTP server/client, not a direct in-process call — so the
// comment-preservation guarantee is verified at the layer a user (or
// ROADMAP.md's "verified end to end" claim) actually relies on.
func TestSiteIDReserveCommandPreservesHandWrittenComment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "local.ipam.hujson")
	content := "{\n\t// reserved for the HQ migration, do not release before Q1\n\t\"siteIDs\": {\n\t\t\"1\": \"hq-vpc\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	if _, _, err := runSiteID(t, dir, "reserve", "2", "branch-office"); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	want := "// reserved for the HQ migration, do not release before Q1"
	if !strings.Contains(string(got), want) {
		t.Errorf("registry after reserving a second entry through the command layer = %q, want it to still contain %q",
			got, want)
	}
}

// TestSiteIDExitCodeDistinguishesCollisionFromUsageError covers issue 8's
// fix: a collision reported over the loopback HTTP boundary (a
// *goa.ServiceError named "collision") must map to exitFailure (1), while a
// bad flag/usage error (here, a missing positional argument) must map to
// exitUsage (2), so a shell caller can tell a refusal apart from a malformed
// invocation.
func TestSiteIDExitCodeDistinguishesCollisionFromUsageError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := runSiteID(t, dir, "reserve", "1", "hq-vpc"); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	_, _, collisionErr := runSiteID(t, dir, "reserve", "1", "branch-office")
	var svcErr *goa.ServiceError
	if !errors.As(collisionErr, &svcErr) || svcErr.Name != collisionErrorName {
		t.Fatalf("reserving an id already held by a different site: error = %v, want a %q *goa.ServiceError",
			collisionErr, collisionErrorName)
	}
	if got := exitCodeFor(collisionErr); got != exitFailure {
		t.Errorf("exitCodeFor(collision) = %d, want %d", got, exitFailure)
	}

	_, _, usageErr := runSiteID(t, dir, "reserve", "1")
	if usageErr == nil {
		t.Fatal("reserve with one arg: error = nil, want a usage error")
	}
	if errors.As(usageErr, &svcErr) {
		t.Fatalf("a usage error should not be a *goa.ServiceError, got %v", usageErr)
	}
	if got := exitCodeFor(usageErr); got != exitUsage {
		t.Errorf("exitCodeFor(usage error) = %d, want %d", got, exitUsage)
	}
}
