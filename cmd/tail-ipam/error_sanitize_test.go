package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

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
// for a value under kind, using the same tool name, tailnet, and key a
// default-tailnet ("local") invocation resolves. Sanitizer.Value is a pure
// function of (tool, scope, tailnet, kind, value), so this reference
// sanitizer's output equals production's without needing to inspect the
// CLI's internal state.
func placeholderFor(t *testing.T, kind, value string) string {
	t.Helper()
	s, err := sanitize.New(sanitize.Options{
		Level: sanitize.Identity, Tool: toolName, Tailnet: localTailnet, KeyHex: testSanitizeKey,
	})
	if err != nil {
		t.Fatalf("constructing reference sanitizer: %v", err)
	}
	return s.Value(kind, value)
}

// assertNoLeak fails the test if haystack contains value verbatim, the
// interior bytes of value's %q rendering, or that rendering's second
// encoding/json escaping pass. The second check is the regression guard for
// Round 2's exact failure mode: it searched for the *unescaped* value in the
// *escaped* error text and never found it, silently leaving the escaped
// bytes on the page untouched. The third check is the regression guard for
// Round 2's follow-up failure mode: undeclared goa errors JSON-escape the
// message a second time (registerQuotedForm, main.go), so a value that needs
// that second registration can leak in a form neither of the first two
// checks can ever detect -- [json.Marshal] always doubles every backslash
// [strconv.Quote] already introduced, so the double-escaped bytes can never
// contain the raw value or the single-escaped form as a substring.
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
	if b, err := json.Marshal(inner); err == nil {
		double := string(b[1 : len(b)-1])
		if double != "" && double != inner && strings.Contains(haystack, double) {
			t.Errorf("output leaks the double-escaped rendering of %q (form %s): %s", value, double, haystack)
		}
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
	if _, _, _, _, err := registry.ReserveSiteIDWithLogger(
		path,
		"1",
		existing,
		registry.ReservationMetadata{},
		nil,
	); err != nil {
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

	existingQuoted := `"` + placeholderFor(t, kindSite, existing) + `"`
	if !strings.Contains(stderrText, existingQuoted) {
		t.Errorf("stderr = %q, want the quoted existing-site placeholder %q", stderrText, existingQuoted)
	}
	wantedQuoted := `"` + placeholderFor(t, kindSite, wanted) + `"`
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

	if _, ok := errors.AsType[*registry.SiteIDCollisionError](err); ok {
		t.Fatalf("errors.As(err, &registry.SiteIDCollisionError) = true for %v (%T); "+
			"the concrete registry error survived the loopback transport, so the deleted "+
			"errors.As branch in sanitizeErrorIfNeeded was live, not dead", err, err)
	}
}

// TestErrorSanitizationOmitsRegistryPathOnMissingDirectory covers a registry
// path leak reached through a plain "file not found" (registry.go's Load,
// via identityFreePathError) combined with Step 1's pre-registered path
// inventory (registerKnownIdentityInventory), which also sweeps the leak
// closed for callers deeper in tsacl policycore/hujsonedit that this tool does not
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
	_, _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "site", registry.ReservationMetadata{}, nil)
	if err == nil {
		fmt.Fprintln(os.Stdout, helperMarkerUnexpectedSuccess)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, err.Error())
	os.Exit(1)
}

// TestSubnetPoolOverlapSanitizesRegistryReadCIDRsAndSites covers Issue 1:
// when a hand-edited registry has overlapping subnet pools, running a command
// like "subnet list" or "subnet reserve" that reads the registry surfaces
// the raw CIDR and site names in an error (from
// internal/registry/subnetpool.go's canonicalizeSubnetPools). Before this fix,
// registerKnownIdentityInventory did not extract values from the registry
// file, so sanitizeErrorIfNeeded's free-text sweep could not redact values
// deep in tsacl policycore/hujsonedit's error strings. This test seeds a registry
// with overlapping subnet pools using distinctive site names ("secret-hq-site"
// and "secret-branch-site"), runs a command that triggers the overlap error,
// and asserts neither raw site name nor CIDR appears under sanitization.
func TestSubnetPoolOverlapSanitizesRegistryReadCIDRsAndSites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	content := "{\n\t\"subnetPools\": {\n" +
		"\t\t\"10.0.0.0/16\": \"secret-hq-site\",\n" +
		"\t\t\"10.0.1.0/24\": \"secret-branch-site\",\n" +
		"\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSubnet, commandList, "--registry-dir", dir, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)

	if code == exitOK {
		t.Fatalf("overlap error exit = %d, want non-zero; stderr: %s", code, stderr.String())
	}
	stderrText := stderr.String()
	for _, secret := range []string{"secret-hq-site", "secret-branch-site", "10.0.0.0/16", "10.0.1.0/24"} {
		if strings.Contains(stderrText, secret) {
			t.Errorf("sanitized overlap stderr leaks %q: %s", secret, stderrText)
		}
	}
}

// TestSubnetReserveInvalidCIDRSanitizesCliArg covers Issue 4: a CLI-supplied
// CIDR like "10.20.30.45/24" (host bits set) goes through the generated Goa
// HTTP validation and produces an error. Before this fix, registerSweepValue
// in newSubnetLeafCommand's RunE did not register the CIDR argument, so
// sanitizeErrorIfNeeded's free-text sweep could not redact it even though the
// operator provided it as input. This test reserves an invalid CIDR with
// --sanitize identity and asserts the raw CIDR does not leak.
func TestSubnetReserveInvalidCIDRSanitizesCliArg(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSubnet, "reserve", "10.20.30.45/24", "test-site",
		"--registry-dir", dir, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)

	if code == exitOK {
		t.Fatalf("invalid CIDR exit = %d, want non-zero; stderr: %s", code, stderr.String())
	}
	stderrText := stderr.String()
	if strings.Contains(stderrText, "10.20.30.45/24") {
		t.Errorf("sanitized invalid CIDR stderr leaks the raw CIDR: %s", stderrText)
	}
}

// TestSubnetPoolCollisionWithFramingWordsSanitizesAllSegments covers Issue 2:
// the subnet-pool collision message has CIDRs and site names at known byte
// positions, and those must all be redacted by positional splice instead of
// value-based sweep. A site name like "overlaps" or "for" (template words
// from subnetPoolCollisionErrorPattern) would corrupt the message if swept
// value-first, breaking the message frame. This test runs a collision scenario
// where the existing or wanted site name equals a message template word,
// reserves a second overlapping pool with --sanitize identity, and asserts
// the quoted site-name collision segments are redacted (replaced by
// placeholders) so the collision message can be parsed correctly.
func TestSubnetPoolCollisionWithFramingWordsSanitizesAllSegments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		existingSite string
		wantedSite   string
		existingCIDR string
		wantedCIDR   string
		reserveFirst bool
	}{
		{
			name:         "wanted site is 'for'",
			existingSite: "hq-vpc",
			wantedSite:   "for",
			existingCIDR: "10.0.0.0/16",
			wantedCIDR:   "10.0.1.0/24",
			reserveFirst: true,
		},
		{
			name:         "wanted site is 'not'",
			existingSite: "hq-vpc",
			wantedSite:   "not",
			existingCIDR: "10.0.0.0/16",
			wantedCIDR:   "10.0.1.0/24",
			reserveFirst: true,
		},
		{
			name:         "wanted site is 'overlaps'",
			existingSite: "hq-vpc",
			wantedSite:   "overlaps",
			existingCIDR: "10.0.0.0/16",
			wantedCIDR:   "10.0.1.0/24",
			reserveFirst: true,
		},
		{
			name:         "existing site is 'for'",
			existingSite: "for",
			wantedSite:   "wanted-site",
			existingCIDR: "10.0.0.0/16",
			wantedCIDR:   "10.0.1.0/24",
			reserveFirst: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testSubnetPoolCollision(t, tc.existingSite, tc.wantedSite, tc.existingCIDR, tc.wantedCIDR)
		})
	}
}

func testSubnetPoolCollision(t *testing.T, existingSite, wantedSite, existingCIDR, wantedCIDR string) {
	t.Helper()
	dir := t.TempDir()
	if _, _, err := runSubnet(t, dir, "reserve", existingCIDR, existingSite); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSubnet, "reserve", wantedCIDR, wantedSite,
		"--registry-dir", dir, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)

	if code != exitFailure {
		t.Fatalf("collision exit = %d, want %d; stderr: %s", code, exitFailure, stderr.String())
	}
	stderrText := stderr.String()

	if !strings.Contains(stderrText, "overlaps") || !strings.Contains(stderrText, "reserved for") {
		t.Errorf("stderr = %q, want the collision message frame intact", stderrText)
	}

	if strings.Contains(stderrText, existingCIDR) {
		t.Errorf("sanitized collision stderr leaks %q: %s", existingCIDR, stderrText)
	}
	if strings.Contains(stderrText, wantedCIDR) {
		t.Errorf("sanitized collision stderr leaks %q: %s", wantedCIDR, stderrText)
	}

	assertNoQuotedLeak(t, stderrText, existingSite)
	assertNoQuotedLeak(t, stderrText, wantedSite)
}

// assertReserveMessageSanitized runs command (a reserve invocation, with
// --registry-dir/--sanitize/--format appended) against dir and checks that
// the JSON envelope's Message field is consistent with the sanitized Site
// field -- neither the raw site name nor its %q-escaped rendering survives
// in Message, and Message contains the same placeholder Site does. This is
// the regression guard for issue #262: Message used to be pre-formatted with
// %q before sanitization ran, then swept with a value-based Text() call
// against the sanitizer's *unescaped* registered value -- the same desync
// sanitizeErrorIfNeeded's doc comment (main.go) documents for the error
// path. messageBuilder (output.go) fixes this by rendering Message from the
// envelope's own already-sanitized Site field instead.
func assertReserveMessageSanitized(t *testing.T, dir string, command []string, site string) {
	t.Helper()
	args := append(append([]string{}, command...),
		"--registry-dir", dir, "--sanitize", "identity", "--format", formatJSON)

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), args, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitOK {
		t.Fatalf("reserve exit = %d, want %d; stderr: %s", code, exitOK, stderr.String())
	}

	var envelope outputEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode reserve JSON: %v", err)
	}

	assertNoLeak(t, envelope.Message, site)
	if envelope.Site == "" || !strings.Contains(envelope.Message, envelope.Site) {
		t.Errorf("message/site placeholder mismatch: message=%q, site=%q", envelope.Message, envelope.Site)
	}
	if !strings.HasPrefix(envelope.Site, "site-") {
		t.Errorf("Site field not sanitized: got %q, want prefix %q", envelope.Site, "site-")
	}
}

// TestReserveMessageSanitizesEscapedSiteName covers the first-reservation
// ("reserved ... for %q") message template for both site-id and subnet
// reserve, across site names that stress %q escaping.
func TestReserveMessageSanitizesEscapedSiteName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, site string
	}{
		{name: "embedded backslash", site: `acme\secret`},
		{name: "embedded quote", site: `acme"secret`},
		{name: "quote and backslash", site: `se\c"ret`},
		{name: "trailing backslash", site: `acme\`},
	}

	for _, tc := range cases {
		t.Run("site-id/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assertReserveMessageSanitized(t, t.TempDir(),
				[]string{commandSiteID, "reserve", "1", tc.site}, tc.site)
		})
		t.Run("subnet/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assertReserveMessageSanitized(t, t.TempDir(),
				[]string{commandSubnet, "reserve", "10.0.0.0/24", tc.site}, tc.site)
		})
	}
}

// TestReserveAgainMessageSanitizesEscapedSiteName covers the second,
// distinct message template ("already reserved for %q") reached when the
// same id/CIDR+site pair is reserved twice.
func TestReserveAgainMessageSanitizesEscapedSiteName(t *testing.T) {
	t.Parallel()
	const site = `acme\secret`

	t.Run("site-id", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if _, _, err := runSiteID(t, dir, "reserve", "1", site); err != nil {
			t.Fatalf("seed reserve: %v", err)
		}
		assertReserveMessageSanitized(t, dir, []string{commandSiteID, "reserve", "1", site}, site)
	})
	t.Run("subnet", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if _, _, err := runSubnet(t, dir, "reserve", "10.0.0.0/24", site); err != nil {
			t.Fatalf("seed reserve: %v", err)
		}
		assertReserveMessageSanitized(t, dir, []string{commandSubnet, "reserve", "10.0.0.0/24", site}, site)
	})
}

// TestSubnetPoolExactCollisionSanitizesAdversarialSiteNames exercises the
// exact-key collision shape separately from the overlap shape above. The
// quote/backslash case proves the sanitizer uses regex-located byte ranges
// instead of searching the still-escaped message for unescaped values. The
// framing-word case proves it replaces only the captured site-name segments,
// not every matching value in the message frame.
func TestSubnetPoolExactCollisionSanitizesAdversarialSiteNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		existingSite string
		wantedSite   string
	}{
		{
			name:         "quoted and escaped site names",
			existingSite: `secret"quote-existing`,
			wantedSite:   `wanted\backslash`,
		},
		{
			name:         "site names collide with framing words",
			existingSite: "reserved",
			wantedSite:   "for",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testSubnetPoolExactCollision(t, tc.existingSite, tc.wantedSite)
		})
	}
}

func testSubnetPoolExactCollision(t *testing.T, existingSite, wantedSite string) {
	t.Helper()
	const cidr = "10.0.0.0/16"
	dir := t.TempDir()
	if _, _, err := runSubnet(t, dir, "reserve", cidr, existingSite); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSubnet, "reserve", cidr, wantedSite,
		"--registry-dir", dir, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitFailure {
		t.Fatalf("collision exit = %d, want %d; stderr: %s", code, exitFailure, stderr.String())
	}
	stderrText := stderr.String()

	if !strings.Contains(stderrText, "is already reserved for") || !strings.Contains(stderrText, ", not ") {
		t.Errorf("stderr = %q, want the exact-key collision message frame intact", stderrText)
	}
	if strings.Contains(stderrText, cidr) {
		t.Errorf("sanitized exact-key collision stderr leaks %q: %s", cidr, stderrText)
	}
	assertNoQuotedLeak(t, stderrText, existingSite)
	assertNoQuotedLeak(t, stderrText, wantedSite)

	existingPlaceholder := `"` + placeholderFor(t, kindSite, existingSite) + `"`
	if !strings.Contains(stderrText, existingPlaceholder) {
		t.Errorf("stderr = %q, want existing-site placeholder %q", stderrText, existingPlaceholder)
	}
	wantedPlaceholder := `"` + placeholderFor(t, kindSite, wantedSite) + `"`
	if !strings.Contains(stderrText, wantedPlaceholder) {
		t.Errorf("stderr = %q, want wanted-site placeholder %q", stderrText, wantedPlaceholder)
	}
}

// helperMarkerServiceUnexpectedSuccess is the service-reserve counterpart of
// helperMarkerUnexpectedSuccess above, kept as a separate constant so the two
// subprocess helpers' skip markers can never be confused with each other.
const helperMarkerServiceUnexpectedSuccess = "HELPER_RESULT: service-unexpected-success"

// TestServiceReserveWriteFailureCanonicalAddressOmitsBareIP is the review's
// T3: the original finding's literal frame -- serviceaddress.go's
// "reserving service address %s: %w" -- which is reached only after Load
// succeeds and ReserveServiceAddressWithLogger's hujsonedit.WritePatch call
// fails. It reuses TestLoadOrCreateWriteFailureOmitsRegistryPath's
// RLIMIT_FSIZE subprocess injection (same ulimit/trap/exec shell wrapper,
// same re-exec-the-test-binary pattern), but pre-creates a valid "{}\n"
// registry file in the parent first, so the child's Load succeeds and the injected
// EFBIG lands in hujsonedit.WriteRaw instead. That distinction is what makes
// this test different from every prior-round injection (see the doc comments
// on TestServiceReserveWithSlashThirtytwoAliasSanitizesCanonicalAddress and
// TestServiceReserveBareSanitizationAndServiceNameAliases in
// service_test.go): a Load-level failure -- a missing directory, or a
// registry path that is itself a directory -- returns a bare, identity-free
// errno string (see identityFreePathError) before the
// "reserving service address %s:" frame is ever built, so it cannot exercise
// the canonicalized-address registration fix under test here. This test
// drives the failure through runCLI via a real subprocess (not a direct
// registry.ReserveServiceAddressWithLogger call, unlike
// TestHelperTriggerRegistryWriteFailure above) so both the RunE
// canonical-registration fix (service.go) and sanitizeErrorIfNeeded's
// free-text sweep (main.go) run exactly as they do in production.
func TestServiceReserveWriteFailureCanonicalAddressOmitsBareIP(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("ulimit-based write-failure injection needs a POSIX shell")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell available to inject the write failure")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("pre-create valid registry: %v", err)
	}

	const script = `ulimit -f 0 && trap '' XFSZ && exec "$0" "$@"`
	cmd := exec.Command("sh", "-c", script, os.Args[0], "-test.run=^TestHelperTriggerServiceReserveWriteFailure$")
	cmd.Env = append(os.Environ(),
		"TAILIPAM_SERVICE_WRITE_FAILURE_HELPER=1",
		"TAILIPAM_SERVICE_WRITE_FAILURE_DIR="+dir,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()

	output := out.String()
	if strings.Contains(output, helperMarkerServiceUnexpectedSuccess) {
		t.Skip("environment does not enforce RLIMIT_FSIZE; cannot exercise the write-failure branch here")
	}
	if !strings.Contains(output, "reserving service address ") {
		t.Fatalf("helper output = %q, want the %q frame", output, "reserving service address ")
	}

	const canonical = "100.100.100.9"
	if strings.Contains(output, canonical) {
		t.Errorf("write-failure error leaks the canonical address %q: %s", canonical, output)
	}
	placeholder := placeholderFor(t, kindIP, canonical)
	if !strings.Contains(output, placeholder) {
		t.Errorf("helper output = %q, want the canonical-address placeholder %q", output, placeholder)
	}
}

// TestHelperTriggerServiceReserveWriteFailure is not a real test: it is
// exec'd as a subprocess by
// TestServiceReserveWriteFailureCanonicalAddressOmitsBareIP, which sets
// RLIMIT_FSIZE to zero and pre-creates a valid registry file before it
// starts. Unlike TestHelperTriggerRegistryWriteFailure, this helper drives
// runCLI so the RunE registration fix and sanitizeErrorIfNeeded both run.
// Guarded by an env var so a plain `go test` run never executes its body.
func TestHelperTriggerServiceReserveWriteFailure(t *testing.T) {
	t.Parallel()
	if os.Getenv("TAILIPAM_SERVICE_WRITE_FAILURE_HELPER") != "1" {
		t.Skip("only runs as TestServiceReserveWriteFailureCanonicalAddressOmitsBareIP's subprocess helper")
	}

	registryDir := os.Getenv("TAILIPAM_SERVICE_WRITE_FAILURE_DIR")
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", "100.100.100.9/32", "web",
		"--registry-dir", registryDir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		fmt.Fprintln(os.Stdout, helperMarkerServiceUnexpectedSuccess)
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, stderr.String())
	os.Exit(1)
}

// TestServiceAddressPaddedArgRegistersTrimmingSpelling covers Issue 1: when a
// service address arg has leading/trailing whitespace around an invalid
// address, NormalizeServiceAddress trims it before validation, and error
// messages echo the trimmed form. The fix registers both the raw padded value
// and its trimmed equivalent so the error's trimmed spelling is swept during
// sanitization.
func TestServiceAddressPaddedArgRegistersTrimmingSpelling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	paddedAddr := " secret-host.example "
	trimmedAddr := "secret-host.example"

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", paddedAddr, "dashboard",
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		t.Fatalf("invalid address exit = %d, want non-zero", code)
	}
	stderrText := stderr.String()
	if strings.Contains(stderrText, trimmedAddr) {
		t.Errorf("stderr leaks trimmed address %q: %s", trimmedAddr, stderrText)
	}
	trimmedPlaceholder := placeholderFor(t, kindIP, trimmedAddr)
	if !strings.Contains(stderrText, trimmedPlaceholder) {
		t.Errorf("stderr = %q, want trimmed-address placeholder %q", stderrText, trimmedPlaceholder)
	}
}

// TestServiceNamePaddedArgRegistersTrimmingSpelling covers Issue 1 with a
// genuine, non-svc: invalid service name. The original version of this test
// reserved only " svc: " (the literal grammar token svcSelectorToken), which
// registerSweepValue deliberately never registers -- see
// TestServiceNameSvcSelectorNotOverRedacted -- so the padded/escaped value
// never appeared in the error text either way, and the assertion (checking
// only that the literal word "svc:" survives) passed identically with the
// round-2 fix reverted. This drives a real control-character service name
// (not the svc: token) through the same %q-escape closure as
// TestOwnerMetadataRegistersForErrorPath: NormalizeServiceName trims before
// rejectControlChars runs, so the error's %q rendering is of the *trimmed*
// value, and the interior control character is escaped rather than
// byte-identical to what registerSweepValueWithCanonical registers raw.
func TestServiceNamePaddedArgRegistersTrimmingSpelling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const paddedName = " sec\tret-svc "
	const trimmedEscaped = `sec\tret-svc`

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", "100.100.100.8", paddedName,
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		t.Fatalf("invalid service exit = %d, want non-zero", code)
	}
	stderrText := stderr.String()
	if strings.Contains(stderrText, trimmedEscaped) {
		t.Errorf("stderr leaks the %%q-escaped service name %q: %s", trimmedEscaped, stderrText)
	}
	placeholder := placeholderFor(t, kindService, trimmedEscaped)
	if !strings.Contains(stderrText, placeholder) {
		t.Errorf("stderr = %q, want service-name placeholder %q", stderrText, placeholder)
	}
}

// TestSubnetCIDRCanonicalSuggestionRegistersForErrorSweep replaces the
// vacuous TestSubnetCIDRPaddedArgRegistersTrimmingSpelling (verifier-proven
// to pass with the round-2 fix reverted: it asserted only that the padded
// raw CIDR arg does not leak, which round 1's registerSweepValueWithCanonical
// call already guaranteed). NormalizeSubnetCIDR's "not canonical" error
// embeds a masked canonical *suggestion* the CLI never received as input
// (subnet.go's registerCanonicalCIDRSuggestion computes it independently),
// so this is the item-2 leak case: a non-canonical CIDR whose suggestion is a
// distinct value from anything registerSweepValueWithCanonical's own
// raw/trimmed/canonical(err==nil) branches ever see, since NormalizeSubnetCIDR
// treats "not canonical" as an error, never a successful canonicalization.
func TestSubnetCIDRCanonicalSuggestionRegistersForErrorSweep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const nonCanonical = "10.20.30.45/24"
	const canonicalSuggestion = "10.20.30.0/24"

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSubnet, "reserve", nonCanonical, "test-site",
		"--registry-dir", dir, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		t.Fatalf("non-canonical CIDR exit = %d, want non-zero", code)
	}
	stderrText := stderr.String()
	if strings.Contains(stderrText, canonicalSuggestion) {
		t.Errorf("stderr leaks the canonical CIDR suggestion %q: %s", canonicalSuggestion, stderrText)
	}
	placeholder := placeholderFor(t, kindCIDR, canonicalSuggestion)
	if !strings.Contains(stderrText, placeholder) {
		t.Errorf("stderr = %q, want the canonical-suggestion placeholder %q", stderrText, placeholder)
	}
	if !strings.Contains(stderrText, "not canonical") {
		t.Errorf("stderr = %q, want the 'not canonical' message frame intact", stderrText)
	}
}

// TestOwnerMetadataRegistersForErrorPath replaces the vacuous original
// (verifier-proven to pass with the round-2 fix reverted: it seeded and
// re-reserved with two *distinct plain* owner values and asserted only the
// collision message frame, never anything owner-specific -- so it passed
// whether or not the owner value was registered for the sweep at all). This
// drives the real --owner control-character 400 path
// (normalizeMetadata -> rejectControlChars, internal/registry/entry.go) and
// asserts the anti-vacuity trio: the %q-escaped owner rendering is absent,
// the exact kindOwner placeholder for that escaped rendering is present, and
// the message frame survives intact.
func TestOwnerMetadataRegistersForErrorPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const owner = "ow\tner"
	const ownerEscaped = `ow\tner`

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", "100.100.100.8", "dashboard",
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
		"--owner", owner,
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		t.Fatalf("control-character owner exit = %d, want non-zero", code)
	}
	stderrText := stderr.String()
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty on error", stdout.String())
	}
	if strings.Contains(stderrText, ownerEscaped) {
		t.Errorf("stderr leaks the %%q-escaped owner %q: %s", ownerEscaped, stderrText)
	}
	placeholder := placeholderFor(t, kindOwner, ownerEscaped)
	if !strings.Contains(stderrText, placeholder) {
		t.Errorf("stderr = %q, want owner placeholder %q", stderrText, placeholder)
	}
	if !strings.Contains(stderrText, "must not contain control characters") {
		t.Errorf("stderr = %q, want the control-character message frame intact", stderrText)
	}
}

// TestServiceNameSvcSelectorNotOverRedacted covers Issue 3: the literal
// string "svc:" is grammar (a policy selector token), not an identity, so it
// should not be registered for sweep. When someone reserves with name "svc:",
// the error message must contain the literal template words unchanged for
// readability.
func TestServiceNameSvcSelectorNotOverRedacted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", "100.100.100.8", "svc:",
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		t.Fatalf("invalid service-name exit = %d, want non-zero", code)
	}
	stderrText := stderr.String()
	if !strings.Contains(stderrText, "svc:") {
		t.Errorf("stderr = %q, want literal 'svc:' in error message (not over-redacted)", stderrText)
	}
	if !strings.Contains(stderrText, "service name must not be") {
		t.Errorf("stderr = %q, want readable error message frame", stderrText)
	}
}

// TestDoubleEscapeDepthSanitization covers the complete input domain for the
// escape-depth fix (Batch B Attempt 2): values that undergo both %q escaping
// (strconv.Quote, used for declared goa errors decoded as ServiceError) and
// JSON escaping (encoding/json.Marshal, used for undeclared goa errors
// returned as 500 faults). Each row is embedded as a properly JSON-quoted
// registry value -- unlike Round 2's unquoted splice, which produced
// unparseable HuJSON that hujson's own tokenizer rejected before the value
// ever reached a %q-formatting error, silently passing regardless of whether
// the fix under test was present or reverted (see the round-3 assignment's
// "WHY ROUND 2 FAILED" analysis). A valid site name is loaded successfully by
// registerKnownIdentityInventory (registering both escape depths) and then
// re-surfaces, %q-formatted, in an unrelated validation failure raised deep
// in internal/registry -- a genuinely undeclared error that crosses the
// loopback transport as a JSON-escaped 500 fault. Each row verifies a value's
// registration at both escape depths covers all its representations on the
// wire, so sanitizeErrorIfNeeded's Text sweep redacts it regardless of
// escaping depth. Tests both pre-existing paths (site names, via a subnet-pool
// overlap) and the new UDP-port path (device names, via a control-character
// rejection) to verify the fix is general across all identity registration
// points.
//
// The original Round 2 domain additionally carried an "invalid UTF-8 with
// control character" site row. That row is not reachable through any real
// validation error in this codebase: NormalizeSiteName and NormalizeDeviceName
// both check [utf8.ValidString] before rejectControlChars and, on invalid UTF-8,
// return a fixed, non-interpolated message ("... must be valid UTF-8") that
// never echoes the value -- so there is no %q-formatted error for such a value
// to leak from. It is also impossible to route an invalid byte through a
// registry *file* fixture at all: hujson/encoding/json's own decode of the
// fixture (shared by both the pre-registration sweep and the eventual error
// path) replaces invalid UTF-8 with U+FFFD before either one ever sees it,
// which would make the row vacuous for a reason unrelated to the fix. That row
// is therefore dropped rather than replaced with a non-representative stand-in.
func TestDoubleEscapeDepthSanitization(t *testing.T) {
	t.Parallel()

	domainTable := []struct {
		name     string
		value    string
		kind     string
		isDevice bool
	}{
		{
			name:  "control character (row 1)",
			value: "laptop\x01secret",
			kind:  kindSite,
		},
		{
			name:  "embedded quote with control character (row 2)",
			value: "lap\"top\x01secret",
			kind:  kindSite,
		},
		{
			name:  "backslash with control character (row 3)",
			value: "back\\slash\x01x",
			kind:  kindSite,
		},
		{
			name:  "HTML-special < with control character (row 4)",
			value: "lt<gt\x01x",
			kind:  kindSite,
		},
		{
			name:  "ampersand with control character (row 5)",
			value: "amp&and\x01x",
			kind:  kindSite,
		},
		{
			name:  "tab character with control character (row 6)",
			value: "tab\tsep\x01x",
			kind:  kindSite,
		},
		{
			name:     "device name with control character (UDP path)",
			value:    "device\x01secret",
			kind:     kindHostname,
			isDevice: true,
		},
		{
			name:     "device name with embedded quote and control character (UDP path)",
			value:    "dev\"ice\x01secret",
			kind:     kindHostname,
			isDevice: true,
		},
	}

	for _, tc := range domainTable {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()

			if tc.isDevice {
				testDoubleEscapeDeviceName(t, dir, tc.value)
			} else {
				testDoubleEscapeSiteName(t, dir, tc.value)
			}
		})
	}
}

// testDoubleEscapeSiteName verifies that a site name doesn't leak in either
// escape form in error messages returned as goa faults. It reserves two
// overlapping subnet pools under distinct sites, one of them siteName, so
// canonicalizeSubnetPools' overlap check (internal/registry/subnetpool.go)
// raises an undeclared, unclassified error that %q-formats siteName directly
// -- unconditionally, regardless of what characters it contains -- and
// crosses the loopback transport as a JSON-escaped 500 fault.
func testDoubleEscapeSiteName(t *testing.T, dir, siteName string) {
	t.Helper()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")

	quotedSite, err := json.Marshal(siteName)
	if err != nil {
		t.Fatalf("marshaling site name for registry fixture: %v", err)
	}
	registryContent := `{
	"subnetPools": {
		"10.0.0.0/16": ` + string(quotedSite) + `,
		"10.0.1.0/24": "plain-other-site"
	}
}
`
	if writeErr := os.WriteFile(path, []byte(registryContent), 0o644); writeErr != nil {
		t.Fatalf("writing registry file: %v", writeErr)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(),
		[]string{commandSubnet, commandList, "--registry-dir", dir, "--sanitize", "identity"},
		fixedKeyGetenv, &stdout, &stderr, nil,
	)
	if code == exitOK {
		t.Fatalf("want a subnet-pool overlap error, got exit code %d; stdout: %s", code, stdout.String())
	}

	stderrText := stderr.String()
	assertNoLeak(t, stderrText, siteName)
}

// testDoubleEscapeDeviceName verifies that a device name doesn't leak in
// either escape form in error messages returned as goa faults. deviceName
// carries a control character, so NormalizeDeviceName
// (internal/registry/udpport.go) rejects it while decoding the registry,
// %q-formatting it in an undeclared error that crosses the loopback transport
// as a JSON-escaped 500 fault.
func testDoubleEscapeDeviceName(t *testing.T, dir, deviceName string) {
	t.Helper()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")

	quotedDevice, err := json.Marshal(deviceName)
	if err != nil {
		t.Fatalf("marshaling device name for registry fixture: %v", err)
	}
	registryContent := `{
	"udpPorts": {
		"test-site": {
			"41642": ` + string(quotedDevice) + `
		}
	}
}
`
	if writeErr := os.WriteFile(path, []byte(registryContent), 0o644); writeErr != nil {
		t.Fatalf("writing registry file: %v", writeErr)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(),
		[]string{commandPort, commandList, "--site", "test-site", "--registry-dir", dir, "--sanitize", "identity"},
		fixedKeyGetenv, &stdout, &stderr, nil,
	)
	if code == exitOK {
		t.Fatalf("want a device-name control-character error, got exit code %d; stdout: %s", code, stdout.String())
	}

	stderrText := stderr.String()
	assertNoLeak(t, stderrText, deviceName)
}
