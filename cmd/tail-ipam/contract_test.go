package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/rshade/tailscale-utils/policycore/sanitize"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
)

// updateGoldens regenerates the AX contract fixtures. Run `go test . -update`
// and read the diff: -update blesses a wrong document as happily as a right
// one.
//
//nolint:gochecknoglobals // a test flag must be package-level to register with the flag package
var updateGoldens = flag.Bool("update", false, "update golden files")

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGoldens {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Logf("updated %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed\n got: %s\nwant: %s", name, got, want)
	}
}

func emptyEnvironment(string) string { return "" }

func TestSchemaGoldenAndCatalog(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := runCLI(
		context.Background(), []string{"-schema"}, emptyEnvironment,
		&stdout, &stderr, nil,
	)
	if code != exitOK {
		t.Fatalf("-schema exit = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("-schema stderr = %q, want empty", stderr.String())
	}
	assertGolden(t, "schema.golden.json", stdout.Bytes())

	var document struct {
		SchemaVersion string           `json:"schema_version"`
		Flags         []schemaFlag     `json:"flags"`
		Commands      []schemaCommand  `json:"commands"`
		ExitCodes     []exitCodeSchema `json:"exit_codes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	assertSchemaCatalog(t, document)
}

func TestSchemaRejectsInvalidFlagsInsteadOfIgnoringThem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{name: "invalid format", args: []string{"-schema", "-format", "bogus"}},
		{name: "invalid sanitize", args: []string{"-schema", "-sanitize", "bogus"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := runCLI(context.Background(), testCase.args, emptyEnvironment, &stdout, &stderr, nil)
			if code != exitUsage {
				t.Errorf("exit = %d, want %d (stdout: %s, stderr: %s)",
					code, exitUsage, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty: an invalid flag must not still print the schema", stdout.String())
			}
		})
	}
}

func TestSchemaWithValidSanitizeFlagStaysSilentOnStderr(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := runCLI(
		context.Background(), []string{"-schema", "-sanitize", "identity"}, emptyEnvironment,
		&stdout, &stderr, nil,
	)
	if code != exitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty: -schema never sanitizes output, so it must not announce that it does",
			stderr.String())
	}
}

func assertSchemaCatalog(t *testing.T, document struct {
	SchemaVersion string           `json:"schema_version"`
	Flags         []schemaFlag     `json:"flags"`
	Commands      []schemaCommand  `json:"commands"`
	ExitCodes     []exitCodeSchema `json:"exit_codes"`
}) {
	t.Helper()
	if document.SchemaVersion != toolSchemaVersion {
		t.Errorf("schema_version = %q, want %q", document.SchemaVersion, toolSchemaVersion)
	}
	wantFlags := []string{"format", "sanitize", "schema", "verbose", "version"}
	for _, want := range wantFlags {
		if !slices.ContainsFunc(document.Flags, func(flag schemaFlag) bool { return flag.Name == want }) {
			t.Errorf("schema flags omit %q", want)
		}
	}
	wantCommands := []string{
		commandDevices, commandDevicesList, commandSiteID,
		commandSiteIDList, commandSiteIDRelease, commandSiteIDReserve,
	}
	for _, want := range wantCommands {
		if !slices.ContainsFunc(document.Commands, func(command schemaCommand) bool { return command.Name == want }) {
			t.Errorf("schema commands omit %q", want)
		}
	}
	for _, command := range document.Commands {
		if !strings.HasPrefix(command.Name, commandSiteID+" ") {
			continue
		}
		for _, want := range []string{"registry-dir", "tailnet"} {
			if !slices.ContainsFunc(command.Flags, func(flag schemaFlag) bool { return flag.Name == want }) {
				t.Errorf("schema command %q omits flag %q", command.Name, want)
			}
		}
	}
	if len(document.ExitCodes) != 3 {
		t.Fatalf("schema exit_codes = %+v, want three entries", document.ExitCodes)
	}
	for expectedCode := exitOK; expectedCode <= exitUsage; expectedCode++ {
		if !slices.ContainsFunc(document.ExitCodes, func(entry exitCodeSchema) bool {
			return entry.Code == expectedCode
		}) {
			t.Errorf("schema exit_codes omit %d", expectedCode)
		}
	}
}

func TestSiteIDJSONEnvelopeGolden(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSiteID(
		t, t.TempDir(), "reserve", "007", "hq-vpc", "--format", formatJSON,
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if stderr != "" {
		t.Errorf("reserve stderr = %q, want empty", stderr)
	}
	assertGolden(t, "site_id_reserve.golden.json", []byte(stdout))
}

func TestSiteIDListAndReleaseJSONEnvelopes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if _, _, err := runSiteID(t, dir, "reserve", "2", "branch-office"); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}
	stdout, stderr, err := runSiteID(t, dir, "list", "--format", formatJSON)
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
	if listed.SchemaVersion != outputSchemaVersion || listed.Command != commandSiteIDList ||
		listed.Status != statusOK || listed.SiteIDs == nil || len(*listed.SiteIDs) != 1 ||
		(*listed.SiteIDs)[0] != (siteIDOutput{ID: "2", Site: "branch-office"}) {
		t.Errorf("list envelope = %+v, want one branch-office reservation", listed)
	}

	stdout, stderr, err = runSiteID(t, dir, "release", "2", "--format", formatJSON)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if stderr != "" {
		t.Errorf("release stderr = %q, want empty", stderr)
	}
	var released outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &released); err != nil {
		t.Fatalf("decode release JSON: %v", err)
	}
	if released.Command != commandSiteIDRelease || released.Status != statusOK ||
		released.Changed == nil || !*released.Changed || released.ID != "2" {
		t.Errorf("release envelope = %+v, want a changed release for ID 2", released)
	}
}

func TestDevicesListSanitizationAndVerboseChannels(t *testing.T) {
	t.Parallel()

	const (
		tailnet = "corp.example"
		name    = "router.corp.example"
		address = "100.64.0.7"
		route   = "10.20.0.0/16"
	)
	getenv := func(name string) string {
		switch name {
		case sanitize.LevelEnv:
			return string(sanitize.Identity)
		case sanitize.KeyEnv:
			return strings.Repeat("0", 64)
		case "TAILSCALE_TAILNET":
			return tailnet
		default:
			return ""
		}
	}
	listDevices := func(_ context.Context, logger diagnostics.Logger) ([]deviceRecord, error) {
		diagnostics.Debug(logger, "device fixture collected", "count", 1)
		return []deviceRecord{{
			Name: name, Addresses: []string{address}, AdvertisedRoutes: []string{route},
		}}, nil
	}

	for _, format := range []string{formatHuman, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			assertSanitizedDevicesFormat(
				t, format, getenv, listDevices, []string{tailnet, name, address, route},
			)
		})
	}
}

func assertSanitizedDevicesFormat(
	t *testing.T,
	format string,
	getenv func(string) string,
	listDevices devicesListFunc,
	leaks []string,
) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(
		context.Background(),
		[]string{commandDevices, commandList, "-format", format, "-sanitize", "off", "-verbose"},
		getenv, &stdout, &stderr, listDevices,
	)
	if code != exitOK {
		t.Fatalf("devices list exit = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	for _, leaked := range leaks {
		if strings.Contains(stdout.String(), leaked) || strings.Contains(stderr.String(), leaked) {
			t.Errorf("%s output leaked %q\nstdout: %s\nstderr: %s",
				format, leaked, stdout.String(), stderr.String())
		}
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") ||
		!strings.Contains(stderr.String(), "device fixture collected") {
		t.Errorf("verbose stderr = %q, want structured debug collection detail", stderr.String())
	}
	if !strings.Contains(stderr.String(), "overrides --sanitize=off") {
		t.Errorf("stderr = %q, want the environment-over-flag notice", stderr.String())
	}
	if format != formatJSON {
		return
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatalf("verbose JSON stdout is not parse-clean: %s", stdout.String())
	}
	var envelope outputEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Sanitization == nil || envelope.Sanitization.Level != sanitize.Identity ||
		envelope.Devices == nil || len(*envelope.Devices) != 1 {
		t.Errorf("sanitized devices envelope = %+v", envelope)
	}
}

func TestSiteIDVerboseRegistryDiagnosticsStayOffJSONStdout(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runSiteID(
		t, t.TempDir(), "reserve", "1", "hq-vpc", "--format", formatJSON, "--verbose",
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("verbose JSON stdout is not parse-clean: %s", stdout)
	}
	for _, event := range []string{
		"starting loopback registry transport",
		"registry lock acquired",
		"registry created",
	} {
		if !strings.Contains(stderr, "level=DEBUG") || !strings.Contains(stderr, event) {
			t.Errorf("verbose stderr = %q, want structured event %q", stderr, event)
		}
		if strings.Contains(stdout, event) {
			t.Errorf("verbose JSON stdout contains diagnostic %q: %s", event, stdout)
		}
	}
}

func TestInvalidSanitizeKeyRefusesBeforeRegistryMutation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	getenv := func(name string) string {
		switch name {
		case sanitize.LevelEnv:
			return string(sanitize.Identity)
		case sanitize.KeyEnv:
			return "not-a-valid-key"
		default:
			return ""
		}
	}
	var stdout, stderr bytes.Buffer
	code := runCLI(
		context.Background(),
		[]string{commandSiteID, "reserve", "1", "hq-vpc", "--registry-dir", dir},
		getenv, &stdout, &stderr, nil,
	)
	if code != exitUsage {
		t.Fatalf("invalid sanitize key exit = %d, want %d (stderr: %s)", code, exitUsage, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("invalid sanitize key stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, localTailnet+".ipam.hujson")); !os.IsNotExist(err) {
		t.Errorf("registry exists after pre-operation sanitization failure: %v", err)
	}
}

func TestSanitizedOutputTypesRequireExplicitFieldDecisions(t *testing.T) {
	t.Parallel()

	if missing := sanitize.UnhandledFields[outputEnvelope](
		"SchemaVersion", "Tool", "Sanitization", "Command", "Status", "Message", "Tailnet",
		"Changed", "ID", "Site", "SiteIDs", "Devices",
	); len(missing) != 0 {
		t.Errorf("outputEnvelope fields without an explicit sanitization decision: %v", missing)
	}
	if missing := sanitize.UnhandledFields[siteIDOutput]("ID", "Site"); len(missing) != 0 {
		t.Errorf("siteIDOutput fields without an explicit sanitization decision: %v", missing)
	}
	if missing := sanitize.UnhandledFields[deviceRecord](
		"Name", "Addresses", "AdvertisedRoutes",
	); len(missing) != 0 {
		t.Errorf("deviceRecord fields without an explicit sanitization decision: %v", missing)
	}
}

func TestBothDashSpellingsAndAuthoredOutputContracts(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{name: "single-dash schema", args: []string{"-schema"}},
		{name: "double-dash schema", args: []string{"--schema"}},
		{name: "single-dash version", args: []string{"-version"}},
		{name: "double-dash version", args: []string{"--version"}},
		{name: "single-dash help", args: []string{"-help"}},
		{name: "double-dash help", args: []string{"--help"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := runCLI(
				context.Background(), testCase.args, emptyEnvironment,
				&stdout, &stderr, nil,
			)
			if code != exitOK {
				t.Fatalf("runCLI(%q) = %d, want %d (stderr: %s)",
					testCase.args, code, exitOK, stderr.String())
			}
			if stdout.Len() == 0 {
				t.Errorf("runCLI(%q) wrote nothing to stdout", testCase.args)
			}
			for _, value := range stdout.Bytes() {
				if value >= 0x80 {
					t.Fatalf("runCLI(%q) emitted non-ASCII authored text: %q", testCase.args, stdout.String())
				}
			}
		})
	}
}

func TestBuildVersionIncludesShortDirtyRevision(t *testing.T) {
	t.Parallel()

	got := buildVersionWithSettings("v1.2.3", []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef"},
		{Key: "vcs.modified", Value: "true"},
	})
	want := "tail-ipam v1.2.3 (0123456789ab+dirty)"
	if got != want {
		t.Errorf("buildVersionWithSettings = %q, want %q", got, want)
	}
}

// TestSanitizeOutputMessageSweepCoversRegisteredIdentities is the regression
// guard for the dual-walk identity registration gap: registerOutputIdentities
// and sanitizeOutput's own per-field decisions must stay in sync. This test
// exercises sanitizeOutput on an envelope whose Message field embeds an
// identity value that also appears in a structured field (Site), and verifies
// both are replaced with the same placeholder. If a future change adds an
// identity-bearing field to sanitizeOutput without a matching
// registerOutputIdentities entry, or vice versa, this test will need to be
// extended to catch it — and more importantly, a maintainer reading this test
// learns the two lists must be kept in sync.
func TestSanitizeOutputMessageSweepCoversRegisteredIdentities(t *testing.T) {
	t.Parallel()

	output, err := sanitize.New(sanitize.Options{
		Level: sanitize.Identity, Tool: toolName, Tailnet: "example.ts.net",
		KeyHex: strings.Repeat("ab", 32),
	})
	if err != nil {
		t.Fatal(err)
	}

	siteName := "hq-vpc"
	input := newOutput(commandSiteIDReserve, statusOK,
		fmt.Sprintf("reserved site ID 7 for %q", siteName), "example.ts.net")
	input.Site = siteName
	input.Changed = new(bool)
	*input.Changed = true
	input.ID = "7"

	got := sanitizeOutput(input, output)
	rendered, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(rendered, []byte(siteName)) {
		t.Errorf("sanitized output leaked original site name %q: %s", siteName, rendered)
	}

	if !strings.Contains(got.Message, got.Site) {
		t.Errorf("site placeholder is inconsistent: Message=%q, Site=%q", got.Message, got.Site)
	}

	if !strings.HasPrefix(got.Site, "site-") {
		t.Errorf("Site field not properly sanitized: got %q, want prefix 'site-'", got.Site)
	}
}
