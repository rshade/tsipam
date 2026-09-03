package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	state := &commandState{}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{commandSiteID}, append(args, "--registry-dir", registryDir)...))
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

func TestSiteIDReserveWithMetadataFlagsThenList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	out, errOut, err := runSiteID(
		t, dir, "reserve", "1", "hq-vpc",
		"--owner", "alice", "--ticket", "OPS-1234", "--description", "HQ gateway",
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if errOut != "" {
		t.Errorf("reserve stderr = %q, want empty on success", errOut)
	}
	if !strings.Contains(out, "reserved site ID 1") {
		t.Errorf("reserve stdout = %q, want it to confirm the reservation", out)
	}

	out, _, err = runSiteID(t, dir, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"1\thq-vpc", "owner=alice", "ticket=OPS-1234", "description=HQ gateway"} {
		if !strings.Contains(out, want) {
			t.Errorf("list stdout = %q, want it to contain %q", out, want)
		}
	}

	out, _, err = runSiteID(t, dir, "list", "--format", formatJSON)
	if err != nil {
		t.Fatalf("list --format json: %v", err)
	}
	var listed outputEnvelope
	if err = json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode list JSON: %v", err)
	}
	if listed.SiteIDs == nil || len(*listed.SiteIDs) != 1 {
		t.Fatalf("list JSON = %+v, want one entry", listed)
	}
	got := (*listed.SiteIDs)[0]
	if got.Owner != "alice" || got.Ticket != "OPS-1234" || got.Description != "HQ gateway" || got.ReservedAt == "" {
		t.Errorf("list JSON entry = %+v, want Owner=alice Ticket=OPS-1234 Description=%q and a non-empty ReservedAt",
			got, "HQ gateway")
	}
}

func TestSiteIDReserveWithoutMetadataOmitsItFromJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	out, _, err := runSiteID(t, dir, "reserve", "1", "hq-vpc", "--format", formatJSON)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if strings.Contains(out, `"owner"`) || strings.Contains(out, `"reserved_at"`) {
		t.Errorf("reserve --format json without metadata flags = %s, want no metadata fields present", out)
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

// TestSiteIDReserveArgsRegisterForErrorSweep covers round-2 item 1: round 1
// never registered "site-id reserve"'s own <id>/<site-name> CLI args at all
// (only the --owner metadata flag, see siteid.go), so a padded/invalid ID or
// a control-character site name reached stderr with the raw or %q-escaped
// value intact.
func TestSiteIDReserveArgsRegisterForErrorSweep(t *testing.T) {
	t.Parallel()

	// A non-numeric ID (as opposed to a short valid numeric one, which
	// minSweepValueLength already suppresses from the sweep) is echoed by
	// registry.NormalizeSiteID's "is not a non-negative integer" error using
	// the *trimmed* spelling, since NormalizeSiteID trims before formatting
	// that message.
	t.Run("padded_non_numeric_id_leaks_trimmed_spelling", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		const trimmedID = "abc123secret"

		var stdout, stderr bytes.Buffer
		code := runCLI(context.Background(), []string{
			commandSiteID, "reserve", " " + trimmedID + " ", "sitename",
			"--registry-dir", dir, "--sanitize", "identity",
		}, fixedKeyGetenv, &stdout, &stderr, nil)
		if code == exitOK {
			t.Fatalf("invalid site ID exit = %d, want non-zero", code)
		}
		stderrText := stderr.String()
		if strings.Contains(stderrText, trimmedID) {
			t.Errorf("stderr leaks trimmed site ID %q: %s", trimmedID, stderrText)
		}
		placeholder := placeholderFor(t, kindSiteID, trimmedID)
		if !strings.Contains(stderrText, placeholder) {
			t.Errorf("stderr = %q, want trimmed-ID placeholder %q", stderrText, placeholder)
		}
		if !strings.HasPrefix(placeholder, "site-id-") {
			t.Errorf("placeholder = %q, want the %q prefix, not the site-name namespace", placeholder, "site-id-")
		}
	})

	// A control character in the site name is rejected by
	// registry.NormalizeSiteName's rejectControlChars, whose error renders
	// the (trimmed) name with %q -- escaping the control character rather
	// than reproducing it byte-for-byte, so only the quoted-form mechanism
	// (registerQuotedForm, main.go) can close this leak.
	t.Run("control_char_site_name_leaks_escaped_rendering", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		const site = "sec\tret-site"
		const siteEscaped = `sec\tret-site`

		var stdout, stderr bytes.Buffer
		code := runCLI(context.Background(), []string{
			commandSiteID, "reserve", "1", site,
			"--registry-dir", dir, "--sanitize", "identity",
		}, fixedKeyGetenv, &stdout, &stderr, nil)
		if code == exitOK {
			t.Fatalf("control-character site name exit = %d, want non-zero", code)
		}
		stderrText := stderr.String()
		if strings.Contains(stderrText, siteEscaped) {
			t.Errorf("stderr leaks the %%q-escaped site name %q: %s", siteEscaped, stderrText)
		}
		placeholder := placeholderFor(t, kindSite, siteEscaped)
		if !strings.Contains(stderrText, placeholder) {
			t.Errorf("stderr = %q, want escaped-site-name placeholder %q", stderrText, placeholder)
		}
	})
}

// TestSiteIDReleaseArgRegistersForErrorSweep is the release-side counterpart
// of TestSiteIDReserveArgsRegisterForErrorSweep: "site-id release" takes the
// same <id> positional argument as "reserve" and fails through the same
// NormalizeSiteID validation, but (before this fix) never registered it for
// the error sanitizer's free-text sweep, so a malformed ID leaked verbatim on
// release while reserve already redacted it. A raw TAB byte (as opposed to
// mere padding) is used so the sweep is actually exercised rather than
// vacuously passing: NormalizeSiteID's error %q-escapes the id it embeds, so
// (like the control_char_site_name subtest above) the placeholder that
// appears is for the escaped rendering, not the raw bytes.
func TestSiteIDReleaseArgRegistersForErrorSweep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const rawID = "se\tcret-id"
	const escapedID = `se\tcret-id`

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSiteID, "release", rawID,
		"--registry-dir", dir, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		t.Fatalf("invalid site ID exit = %d, want non-zero", code)
	}
	stderrText := stderr.String()
	if strings.Contains(stderrText, escapedID) {
		t.Errorf("stderr leaks the %%q-escaped site ID %q: %s", escapedID, stderrText)
	}
	placeholder := placeholderFor(t, kindSiteID, escapedID)
	if !strings.Contains(stderrText, placeholder) {
		t.Errorf("stderr = %q, want site-ID placeholder %q", stderrText, placeholder)
	}
	if !strings.HasPrefix(placeholder, "site-id-") {
		t.Errorf("placeholder = %q, want the %q prefix, not the site-name namespace", placeholder, "site-id-")
	}
}
