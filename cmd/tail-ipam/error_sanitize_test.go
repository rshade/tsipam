package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rshade/tailscale-utils/policycore/sanitize"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

// testSanitizeKey is a fixed TAILSCALE_SANITIZE_KEY so a placeholder computed
// independently in a test (see placeholderFor) matches the one the CLI itself
// produces, and so identity and strict runs are directly comparable (see
// TestSanitizeStrictMatchesIdentityForErrorText).
const testSanitizeKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func fixedKeyGetenv(key string) string {
	if key == sanitize.KeyEnv {
		return testSanitizeKey
	}
	return ""
}

// placeholderFor computes the placeholder the CLI's own sanitizer produces
// for a "site" value, using the same tool name, tailnet, and key a
// default-tailnet ("local") invocation resolves. Sanitizer.Value is a pure
// function of (tool, scope, tailnet, kind, value), so this reference
// sanitizer's output equals production's without needing to inspect the
// CLI's internal state.
func placeholderFor(t *testing.T, value string) string {
	t.Helper()
	s, err := sanitize.New(sanitize.Options{
		Level: sanitize.Identity, Tool: toolName, Tailnet: localTailnet, KeyHex: testSanitizeKey,
	})
	if err != nil {
		t.Fatalf("constructing reference sanitizer: %v", err)
	}
	return s.Value("site", value)
}

// assertNoLeak fails the test if haystack contains value verbatim, or
// contains the interior bytes of value's %q rendering. The second check is
// the regression guard for Round 2's exact failure mode: it searched for the
// *unescaped* value in the *escaped* error text and never found it, silently
// leaving the escaped bytes on the page untouched.
func assertNoLeak(t *testing.T, haystack, value string) {
	t.Helper()
	if value == "" {
		return
	}
	if strings.Contains(haystack, value) {
		t.Errorf("output leaks raw value %q: %s", value, haystack)
	}
	escaped := fmt.Sprintf("%q", value)
	inner := escaped[1 : len(escaped)-1]
	if inner != "" && strings.Contains(haystack, inner) {
		t.Errorf("output leaks the %%q-escaped rendering of %q (escaped form %s): %s", value, escaped, haystack)
	}
}

// assertNoQuotedLeak fails the test if haystack contains value's %q
// rendering, quotes included. Used instead of assertNoLeak when value is
// also one of the collision message's own immutable template words ("for",
// "not"): the bare word legitimately remains in the message frame, so only
// the quoted collision segment itself is a leak.
func assertNoQuotedLeak(t *testing.T, haystack, value string) {
	t.Helper()
	if value == "" {
		return
	}
	quoted := fmt.Sprintf("%q", value)
	if strings.Contains(haystack, quoted) {
		t.Errorf("output leaks the quoted collision segment %s: %s", quoted, haystack)
	}
}

// seedCollision reserves id 1 for existing in a fresh registry directory
// under dir, matching the "<tailnet>.ipam.hujson" path the default
// ("local") tailnet resolves to.
func seedCollision(t *testing.T, dir, existing string) {
	t.Helper()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", existing, nil); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
}

// TestCollisionSiteNameSanitization is the table-driven regression suite for
// the review's complete input-domain characterization: every one of these
// site-name shapes must be redacted from a collision error's stderr text
// without corrupting the surrounding message frame. The positional splice in
// sanitizeErrorIfNeeded (main.go) never searches for a value's bytes in the
// error text -- it replaces a byte range the anchored regex already located
// -- which is what makes every one of these cases safe regardless of
// escaping.
func TestCollisionSiteNameSanitization(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// existing/wanted collide with immutable template words ("is
		// already reserved for" / ", not ") in the "common english words"
		// case below: the bare words legitimately remain in the message
		// frame, so only their *quoted* rendering (the actual collision
		// segment) must disappear -- checked with assertNoQuotedLeak
		// instead of assertNoLeak's stronger bare-substring check.
		existing, wanted    string
		collidesWithFraming bool
	}{
		{name: "plain names", existing: "existing-plain", wanted: "wanted-plain"},
		{name: "existing has an embedded quote", existing: `secret"quote-existing`, wanted: "plainwanted"},
		{name: "wanted has an embedded quote", existing: "plainexisting", wanted: `secret"quote-wanted`},
		{name: "existing has an embedded backslash", existing: `secret\back-existing`, wanted: "plainwanted"},
		{name: "existing has a quote and a backslash", existing: `se\c"ret-existing`, wanted: "plainwanted"},
		{name: "existing ends in a backslash", existing: `existing\`, wanted: "plainwanted"},
		{
			name:     "wanted is a substring of the escaping-needy existing name",
			existing: `secret"quote-existing`, wanted: "existing",
		},
		{name: "case-differing names", existing: "Alpha", wanted: "alpha"},
		{name: "common english words as site names", existing: "for", wanted: "not", collidesWithFraming: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			seedCollision(t, dir, tc.existing)

			var stdout, stderr bytes.Buffer
			code := runCLI(context.Background(),
				[]string{"site-id", "reserve", "1", tc.wanted, "--registry-dir", dir, "--sanitize", "identity"},
				fixedKeyGetenv, &stdout, &stderr, nil,
			)
			if code != exitFailure {
				t.Fatalf("exit code = %d, want %d (collision); stderr: %s", code, exitFailure, stderr.String())
			}
			assertCollisionSanitized(t, stderr.String(), tc.existing, tc.wanted, tc.collidesWithFraming)
		})
	}
}

// assertCollisionSanitized checks a sanitized collision message against the
// shared expectations every TestCollisionSiteNameSanitization case must
// satisfy: neither site name (nor, for a name colliding with the message's
// own template words, its quoted rendering) leaks, the surrounding message
// frame survives, both sites are replaced by their expected quoted
// placeholder, and distinct site names get distinct placeholders.
func assertCollisionSanitized(t *testing.T, stderrText, existing, wanted string, collidesWithFraming bool) {
	t.Helper()

	if collidesWithFraming {
		assertNoQuotedLeak(t, stderrText, existing)
		assertNoQuotedLeak(t, stderrText, wanted)
	} else {
		assertNoLeak(t, stderrText, existing)
		assertNoLeak(t, stderrText, wanted)
	}

	if !strings.Contains(stderrText, "is already reserved for") || !strings.Contains(stderrText, ", not ") {
		t.Errorf("stderr = %q, want the collision message frame intact", stderrText)
	}

	existingQuoted := `"` + placeholderFor(t, existing) + `"`
	if !strings.Contains(stderrText, existingQuoted) {
		t.Errorf("stderr = %q, want the quoted existing-site placeholder %q", stderrText, existingQuoted)
	}
	wantedQuoted := `"` + placeholderFor(t, wanted) + `"`
	if !strings.Contains(stderrText, wantedQuoted) {
		t.Errorf("stderr = %q, want the quoted wanted-site placeholder %q", stderrText, wantedQuoted)
	}
	if existing != wanted && existingQuoted == wantedQuoted {
		t.Errorf("existing and wanted site names got the same placeholder %q, want distinct placeholders",
			existingQuoted)
	}
}

// TestCollisionErrorFormatMatchesAnchoredSanitizerPattern is the format-drift
// guard for Step 3: collisionErrorPattern (main.go) is hand-anchored to
// SiteIDCollisionError.Error()'s exact format string (registry.go). If that
// format string ever changes, this test fails loudly instead of the
// sanitizer silently no longer matching a real collision.
func TestCollisionErrorFormatMatchesAnchoredSanitizerPattern(t *testing.T) {
	t.Parallel()

	err := &registry.SiteIDCollisionError{ID: "42", Existing: "alpha", Wanted: "beta"}
	text := err.Error()

	match := collisionErrorPattern.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("collisionErrorPattern does not match SiteIDCollisionError.Error() = %q; "+
			"registry.go's format string and main.go's anchored pattern have drifted apart", text)
	}
	if match[1] != "42" {
		t.Errorf("captured ID = %q, want %q", match[1], "42")
	}
	if match[2] != `"alpha"` {
		t.Errorf("captured existing-site segment = %q, want %q", match[2], `"alpha"`)
	}
	if match[3] != `"beta"` {
		t.Errorf("captured wanted-site segment = %q, want %q", match[3], `"beta"`)
	}
}

// TestCollisionErrorDoesNotSurviveTheLoopbackTransport is the dead-branch
// guard for Step 4: it proves, through the real CLI (not a hand-constructed
// error), that a *registry.SiteIDCollisionError never reaches runCLI's error
// handling as that concrete type -- it crosses the loopback goa HTTP
// transport and is reconstructed client-side as a *goa.ServiceError with no
// path back to the original. A future change must not re-add a
// [errors.As](err, &registry.SiteIDCollisionError) branch to
// sanitizeErrorIfNeeded on the assumption that it fires; this test fails if
// that assumption is ever true again.
func TestCollisionErrorDoesNotSurviveTheLoopbackTransport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := runSiteID(t, dir, "reserve", "1", "hq-vpc"); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}
	_, _, err := runSiteID(t, dir, "reserve", "1", "branch-office")
	if err == nil {
		t.Fatal("reserving a colliding ID: want an error, got nil")
	}

	var collision *registry.SiteIDCollisionError
	if errors.As(err, &collision) {
		t.Fatalf("errors.As(err, &registry.SiteIDCollisionError) = true for %v (%T); "+
			"the concrete registry error survived the loopback transport, so the deleted "+
			"errors.As branch in sanitizeErrorIfNeeded was live, not dead", err, err)
	}
}

// TestErrorSanitizationOmitsRegistryPathOnMissingDirectory covers a registry
// path leak reached through a plain "file not found" (registry.go's Load,
// via identityFreePathError) combined with Step 1's pre-registered path
// inventory (registerKnownIdentityInventory), which also sweeps the leak
// closed for callers deeper in policycore/hujsonedit that this tool does not
// control the wording of.
func TestErrorSanitizationOmitsRegistryPathOnMissingDirectory(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	missingDir := filepath.Join(base, "nonexistent")

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(),
		[]string{"site-id", "list", "--registry-dir", missingDir, "--sanitize", "identity"},
		fixedKeyGetenv, &stdout, &stderr, nil,
	)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitUsage, stderr.String())
	}
	stderrText := stderr.String()
	if strings.Contains(stderrText, missingDir) || strings.Contains(stderrText, base) {
		t.Errorf("stderr leaks the registry path: %s", stderrText)
	}
	if stderrText == "" {
		t.Error("want a non-empty error message")
	}
}

// TestNonIdentityQuotedTextPassesThroughUnchanged is the D3 over-redaction
// guard: a registry decode error naming its own near-miss top-level key
// spelling is quoted text that is not identity, so it must survive
// sanitizeErrorIfNeeded's free-text sweep byte-for-byte (Step 5) -- proving
// the fix does not fall back to Round 1/2's generic "redact any quoted
// segment" behavior outside the one anchored collision shape.
func TestNonIdentityQuotedTextPassesThroughUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	if err := os.WriteFile(path, []byte(`{"siteids": {"1": "hq"}}`), 0o600); err != nil {
		t.Fatalf("seed malformed registry: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(),
		[]string{"site-id", "list", "--registry-dir", dir, "--sanitize", "identity"},
		fixedKeyGetenv, &stdout, &stderr, nil,
	)
	if code == exitOK {
		t.Fatalf("want an error for a near-miss top-level key, got success; stdout: %s", stdout.String())
	}
	stderrText := stderr.String()
	// The error crosses the loopback transport as an undeclared ("fault")
	// goa error, so it arrives JSON-embedded with backslash-escaped quotes
	// rather than as a bare Go %q string; check for the key spellings
	// themselves rather than assuming a particular quote style.
	if !strings.Contains(stderrText, "siteids") || !strings.Contains(stderrText, "siteIDs") {
		t.Errorf("stderr = %q, want the unrelated key spellings to pass through unchanged (resolved decision D3)",
			stderrText)
	}
}

// TestSanitizeOffLeavesErrorTextUntouched covers the "sanitize off" row: with
// no sanitization requested, output must be byte-identical to what it would
// be with no sanitization code involved at all, even for a site name shaped
// to defeat a naive quote-scanner.
func TestSanitizeOffLeavesErrorTextUntouched(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const existing = `secret"quote-existing`
	seedCollision(t, dir, existing)

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(),
		[]string{"site-id", "reserve", "1", "wanted-plain", "--registry-dir", dir},
		fixedKeyGetenv, &stdout, &stderr, nil,
	)
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitFailure, stderr.String())
	}
	stderrText := stderr.String()
	// SiteIDCollisionError.Error() always %q-formats its two site names
	// (registry.go), sanitized or not: with sanitization off, that %q
	// rendering must appear completely untouched -- the same bytes a build
	// with no sanitization code at all would have produced.
	if !strings.Contains(stderrText, fmt.Sprintf("%q", existing)) {
		t.Errorf("stderr = %q, want the untouched %%q-rendered existing site name present when sanitization is off",
			stderrText)
	}
	if !strings.Contains(stderrText, `"wanted-plain"`) {
		t.Errorf("stderr = %q, want the untouched wanted site name present when sanitization is off", stderrText)
	}
}

// lastLine returns the final non-empty line of text, isolating the actual
// sanitized error line runCLI prints from the sanitize-level notice line
// PersistentPreRunE prints above it (which legitimately differs between
// identity and strict, since it names the level).
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return lines[len(lines)-1]
}

// TestSanitizeStrictMatchesIdentityForErrorText is the explicit regression
// guard for resolved decision D7: nothing in a collision error's text is
// timestamp-like, so strict has nothing additional to quantize and must
// produce identical error-text sanitization to identity.
func TestSanitizeStrictMatchesIdentityForErrorText(t *testing.T) {
	t.Parallel()
	const existing = `secret"quote-existing`
	const wanted = `wanted\backslash`

	identityStderr := runCollisionScenario(t, sanitize.Identity, existing, wanted)
	strictStderr := runCollisionScenario(t, sanitize.Strict, existing, wanted)

	if lastLine(identityStderr) != lastLine(strictStderr) {
		t.Errorf("strict error text differs from identity (resolved decision D7):\nidentity: %q\nstrict:   %q",
			lastLine(identityStderr), lastLine(strictStderr))
	}
}

func runCollisionScenario(t *testing.T, level sanitize.Level, existing, wanted string) string {
	t.Helper()
	dir := t.TempDir()
	seedCollision(t, dir, existing)

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(),
		[]string{"site-id", "reserve", "1", wanted, "--registry-dir", dir, "--sanitize", string(level)},
		fixedKeyGetenv, &stdout, &stderr, nil,
	)
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitFailure, stderr.String())
	}
	return stderr.String()
}

// helperMarkerUnexpectedSuccess is emitted by TestHelperTriggerRegistryWriteFailure
// when the RLIMIT_FSIZE injection did not take effect (some sandboxes do not
// enforce it), so the parent test can skip rather than misreport an
// environment limitation as a sanitization bug.
const helperMarkerUnexpectedSuccess = "HELPER_RESULT: unexpected-success"

// TestLoadOrCreateWriteFailureOmitsRegistryPath drives loadOrCreate's
// write-failure branch (registry.go, the previously-unfixed bypass from Step
// 2) in a subprocess with RLIMIT_FSIZE set to zero via the POSIX shell's
// "ulimit -f 0", so the very first write of the new registry's "{}\n"
// document fails with EFBIG. SIGXFSZ is ignored via "trap ” XFSZ" before the
// subprocess execs into the test binary, so POSIX write() returns the error
// instead of the default action (process termination) delivering the
// signal. This runs in a subprocess -- not by mutating this process's own
// rlimit -- because RLIMIT_FSIZE is process-wide and would otherwise starve
// every parallel test in this package that writes a file.
func TestLoadOrCreateWriteFailureOmitsRegistryPath(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("ulimit-based write-failure injection needs a POSIX shell")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell available to inject the write failure")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "helper.ipam.hujson")

	const script = `ulimit -f 0 && trap '' XFSZ && exec "$0" "$@"`
	cmd := exec.Command("sh", "-c", script, os.Args[0], "-test.run=^TestHelperTriggerRegistryWriteFailure$")
	cmd.Env = append(os.Environ(),
		"TAILIPAM_WRITE_FAILURE_HELPER=1",
		"TAILIPAM_WRITE_FAILURE_PATH="+path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()

	output := out.String()
	if strings.Contains(output, helperMarkerUnexpectedSuccess) {
		t.Skip("environment does not enforce RLIMIT_FSIZE; cannot exercise the write-failure branch here")
	}
	if strings.Contains(output, path) || strings.Contains(output, dir) {
		t.Errorf("write-failure error leaks the registry path: %s", output)
	}
	if !strings.Contains(output, "creating registry:") {
		t.Errorf("helper output = %q, want a %q error", output, "creating registry:")
	}
}

// TestHelperTriggerRegistryWriteFailure is not a real test: it is exec'd as
// a subprocess by TestLoadOrCreateWriteFailureOmitsRegistryPath, which sets
// RLIMIT_FSIZE to zero before it starts. Guarded by an env var so a plain
// `go test` run never executes its body.
func TestHelperTriggerRegistryWriteFailure(t *testing.T) {
	t.Parallel()
	if os.Getenv("TAILIPAM_WRITE_FAILURE_HELPER") != "1" {
		t.Skip("only runs as TestLoadOrCreateWriteFailureOmitsRegistryPath's subprocess helper")
	}

	path := os.Getenv("TAILIPAM_WRITE_FAILURE_PATH")
	_, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "site", nil)
	if err == nil {
		fmt.Fprintln(os.Stdout, helperMarkerUnexpectedSuccess)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, err.Error())
	os.Exit(1)
}
