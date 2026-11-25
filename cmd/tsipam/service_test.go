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

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/registry"
)

// runService drives the command through Cobra and the generated Goa loopback
// HTTP transport, matching the other registry command helpers.
func runService(t *testing.T, registryDir string, args ...string) (string, string, error) {
	t.Helper()
	state := &commandState{}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{commandService}, append(args, "--registry-dir", registryDir)...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestServiceReserveRepeatAndAddressAliases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	stdout, stderr, err := runService(t, dir, "reserve", "100.100.100.8/32", "dashboard")
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if stderr != "" {
		t.Errorf("reserve stderr = %q, want empty", stderr)
	}
	if stdout != "reserved service address 100.100.100.8 for \"svc:dashboard\"\n" {
		t.Errorf("reserve stdout = %q, want canonical confirmation", stdout)
	}

	stdout, stderr, err = runService(
		t, dir, "reserve", "100.100.100.8", "svc:dashboard", "--format", formatJSON,
	)
	if err != nil {
		t.Fatalf("repeat reserve: %v", err)
	}
	if stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("repeat reserve stdout/stderr = %q/%q, want parse-clean JSON and empty stderr", stdout, stderr)
	}
	var result outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Command != commandServiceReserve || result.Status != statusNoChanges ||
		result.Changed == nil || *result.Changed || result.Address != "100.100.100.8" ||
		result.Service != "svc:dashboard" {
		t.Errorf("repeat reserve envelope = %+v, want canonical no_changes result", result)
	}
}

func TestServiceReleaseAndAbsentRelease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, _, err := runService(t, dir, "reserve", "100.100.100.8", "dashboard"); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}

	stdout, stderr, err := runService(t, dir, "release", "100.100.100.8/32")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if stderr != "" || stdout != "released service address 100.100.100.8\n" {
		t.Errorf("release stdout/stderr = %q/%q, want canonical confirmation", stdout, stderr)
	}

	stdout, stderr, err = runService(t, dir, "release", "100.100.100.8", "--format", formatJSON)
	if err != nil {
		t.Fatalf("absent release: %v", err)
	}
	if stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("absent release stdout/stderr = %q/%q, want parse-clean JSON", stdout, stderr)
	}
	var result outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Command != commandServiceRelease || result.Status != statusNoChanges ||
		result.Changed == nil || *result.Changed || result.Address != "100.100.100.8" {
		t.Errorf("absent release envelope = %+v, want canonical no_changes result", result)
	}
}

func TestServiceListUsesNumericAddressOrderInHumanAndJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reservations := []struct{ address, service string }{
		{"100.100.100.20", "twenty"},
		{"100.100.100.2", "two"},
		{"100.100.100.10", "ten"},
	}
	for _, reservation := range reservations {
		if _, _, err := runService(t, dir, "reserve", reservation.address, reservation.service); err != nil {
			t.Fatalf("reserve %s: %v", reservation.address, err)
		}
	}

	stdout, stderr, err := runService(t, dir, commandList)
	if err != nil {
		t.Fatalf("human list: %v", err)
	}
	wantHuman := "100.100.100.2\tsvc:two\n" +
		"100.100.100.10\tsvc:ten\n" +
		"100.100.100.20\tsvc:twenty\n"
	if stderr != "" || stdout != wantHuman {
		t.Errorf("human list stdout/stderr = %q/%q, want %q/empty", stdout, stderr, wantHuman)
	}

	stdout, stderr, err = runService(t, dir, commandList, "--format", formatJSON)
	if err != nil {
		t.Fatalf("JSON list: %v", err)
	}
	if stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("JSON list stdout/stderr = %q/%q, want parse-clean JSON", stdout, stderr)
	}
	var result outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Command != commandServiceList || result.ServiceAddresses == nil || len(*result.ServiceAddresses) != 3 {
		t.Fatalf("JSON list envelope = %+v, want three service addresses", result)
	}
	for index, address := range []string{"100.100.100.2", "100.100.100.10", "100.100.100.20"} {
		if (*result.ServiceAddresses)[index].Address != address {
			t.Errorf("JSON list address %d = %q, want %q", index, (*result.ServiceAddresses)[index].Address, address)
		}
	}
}

func TestServiceReserveMetadataAppearsInList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stdout, stderr, err := runService(
		t, dir, "reserve", "100.100.100.8", "dashboard", "--format", formatJSON,
		"--owner", "network-team", "--ticket", "NET-237", "--description", "internal dashboard",
		"--reserved-at", "2026-08-31T12:00:00Z",
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("reserve stdout/stderr = %q/%q, want parse-clean JSON", stdout, stderr)
	}

	stdout, _, err = runService(t, dir, commandList, "--format", formatJSON)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var result outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.ServiceAddresses == nil || len(*result.ServiceAddresses) != 1 {
		t.Fatalf("list envelope = %+v, want one reservation", result)
	}
	entry := (*result.ServiceAddresses)[0]
	if entry.Owner != "network-team" || entry.Ticket != "NET-237" ||
		entry.Description != "internal dashboard" || entry.ReservedAt != "2026-08-31T12:00:00Z" {
		t.Errorf("list metadata = %+v, want all authored fields", entry)
	}
}

func TestServiceReserveRejectsInvalidAddressesWithParseCleanStdout(t *testing.T) {
	t.Parallel()
	for _, address := range []string{
		"not-an-address", "100.100.100.0/24", "2001:db8::8", "::ffff:100.100.100.8",
	} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()
			stdout, _, err := runService(t, t.TempDir(), "reserve", address, "dashboard")
			if err == nil {
				t.Fatalf("reserve %q: error = nil, want invalid_input", address)
			}
			var serviceError *goa.ServiceError
			if !errors.As(err, &serviceError) || serviceError.Name != "invalid_input" {
				t.Fatalf("reserve %q error = %v, want invalid_input ServiceError", address, err)
			}
			if exitCodeFor(err) != exitUsage {
				t.Errorf("exitCodeFor(%q error) = %d, want %d", address, exitCodeFor(err), exitUsage)
			}
			if stdout != "" {
				t.Errorf("reserve %q stdout = %q, want empty on failure", address, stdout)
			}
		})
	}
}

// TestServiceReserveRejectsInvalidUTF8ServiceNameBeforeWrite covers a
// client-side pre-check: a JSON POST body silently rewrites invalid UTF-8 to
// U+FFFD (which is itself valid UTF-8), so a server-side-only check would
// accept the mangled value with exit 0 and a corrupted registry entry. The
// service-name argument must be validated against the raw argv before the
// loopback client ever sends it, mirroring newUDPPortLeafCommand's
// device-name pre-check (udpport.go).
func TestServiceReserveRejectsInvalidUTF8ServiceNameBeforeWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	stdout, _, err := runService(t, dir, "reserve", "100.100.100.8", "svc\xff")
	if err == nil {
		t.Fatal("reserve with invalid UTF-8 service name: error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("reserve error = %v, want it to mention UTF-8", err)
	}
	if exitCodeFor(err) == exitOK {
		t.Errorf("exitCodeFor(err) = %d, want non-zero", exitCodeFor(err))
	}
	if stdout != "" {
		t.Errorf("reserve stdout = %q, want empty on failure", stdout)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "local.ipam.hujson")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("registry exists after invalid input: %v", statErr)
	}
}

// TestServiceReserveAcceptsValidMultiByteUTF8ServiceName is the accept-path
// counterpart of the rejection test above: valid multi-byte UTF-8 must keep
// working end to end through reserve, list, and release.
func TestServiceReserveAcceptsValidMultiByteUTF8ServiceName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const service = "büro-dash"

	if _, _, err := runService(t, dir, "reserve", "100.100.100.8", service); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	out, _, err := runService(t, dir, commandList)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, service) {
		t.Errorf("list stdout = %q, want it to contain %q", out, service)
	}
	if _, _, err = runService(t, dir, "release", "100.100.100.8"); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestServiceCommandValidatesArity(t *testing.T) {
	t.Parallel()
	tests := [][]string{
		{"reserve", "100.100.100.8"},
		{"reserve", "100.100.100.8", "dashboard", "extra"},
		{"release"},
		{"release", "100.100.100.8", "extra"},
		{commandList, "extra"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			stdout, _, err := runService(t, t.TempDir(), args...)
			if err == nil || exitCodeFor(err) != exitUsage {
				t.Errorf("service %v error = %v, want usage error", args, err)
			}
			if stdout != "" {
				t.Errorf("service %v stdout = %q, want empty", args, stdout)
			}
		})
	}
}

func TestServiceListMissingRegistryFailsWithEmptyStdout(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "missing")
	stdout, _, err := runService(t, dir, commandList, "--format", formatJSON)
	if err == nil {
		t.Fatal("list missing registry: error = nil, want failure")
	}
	if stdout != "" {
		t.Errorf("list missing registry stdout = %q, want empty", stdout)
	}
}

func TestServiceCollisionIsRefusalWithEmptyStdout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, _, err := runService(t, dir, "reserve", "100.100.100.8", "dashboard"); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}
	stdout, _, err := runService(t, dir, "reserve", "100.100.100.8/32", "metrics")
	var serviceError *goa.ServiceError
	if !errors.As(err, &serviceError) || serviceError.Name != serviceErrorCollision {
		t.Fatalf("collision error = %v, want named collision ServiceError", err)
	}
	if exitCodeFor(err) != exitFailure {
		t.Errorf("exitCodeFor(collision) = %d, want %d", exitCodeFor(err), exitFailure)
	}
	if stdout != "" {
		t.Errorf("collision stdout = %q, want empty", stdout)
	}
}

func TestServiceReserveCommandPreservesHandWrittenComment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	content := "{\n\t\"serviceAddresses\": {\n" +
		"\t\t// dashboard VIP approved in NET-200\n" +
		"\t\t\"100.100.100.8\": \"svc:dashboard\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	if _, _, err := runService(t, dir, "reserve", "100.100.100.9", "metrics"); err != nil {
		t.Fatalf("reserve unrelated address: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if !strings.Contains(string(got), "// dashboard VIP approved in NET-200") {
		t.Errorf("registry after unrelated edit = %q, want hand-written comment preserved", got)
	}
}

func TestServiceAddressCollisionErrorFormatMatchesAnchoredSanitizerPattern(t *testing.T) {
	t.Parallel()
	text := (&registry.ServiceAddressCollisionError{
		Address: "100.100.100.8", Existing: `svc:dash"board`, Wanted: `svc:met\rics`,
	}).Error()
	match := serviceAddressCollisionErrorPattern.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("serviceAddressCollisionErrorPattern does not match %q", text)
	}
	want := []string{"100.100.100.8", `"svc:dash\"board"`, `"svc:met\\rics"`}
	for index, value := range want {
		if match[index+1] != value {
			t.Errorf("capture %d = %q, want %q", index+1, match[index+1], value)
		}
	}
}

func TestServiceCollisionSanitizesAdversarialIdentities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		existing, wanted string
	}{
		{name: "plain", existing: "svc:dashboard", wanted: "svc:metrics"},
		{name: "embedded quote", existing: `svc:dash"board`, wanted: `svc:met"rics`},
		{name: "embedded backslash", existing: `svc:dash\board`, wanted: `svc:met\rics`},
		{name: "template words", existing: "svc:for", wanted: "svc:not"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertServiceCollisionSanitized(t, test.existing, test.wanted)
		})
	}
}

func assertServiceCollisionSanitized(t *testing.T, existing, wanted string) {
	t.Helper()
	dir := t.TempDir()
	address := "100.100.100.8"
	if _, _, err := runService(t, dir, "reserve", address, existing); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", address + "/32", wanted,
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitFailure {
		t.Fatalf("collision exit = %d, want %d (stderr: %s)", code, exitFailure, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("collision stdout = %q, want empty", stdout.String())
	}
	for _, secret := range []string{address, existing, wanted} {
		assertNoLeak(t, stderr.String(), secret)
	}
	if !strings.Contains(stderr.String(), "service address ") ||
		!strings.Contains(stderr.String(), " is already reserved for ") ||
		!strings.Contains(stderr.String(), ", not ") {
		t.Errorf("sanitized collision lost message frame: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "ip-") || !strings.Contains(stderr.String(), "service-") {
		t.Errorf("sanitized collision has unexpected placeholder kinds: %s", stderr.String())
	}
}

func TestServiceAddressCollisionTypeUsesRefusalExitCode(t *testing.T) {
	t.Parallel()
	err := &registry.ServiceAddressCollisionError{
		Address: "100.100.100.8", Existing: "svc:dashboard", Wanted: "svc:metrics",
	}
	if got := exitCodeFor(err); got != exitFailure {
		t.Errorf("exitCodeFor(ServiceAddressCollisionError) = %d, want %d", got, exitFailure)
	}
}

// TestServiceReserveWithSlashThirtytwoAliasSanitizesCanonicalAddress verifies
// that when a /32 address alias is provided as a CLI arg, both the raw spelling
// and its canonical bare form are registered under the same kind (kindIP) so they
// map to the same sanitization placeholder. This guards against pseudonym-domain
// split where an error message embeds the canonical form but the sweep only
// registered the /32 spelling.
//
// Mechanism note (round 3 characterization): this test's failure is a
// ServiceAddressCollisionError, which sanitizeErrorIfNeeded redacts through
// spliceCollisionSegments/sanitizedQuotedSegment -- a direct sanitizer.Value()
// call on the byte range serviceAddressCollisionErrorPattern's regex already
// located inside registry.ServiceAddressCollisionError.Error() (whose Address
// field is already canonical; see internal/registry/serviceaddress.go). That
// path never consults registerSweepValue's pre-registered inventory, so it
// passes identically with or without the RunE canonical-registration fix
// under test in TestServiceCanonicalAddressRegistrationGuardsErrorSweep
// below -- it does NOT guard that fix. It stays here as a regression guard
// for the collision-splice mechanism itself.
func TestServiceReserveWithSlashThirtytwoAliasSanitizesCanonicalAddress(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Seed a reservation so the next attempt at the same address triggers a collision.
	if _, _, err := runService(t, dir, "reserve", "100.100.100.8", "dashboard"); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}

	// Try to reserve the same address with the /32 spelling under sanitization.
	// The collision error will embed the canonical bare address in its text.
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", "100.100.100.8/32", "metrics",
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)

	if code != exitFailure {
		t.Fatalf("collision exit = %d, want %d (stderr: %s)", code, exitFailure, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("collision stdout = %q, want empty", stdout.String())
	}

	// The bare canonical address should not appear in the error output.
	canonicalAddress := "100.100.100.8"
	if strings.Contains(stderr.String(), canonicalAddress) {
		t.Errorf("sanitized error output leaked canonical address %q: %s", canonicalAddress, stderr.String())
	}

	// The error should contain the message frame structure (showing it was properly sanitized).
	if !strings.Contains(stderr.String(), "service address ") ||
		!strings.Contains(stderr.String(), " is already reserved for ") ||
		!strings.Contains(stderr.String(), ", not ") {
		t.Errorf("sanitized error lost message frame: %s", stderr.String())
	}

	// Both /32 spelling and canonical bare form should have been registered,
	// so the placeholder kinds should match.
	if !strings.Contains(stderr.String(), "ip-") {
		t.Errorf("sanitized error missing 'ip-' placeholder kind: %s", stderr.String())
	}
}

// TestServiceReserveBareSanitizationAndServiceNameAliases verifies that both
// bare and svc:-prefixed service names map to the same sanitization placeholder,
// and that both bare and /32-suffixed address spellings also map to the same
// placeholder, ensuring pseudonym stability across all supported input variations.
//
// Mechanism note (round 3 characterization): every assertion here reads a
// *successful* reserve's JSON envelope. sanitizeOutput (output.go) sanitizes
// the envelope's Address/Service fields with a direct sanitizer.Value() call
// on the already-canonical result the registry returned -- this is the
// envelope Value() path, entirely independent of registerSweepValue's error-
// path pre-registration. It passes identically with or without the RunE/
// registerKnownIdentityInventory canonical-registration fix under test in
// TestServiceCanonicalAddressRegistrationGuardsErrorSweep below -- it does
// NOT guard that fix. It stays here as a regression guard for the success
// path's pseudonym stability.
func TestServiceReserveBareSanitizationAndServiceNameAliases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Reserve with bare service name (no svc: prefix) and bare address.
	stdout, stderr, err := runService(t, dir, "reserve", "100.100.100.8", "dashboard",
		"--format", formatJSON)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("reserve stdout/stderr = %q/%q, want parse-clean JSON", stdout, stderr)
	}

	var result outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}

	// Verify the envelope fields are in their canonical forms.
	wantAddress := "100.100.100.8"
	wantService := "svc:dashboard"
	if result.Address != wantAddress || result.Service != wantService {
		t.Errorf("envelope fields = Address:%q Service:%q, want %q/%q",
			result.Address, result.Service, wantAddress, wantService)
	}

	sanitizedBareResult := reserveWithSanitization(t, dir, "100.100.100.8", "dashboard")
	sanitizedSlash32Result := reserveWithSanitization(t, dir, "100.100.100.8/32", "svc:dashboard")

	// Verify the placeholder values match between both invocations.
	if sanitizedBareResult.Address != sanitizedSlash32Result.Address {
		t.Errorf("address placeholder mismatch: bare=%q /32=%q",
			sanitizedBareResult.Address, sanitizedSlash32Result.Address)
	}
	if sanitizedBareResult.Service != sanitizedSlash32Result.Service {
		t.Errorf("service placeholder mismatch: bare=%q svc:=%q",
			sanitizedBareResult.Service, sanitizedSlash32Result.Service)
	}
}

// reserveWithSanitization performs a sanitized service reserve and returns the result.
func reserveWithSanitization(t *testing.T, dir, address, service string) *outputEnvelope {
	t.Helper()
	var buf bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", address, service,
		"--registry-dir", dir, "--format", formatJSON, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &buf, nil, nil)

	if code != exitOK {
		t.Fatalf("sanitized reserve exit = %d, want %d (output: %s)", code, exitOK, buf.String())
	}

	var result outputEnvelope
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return &result
}

// assertCanonicalAddressRegisteredInSweep is the shared anti-vacuity guard
// for TestServiceCanonicalAddressRegistrationGuardsErrorSweep's rows: (a) the
// bare canonical address must not survive sanitization (a leak check that a
// vacuous test -- one where the identity never appeared in the message at
// all -- would trivially satisfy), (b) the exact placeholder
// sanitizer.Value(kindIP, canonical) would produce must be present (the
// positive counterpart that rules out (a) passing vacuously), and (c) every
// frame substring must still be present, proving the fault/decode message
// was not corrupted along the way.
func assertCanonicalAddressRegisteredInSweep(t *testing.T, stderrText, canonical string, frame ...string) {
	t.Helper()
	if strings.Contains(stderrText, canonical) {
		t.Errorf("stderr leaks canonical address %q: %s", canonical, stderrText)
	}
	placeholder := placeholderFor(t, kindIP, canonical)
	if !strings.Contains(stderrText, placeholder) {
		t.Errorf("stderr = %q, want the canonical-address placeholder %q", stderrText, placeholder)
	}
	for _, part := range frame {
		if !strings.Contains(stderrText, part) {
			t.Errorf("stderr = %q, want frame text %q intact", stderrText, part)
		}
	}
}

// TestServiceCanonicalAddressRegistrationGuardsErrorSweep is the round-3
// characterization suite: every prior round's injection aborts before the
// registry code that canonicalizes the address runs (see the mechanism notes
// on TestServiceReserveWithSlashThirtytwoAliasSanitizesCanonicalAddress and
// TestServiceReserveBareSanitizationAndServiceNameAliases above, and on
// TestServiceReserveWriteFailureCanonicalAddressOmitsBareIP in
// error_sanitize_test.go for the write-failure shape). The fix is observable
// only in an error produced AFTER registry.NormalizeServiceAddress succeeds:
// withRegistryLock's write frames (see error_sanitize_test.go's T3) and
// canonicalizeServiceAddresses' decode frames (T1/T2 below).
func TestServiceCanonicalAddressRegistrationGuardsErrorSweep(t *testing.T) {
	t.Parallel()
	t.Run("T1_RunE_registers_release_arg_canonical_form", testServiceCanonicalT1ReleaseArg)
	t.Run("T2_registerKnownIdentityInventory_registers_disk_key_canonical_form", testServiceCanonicalT2DiskKey)
	t.Run("control_bare_spelling_is_already_canonical", testServiceCanonicalControlBareSpelling)
	t.Run("control_collision_redacted_by_splice_not_sweep", testServiceCanonicalControlCollisionSplice)
}

// testServiceCanonicalT1ReleaseArg is T1: RunE (service.go) registers the CLI
// address arg's canonical form under kindIP. The fixture's "not-an-ip" key
// makes canonicalizeServiceAddresses fail to build a ServiceAddresses view at
// all, so releaseRegistryEntry fails closed with "cannot determine whether
// ... is reserved" instead of a confident absent/present answer -- and that
// frame embeds the *canonical* bare address computed from the /32 CLI arg.
// Registering "100.100.100.9/32" alone does not redact the bare
// "100.100.100.9" that ends up in this frame (the reverse direction --
// registering the bare form redacting a /32 occurrence -- does work, which is
// why the /32 arg is the dangerous input).
func testServiceCanonicalT1ReleaseArg(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	if err := os.WriteFile(path, []byte(`{"serviceAddresses":{"not-an-ip":"svc:x"}}`), 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "release", "100.100.100.9/32",
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, exitUsage, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	assertCanonicalAddressRegisteredInSweep(t, stderr.String(), "100.100.100.9",
		"cannot determine whether service address ", " is reserved:")
}

// testServiceCanonicalT2DiskKey is T2: registerKnownIdentityInventory
// (main.go) registers the raw disk key's canonical form under kindIP,
// isolated from RunE because "list" takes no address arg -- only the
// inventory built from the registry file's own raw keys can supply the
// canonical form here. The fixture's malformed "svc:svc:web" service value
// makes canonicalizeServiceAddresses fail after computing the canonical
// address but before validating the service name, so the error frame still
// names the canonical address.
func testServiceCanonicalT2DiskKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	if err := os.WriteFile(
		path, []byte(`{"serviceAddresses":{"100.100.100.9/32":"svc:svc:web"}}`), 0o600,
	); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, commandList,
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, exitUsage, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	assertCanonicalAddressRegisteredInSweep(t, stderr.String(), "100.100.100.9",
		"service address ", " (key ", "): service name must contain exactly one")
}

// testServiceCanonicalControlBareSpelling is a control row: the bare spelling
// is already its own canonical form, so this passes with or without the fix
// -- it does not exercise registerSweepValue's canonical-form branch at all.
// It exists to prove this suite's fixture and assertions are otherwise sound,
// isolating T1's failure (when reverted) to the /32-vs-bare registration gap
// specifically.
func testServiceCanonicalControlBareSpelling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	if err := os.WriteFile(path, []byte(`{"serviceAddresses":{"not-an-ip":"svc:x"}}`), 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "release", "100.100.100.9",
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, exitUsage, stderr.String())
	}
	assertCanonicalAddressRegisteredInSweep(t, stderr.String(), "100.100.100.9",
		"cannot determine whether service address ", " is reserved:")
}

// testServiceCanonicalControlCollisionSplice is a control row: a collision
// error is redacted by spliceCollisionSegments's direct sanitizer.Value()
// call on the regex-captured bytes (main.go), not by the free-text sweep the
// registration fix under test feeds -- see the mechanism note on
// TestServiceReserveWithSlashThirtytwoAliasSanitizesCanonicalAddress above.
// This passes with or without the fix, proving this suite's other rows are
// not just accidentally green from an over-broad assertion.
func testServiceCanonicalControlCollisionSplice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, _, err := runService(t, dir, "reserve", "100.100.100.8", "dashboard"); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandService, "reserve", "100.100.100.8/32", "metrics",
		"--registry-dir", dir, "--sanitize", string(sanitize.Identity),
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitFailure {
		t.Fatalf("collision exit = %d, want %d (stderr: %s)", code, exitFailure, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	assertCanonicalAddressRegisteredInSweep(t, stderr.String(), "100.100.100.8",
		"service address ", " is already reserved for ", ", not ")
}
