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

	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

// runSubnet drives the command through Cobra and the real generated Goa
// loopback HTTP transport, matching runSiteID's end-to-end boundary.
func runSubnet(t *testing.T, registryDir string, args ...string) (string, string, error) {
	t.Helper()
	state := &commandState{}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{commandSubnet}, append(args, "--registry-dir", registryDir)...))
	err := root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestSubnetReserveWithMetadataFlagsThenList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, errOut, err := runSubnet(
		t, dir, "reserve", "10.0.0.0/24", "hq-vpc",
		"--owner", "carol", "--ticket", "OPS-3",
	); err != nil || errOut != "" {
		t.Fatalf("reserve: err=%v stderr=%q", err, errOut)
	}

	out, _, err := runSubnet(t, dir, "list", "--format", formatJSON)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listed outputEnvelope
	if err = json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode list JSON: %v", err)
	}
	if listed.SubnetPools == nil || len(*listed.SubnetPools) != 1 {
		t.Fatalf("list JSON = %+v, want one entry", listed)
	}
	got := (*listed.SubnetPools)[0]
	if got.Owner != "carol" || got.Ticket != "OPS-3" || got.ReservedAt == "" {
		t.Errorf("list JSON entry = %+v, want Owner=carol Ticket=OPS-3 and a non-empty ReservedAt", got)
	}
}

func TestSubnetReserveListReleaseHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	out, errOut, err := runSubnet(t, dir, "reserve", "10.0.0.0/24", "hq-vpc")
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !strings.Contains(out, "reserved subnet CIDR 10.0.0.0/24") {
		t.Errorf("reserve stdout = %q, want confirmation", out)
	}
	if errOut != "" {
		t.Errorf("reserve stderr = %q, want empty", errOut)
	}

	out, errOut, err = runSubnet(t, dir, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "10.0.0.0/24\thq-vpc") {
		t.Errorf("list stdout = %q, want the reservation", out)
	}
	if errOut != "" {
		t.Errorf("list stderr = %q, want empty", errOut)
	}

	out, errOut, err = runSubnet(t, dir, "release", "10.0.0.0/24")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !strings.Contains(out, "released subnet CIDR 10.0.0.0/24") {
		t.Errorf("release stdout = %q, want confirmation", out)
	}
	if errOut != "" {
		t.Errorf("release stderr = %q, want empty", errOut)
	}
}

func TestSubnetJSONEnvelopes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	stdout, stderr, err := runSubnet(
		t, dir, "reserve", "10.0.0.0/24", "hq-vpc", "--format", formatJSON,
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if stderr != "" {
		t.Errorf("reserve stderr = %q, want empty", stderr)
	}
	var reserved outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &reserved); err != nil {
		t.Fatalf("decode reserve JSON: %v", err)
	}
	if reserved.SchemaVersion != outputSchemaVersion || reserved.Command != commandSubnetReserve ||
		reserved.Status != statusOK || reserved.Changed == nil || !*reserved.Changed ||
		reserved.CIDR != "10.0.0.0/24" || reserved.Site != "hq-vpc" {
		t.Errorf("reserve envelope = %+v, want changed hq-vpc reservation", reserved)
	}

	stdout, stderr, err = runSubnet(t, dir, "list", "--format", formatJSON)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if stderr != "" {
		t.Errorf("list stderr = %q, want empty", stderr)
	}
	var listed outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("decode list JSON: %v", err)
	}
	if listed.Command != commandSubnetList || listed.SubnetPools == nil || len(*listed.SubnetPools) != 1 ||
		(*listed.SubnetPools)[0] != (subnetPoolOutput{CIDR: "10.0.0.0/24", Site: "hq-vpc"}) {
		t.Errorf("list envelope = %+v, want one subnet-pool reservation", listed)
	}
}

func TestSubnetReserveCommandPreservesHandWrittenComment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "local.ipam.hujson")
	content := "{\n\t\"subnetPools\": {\n" +
		"\t\t// reserved for the HQ migration, do not release before Q1\n" +
		"\t\t\"10.0.0.0/24\": \"hq-vpc\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	if _, _, err := runSubnet(t, dir, "reserve", "10.0.1.0/24", "hq-vpc"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	want := "// reserved for the HQ migration, do not release before Q1"
	if !strings.Contains(string(got), want) {
		t.Errorf("registry after command-layer edit = %q, want it to contain %q", got, want)
	}
}

func TestSubnetExitCodeDistinguishesCollisionFromUsageError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := runSubnet(t, dir, "reserve", "10.0.0.0/16", "hq-vpc"); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	_, _, collisionErr := runSubnet(t, dir, "reserve", "10.0.1.0/24", "branch-office")
	var svcErr *goa.ServiceError
	if !errors.As(collisionErr, &svcErr) || svcErr.Name != collisionErrorName {
		t.Fatalf("overlap error = %v, want a %q *goa.ServiceError", collisionErr, collisionErrorName)
	}
	if got := exitCodeFor(collisionErr); got != exitFailure {
		t.Errorf("exitCodeFor(collision) = %d, want %d", got, exitFailure)
	}

	_, _, usageErr := runSubnet(t, dir, "reserve", "10.0.1.0/24")
	if usageErr == nil {
		t.Fatal("reserve with one arg: error = nil, want a usage error")
	}
	if got := exitCodeFor(usageErr); got != exitUsage {
		t.Errorf("exitCodeFor(usage error) = %d, want %d", got, exitUsage)
	}
}

func TestSubnetReserveRejectsNonCanonicalCIDR(t *testing.T) {
	t.Parallel()
	_, _, err := runSubnet(t, t.TempDir(), "reserve", "10.0.0.1/24", "hq-vpc")
	if err == nil {
		t.Fatal("reserve non-canonical CIDR: error = nil, want an error")
	}
	errText := err.Error()
	// A non-canonical CIDR is a designed invalid_input error (HTTP 400), so
	// the diagnostic arrives clean rather than inside the generic 500 fault
	// envelope the transport used to wrap it in.
	if strings.Contains(errText, "invalid response code 500") {
		t.Errorf("error = %v, want a designed invalid_input error, not a 500 fault envelope", errText)
	}
	if !strings.Contains(errText, "not canonical") {
		t.Errorf("error = %v, want the 'not canonical' diagnostic", errText)
	}
	if !strings.Contains(errText, "10.0.0.0/24") {
		t.Errorf("error = %v, want the canonical CIDR suggestion", errText)
	}
}

func TestSubnetListSanitizesCIDRsAndSites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, _, err := runSubnet(t, dir, "reserve", "10.0.0.0/24", "hq-vpc"); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSubnet, commandList, "--registry-dir", dir,
		"--format", formatJSON, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitOK {
		t.Fatalf("subnet list exit = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if strings.Contains(stdout.String(), "10.0.0.0/24") || strings.Contains(stdout.String(), "hq-vpc") {
		t.Errorf("sanitized subnet list leaked CIDR or site: %s", stdout.String())
	}
	var envelope outputEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode sanitized JSON: %v", err)
	}
	if envelope.Sanitization == nil || envelope.SubnetPools == nil || len(*envelope.SubnetPools) != 1 {
		t.Errorf("sanitized subnet envelope = %+v", envelope)
	}
}

func TestSubnetCollisionSanitizesCIDRsAndSites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, _, err := runSubnet(t, dir, "reserve", "10.0.0.0/16", "hq-vpc"); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSubnet, "reserve", "10.0.1.0/24", "branch-office",
		"--registry-dir", dir, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitFailure {
		t.Fatalf("collision exit = %d, want %d (stderr: %s)", code, exitFailure, stderr.String())
	}
	for _, secret := range []string{"10.0.0.0/16", "10.0.1.0/24", "hq-vpc", "branch-office"} {
		if strings.Contains(stderr.String(), secret) {
			t.Errorf("sanitized collision stderr leaks %q: %s", secret, stderr.String())
		}
	}
}

func TestSubnetOverlapCollisionErrorFormatMatchesAnchoredSanitizerPattern(t *testing.T) {
	t.Parallel()
	err := (&registry.SubnetPoolCollisionError{
		CIDR:         "10.0.1.0/24",
		ExistingCIDR: "10.0.0.0/16",
		ExistingSite: "hq-vpc",
		WantedSite:   "branch-office",
	}).Error()
	match := subnetPoolCollisionErrorPattern.FindStringSubmatch(err)
	if match == nil {
		t.Fatalf("subnetPoolCollisionErrorPattern does not match %q", err)
	}
	want := []string{"10.0.1.0/24", "10.0.0.0/16", `"hq-vpc"`, `"branch-office"`}
	for index, value := range want {
		if match[index+1] != value {
			t.Errorf("capture %d = %q, want %q", index+1, match[index+1], value)
		}
	}
}

// TestSubnetReserveSiteArgRegistersForErrorSweep covers issue 1: unlike
// site-id reserve and service reserve, newSubnetLeafCommand never registered
// "subnet reserve"'s <site-name> argument for sanitizeErrorIfNeeded's
// free-text sweep, so a control character in the site name reached stderr via
// registry.NormalizeSiteName's %q-escaped rejection message unredacted.
func TestSubnetReserveSiteArgRegistersForErrorSweep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const site = "sec\tret-site"
	const siteEscaped = `sec\tret-site`

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandSubnet, "reserve", "10.0.0.0/24", site,
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
}

func TestSubnetExactCollisionErrorFormatMatchesAnchoredSanitizerPattern(t *testing.T) {
	t.Parallel()
	err := (&registry.SubnetPoolCollisionError{
		CIDR:         "10.0.0.0/16",
		ExistingCIDR: "10.0.0.0/16",
		ExistingSite: "hq-vpc",
		WantedSite:   "branch-office",
	}).Error()
	match := subnetPoolExactCollisionErrorPattern.FindStringSubmatch(err)
	if match == nil {
		t.Fatalf("subnetPoolExactCollisionErrorPattern does not match %q", err)
	}
	want := []string{"10.0.0.0/16", `"hq-vpc"`, `"branch-office"`}
	for index, value := range want {
		if match[index+1] != value {
			t.Errorf("capture %d = %q, want %q", index+1, match[index+1], value)
		}
	}
}
