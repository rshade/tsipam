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

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

// runUDPPort traverses Cobra, the generated Goa client, and the ephemeral
// loopback HTTP server, matching the path used by a live invocation.
func runUDPPort(t *testing.T, registryDir string, args ...string) (string, string, error) {
	t.Helper()
	state := &commandState{}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{commandPort}, append(args, "--registry-dir", registryDir)...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestUDPPortReserveRepeatAndCrossSiteReuse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stdout, stderr, err := runUDPPort(
		t, dir, "reserve", "041642", " branch-fw01 ", "--site", " branch ",
	)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if stderr != "" || stdout != "reserved UDP port 41642 in site \"branch\" for \"branch-fw01\"\n" {
		t.Errorf("reserve stdout/stderr = %q/%q, want canonical confirmation/empty", stdout, stderr)
	}

	stdout, stderr, err = runUDPPort(
		t, dir, "reserve", "41642", "branch-fw01", "--site", "branch", "--format", formatJSON,
	)
	if err != nil {
		t.Fatalf("repeat reserve: %v", err)
	}
	if stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("repeat reserve stdout/stderr = %q/%q, want parse-clean JSON/empty", stdout, stderr)
	}
	var repeated outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.Command != commandPortReserve || repeated.Status != statusNoChanges ||
		repeated.Changed == nil || *repeated.Changed || repeated.Site != "branch" ||
		repeated.Port != "41642" || repeated.Device != "branch-fw01" {
		t.Errorf("repeat reserve envelope = %+v, want canonical no_changes result", repeated)
	}

	if _, _, err = runUDPPort(t, dir, "reserve", "41642", "hq-fw01", "--site", "hq"); err != nil {
		t.Fatalf("cross-site reserve: %v", err)
	}
}

func TestUDPPortListSortsAndFiltersInHumanAndJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	reservations := []struct{ site, port, device string }{
		{site: "hq", port: "41650", device: "hq-c"},
		{site: "branch", port: "41642", device: "branch-b"},
		{site: "hq", port: "1024", device: "hq-a"},
		{site: "branch", port: "10240", device: "branch-a"},
	}
	for _, reservation := range reservations {
		if _, _, err := runUDPPort(
			t, dir, "reserve", reservation.port, reservation.device, "--site", reservation.site,
		); err != nil {
			t.Fatalf("reserve %+v: %v", reservation, err)
		}
	}

	stdout, stderr, err := runUDPPort(t, dir, commandList)
	if err != nil {
		t.Fatalf("human list: %v", err)
	}
	wantHuman := "branch\t10240\tbranch-a\n" +
		"branch\t41642\tbranch-b\n" +
		"hq\t1024\thq-a\n" +
		"hq\t41650\thq-c\n"
	if stderr != "" || stdout != wantHuman {
		t.Errorf("human list stdout/stderr = %q/%q, want %q/empty", stdout, stderr, wantHuman)
	}

	stdout, stderr, err = runUDPPort(t, dir, commandList, "--site", " hq ", "--format", formatJSON)
	if err != nil {
		t.Fatalf("filtered JSON list: %v", err)
	}
	if stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("filtered list stdout/stderr = %q/%q, want parse-clean JSON/empty", stdout, stderr)
	}
	var listed outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Command != commandPortList || listed.UDPPorts == nil || len(*listed.UDPPorts) != 2 {
		t.Fatalf("filtered list envelope = %+v, want two UDP ports", listed)
	}
	if (*listed.UDPPorts)[0].Port != "1024" || (*listed.UDPPorts)[1].Port != "41650" {
		t.Errorf("filtered list = %+v, want numeric port order", *listed.UDPPorts)
	}

	stdout, stderr, err = runUDPPort(t, dir, commandList, "--site", "unknown", "--format", formatJSON)
	if err != nil {
		t.Fatalf("unknown-site JSON list: %v", err)
	}
	if stderr != "" || !strings.Contains(stdout, `"udp_ports": []`) {
		t.Errorf("unknown-site list stdout/stderr = %q/%q, want udp_ports:[]/empty", stdout, stderr)
	}
}

func TestUDPPortReleaseAndAbsentRelease(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, _, err := runUDPPort(t, dir, "reserve", "41642", "branch-fw01", "--site", "branch"); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}

	stdout, stderr, err := runUDPPort(t, dir, "release", "041642", "--site", " branch ")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if stderr != "" || stdout != "released UDP port 41642 from site \"branch\"\n" {
		t.Errorf("release stdout/stderr = %q/%q, want canonical confirmation/empty", stdout, stderr)
	}

	stdout, stderr, err = runUDPPort(
		t, dir, "release", "41642", "--site", "branch", "--format", formatJSON,
	)
	if err != nil {
		t.Fatalf("absent release: %v", err)
	}
	if stderr != "" || !json.Valid([]byte(stdout)) {
		t.Fatalf("absent release stdout/stderr = %q/%q, want parse-clean JSON/empty", stdout, stderr)
	}
	var result outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Command != commandPortRelease || result.Status != statusNoChanges ||
		result.Changed == nil || *result.Changed || result.Site != "branch" || result.Port != "41642" {
		t.Errorf("absent release envelope = %+v, want canonical no_changes result", result)
	}
}

func TestUDPPortCollisionIsFailureWithCleanStdout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, _, err := runUDPPort(t, dir, "reserve", "41642", "branch-fw01", "--site", "branch"); err != nil {
		t.Fatalf("seed reserve: %v", err)
	}
	stdout, _, err := runUDPPort(t, dir, "reserve", "41642", "branch-fw02", "--site", "branch")
	if stdout != "" {
		t.Errorf("collision stdout = %q, want empty", stdout)
	}
	var serviceError *goa.ServiceError
	if !errors.As(err, &serviceError) || serviceError.Name != collisionErrorName {
		t.Fatalf("collision error = %v, want named Goa collision", err)
	}
	if code := exitCodeFor(err); code != exitFailure {
		t.Errorf("collision exit code = %d, want %d", code, exitFailure)
	}
}

func TestUDPPortCommandRejectsInvalidArityAndRequiredSite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "reserve too few args", args: []string{"reserve", "41642", "--site", "branch"}},
		{name: "reserve too many args", args: []string{"reserve", "41642", "fw", "extra", "--site", "branch"}},
		{name: "release no port", args: []string{"release", "--site", "branch"}},
		{name: "list positional arg", args: []string{"list", "extra"}},
		{name: "reserve missing site", args: []string{"reserve", "41642", "fw"}},
		{name: "release missing site", args: []string{"release", "41642"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stdout, _, err := runUDPPort(t, t.TempDir(), test.args...)
			if err == nil {
				t.Fatal("command error = nil, want usage error")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if code := exitCodeFor(err); code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
		})
	}
}

func TestUDPPortCommandRejectsInvalidInputBeforeWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		args   []string
		wantIn string
	}{
		{
			name: "privileged port", args: []string{"reserve", "1023", "fw", "--site", "branch"},
			wantIn: "1024-65535",
		},
		{
			name: "empty device", args: []string{"reserve", "41642", " ", "--site", "branch"},
			wantIn: "device name must not be empty",
		},
		{
			name: "empty site", args: []string{"reserve", "41642", "fw", "--site", " "},
			wantIn: "site name must not be empty",
		},
		{
			name: "explicit empty list site", args: []string{"list", "--site", ""},
			wantIn: "site name must not be empty",
		},
		{
			name: "invalid list site", args: []string{"list", "--site", " "},
			wantIn: "site name must not be empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stdout, _, err := runUDPPort(t, dir, test.args...)
			if err == nil {
				t.Fatal("command error = nil, want invalid input")
			}
			if !strings.Contains(err.Error(), test.wantIn) {
				t.Errorf("command error = %q, want it to contain %q", err, test.wantIn)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "local.ipam.hujson")); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("registry exists after invalid input: %v", statErr)
			}
		})
	}
}

func TestUDPPortListAndReleaseMissingRegistryFail(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"list"}, {"release", "41642", "--site", "branch"}} {
		stdout, _, err := runUDPPort(t, t.TempDir(), args...)
		if err == nil {
			t.Errorf("port %v error = nil, want missing-registry failure", args)
		}
		if stdout != "" {
			t.Errorf("port %v stdout = %q, want empty", args, stdout)
		}
	}
}

func TestUDPPortCommandsAppearInSchemaWithSiteRequirements(t *testing.T) {
	t.Parallel()

	dependencies := commandDependencies{getenv: func(string) string { return "" }}
	root := newRootCommandWithDependencies(&commandState{}, dependencies)
	commands := commandSchema(root)
	found := make(map[string]schemaCommand)
	for _, command := range commands {
		if strings.HasPrefix(command.Name, commandPort) {
			found[command.Name] = command
		}
	}
	for _, name := range []string{commandPort, commandPortList, commandPortReserve, commandPortRelease} {
		if _, ok := found[name]; !ok {
			t.Errorf("schema omits %q", name)
		}
	}
	for _, name := range []string{commandPortReserve, commandPortRelease} {
		siteRequired := false
		for _, flag := range found[name].Flags {
			if flag.Name == "site" && flag.Required {
				siteRequired = true
			}
		}
		if !siteRequired {
			t.Errorf("schema command %q does not mark --site required: %+v", name, found[name])
		}
	}
	for _, flag := range found[commandPortList].Flags {
		if flag.Name == "site" && flag.Required {
			t.Errorf("schema command %q marks optional --site required", commandPortList)
		}
	}
}

func TestUDPPortSanitizedReserveAndListOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	const site = `secret-branch`
	const device = `secret-fw01`

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandPort, "reserve", "41642", device, "--site", site,
		"--registry-dir", dir, "--sanitize", "identity", "--format", formatJSON,
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitOK {
		t.Fatalf("sanitized reserve exit = %d, want %d; stderr: %s", code, exitOK, stderr.String())
	}
	var reserved outputEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &reserved); err != nil {
		t.Fatalf("decode sanitized reserve: %v; stdout: %s", err, stdout.String())
	}
	if reserved.Port != "41642" {
		t.Errorf("sanitized reserve port = %q, want visible port 41642", reserved.Port)
	}
	if !strings.HasPrefix(reserved.Site, "site-") || !strings.HasPrefix(reserved.Device, "hostname-") {
		t.Errorf("sanitized reserve identities = site %q, device %q", reserved.Site, reserved.Device)
	}
	for _, secret := range []string{site, device} {
		assertNoLeak(t, stdout.String(), secret)
	}
	if !strings.Contains(reserved.Message, reserved.Site) || !strings.Contains(reserved.Message, reserved.Device) {
		t.Errorf(
			"sanitized reserve message = %q, want site %q and device %q",
			reserved.Message,
			reserved.Site,
			reserved.Device,
		)
	}

	stdout.Reset()
	stderr.Reset()
	code = runCLI(context.Background(), []string{
		commandPort, commandList, "--registry-dir", dir, "--sanitize", "identity", "--format", formatJSON,
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code != exitOK {
		t.Fatalf("sanitized list exit = %d, want %d; stderr: %s", code, exitOK, stderr.String())
	}
	var listed outputEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("decode sanitized list: %v; stdout: %s", err, stdout.String())
	}
	if listed.UDPPorts == nil || len(*listed.UDPPorts) != 1 {
		t.Fatalf("sanitized UDP ports = %+v, want one entry", listed.UDPPorts)
	}
	entry := (*listed.UDPPorts)[0]
	if entry.Site != reserved.Site || entry.Device != reserved.Device || entry.Port != "41642" {
		t.Errorf("sanitized list entry = %+v, want site/device placeholders and visible port", entry)
	}
	for _, secret := range []string{site, device} {
		assertNoLeak(t, stdout.String(), secret)
	}
}

func TestUDPPortSanitizesInvalidRawInput(t *testing.T) {
	t.Parallel()

	const site = "secret-site"
	const device = "secret\tdevice"
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandPort, "reserve", "41642", device, "--site", site,
		"--registry-dir", t.TempDir(), "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		t.Fatalf("invalid device exit = %d, want non-zero", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("invalid device stdout = %q, want empty", stdout.String())
	}
	assertNoLeak(t, stderr.String(), device)
}

func TestUDPPortSanitizesMalformedStoredIdentities(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, localTailnet+".ipam.hujson")
	const site = `secret-site`
	const device = "malformed\tdevice"
	content := "{\n  \"udpPorts\": {\n    \"" + site + "\": {\n" +
		"      \"41642\": \"malformed\\tdevice\",\n    },\n  },\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed malformed UDP registry: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), []string{
		commandPort, commandList, "--registry-dir", dir, "--sanitize", "identity",
	}, fixedKeyGetenv, &stdout, &stderr, nil)
	if code == exitOK {
		t.Fatalf("malformed registry exit = %d, want non-zero; stdout: %s", code, stdout.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("malformed registry stdout = %q, want empty", stdout.String())
	}
	assertNoLeak(t, stderr.String(), site)
	assertNoLeak(t, stderr.String(), device)
}

func TestUDPPortCollisionSanitization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, site, existing, wanted string
		templateWords                bool
	}{
		{
			name: "quotes and backslashes", site: `site\"boundary`,
			existing: `existing\"device`, wanted: `wanted\device`,
		},
		{name: "template-word lookalikes", site: "site-frame", existing: "for", wanted: "not", templateWords: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if _, _, _, _, err := registry.NewLedger(hujsonstore.New(dir, nil), nil).ReserveUDPPort(
				context.Background(), localTailnet, test.site, "41642", test.existing,
			); err != nil {
				t.Fatalf("seed UDP collision: %v", err)
			}

			var stdout, stderr bytes.Buffer
			code := runCLI(context.Background(), []string{
				commandPort, "reserve", "41642", test.wanted, "--site", test.site,
				"--registry-dir", dir, "--sanitize", "identity",
			}, fixedKeyGetenv, &stdout, &stderr, nil)
			if code != exitFailure {
				t.Fatalf("UDP collision exit = %d, want %d; stderr: %s", code, exitFailure, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("UDP collision stdout = %q, want empty", stdout.String())
			}
			stderrText := stderr.String()
			if !strings.Contains(stderrText, "UDP port 41642 in site") ||
				!strings.Contains(stderrText, "is already reserved for") || !strings.Contains(stderrText, ", not ") {
				t.Errorf("stderr = %q, want intact UDP collision frame and visible port", stderrText)
			}
			assertNoLeak(t, stderrText, test.site)
			if test.templateWords {
				assertNoQuotedLeak(t, stderrText, test.existing)
				assertNoQuotedLeak(t, stderrText, test.wanted)
			} else {
				assertNoLeak(t, stderrText, test.existing)
				assertNoLeak(t, stderrText, test.wanted)
			}
		})
	}
}

func TestUDPPortCollisionErrorFormatMatchesAnchoredPattern(t *testing.T) {
	t.Parallel()

	err := &registry.UDPPortCollisionError{
		Site: `site\"boundary`, Port: "41642", Existing: `existing\device`, Wanted: `wanted"device`,
	}
	match := udpPortCollisionErrorPattern.FindStringSubmatch(err.Error())
	if match == nil {
		t.Fatalf("UDP collision sanitizer pattern does not match %q", err.Error())
	}
	if match[1] != "41642" || match[2] != `"site\\\"boundary"` ||
		match[3] != `"existing\\device"` || match[4] != `"wanted\"device"` {
		t.Errorf("UDP collision captures = %#v, want port/site/existing/wanted", match[1:])
	}
}
