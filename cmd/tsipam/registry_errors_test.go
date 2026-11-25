package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// runRegistryCommand executes one registry-backed command against dir with
// the planning collector stubbed out, so a registry failure is the only
// failure a case can hit.
func runRegistryCommand(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(t.Context(), args, devicesEnv(env), &stdout, &stderr,
		func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
			return nil, errors.New("registry failure must surface before live collection")
		})
	return code, stdout.String(), failureText(t, stderr.String())
}

// registryCommandCases enumerates every command that reads an existing
// registry; each must report a missing file the same way.
func registryCommandCases(t *testing.T, dir string) map[string][]string {
	t.Helper()
	policy := policyFile(t, dir, "{}")
	return map[string][]string{
		commandSubnetList:     {commandSubnet, commandList},
		commandSiteIDList:     {commandSiteID, commandList},
		commandSiteIDRelease:  {commandSiteID, "release", "1", "--yes"},
		commandServiceList:    {commandService, commandList},
		commandPortList:       {commandPort, commandList},
		"calc aggregate":      {commandCalc, "aggregate", "--from-registry"},
		commandPlan:           {commandPlan},
		commandCheck:          {commandCheck},
		commandCheckPolicy:    {commandCheckPolicy, policy},
		commandSubnetRelease:  {commandSubnet, "release", "192.0.2.0/24", "--yes"},
		commandServiceRelease: {commandService, "release", "100.64.0.1", "--yes"},
		commandPortRelease:    {commandPort, "release", "41641", "--site", "hq", "--yes"},
	}
}

func TestMissingRegistryErrorNamesTailnetAndDirectory(t *testing.T) {
	t.Parallel()
	env := map[string]string{"TAILSCALE_TAILNET": "unused", "TAILSCALE_API_KEY": "unused"}
	for name, base := range registryCommandCases(t, t.TempDir()) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			args := slices.Concat(base, []string{"--registry-dir", dir, "--tailnet", "alias"})
			code, out, errOut := runRegistryCommand(t, env, args...)
			assertUsageFailure(t, code, out)
			want := fmt.Sprintf("no registry for tailnet %q in %q", "alias", dir)
			if !strings.Contains(errOut, want) {
				t.Errorf("stderr = %q, want %q", errOut, want)
			}
			assertNoTransportFault(t, errOut)
		})
	}
}

func TestMissingRegistryDirectoryIsReported(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "absent")
	cases := map[string][]string{
		commandSubnetList:    {commandSubnet, commandList},
		commandSubnetReserve: {commandSubnet, "reserve", "192.0.2.0/24", "hq"},
	}
	for name, base := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			args := slices.Concat(base, []string{"--registry-dir", dir, "--tailnet", "alias"})
			code, out, errOut := runRegistryCommand(t, nil, args...)
			assertUsageFailure(t, code, out)
			want := fmt.Sprintf("registry directory %q does not exist", dir)
			if !strings.Contains(errOut, want) {
				t.Errorf("stderr = %q, want %q", errOut, want)
			}
			assertNoTransportFault(t, errOut)
		})
	}
}

func TestUnreadableRegistryErrorIsNotTransportFault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, "alias", "{\n{{{")
	code, out, errOut := runRegistryCommand(t, nil,
		commandSubnet, commandList, "--registry-dir", dir, "--tailnet", "alias")
	assertUsageFailure(t, code, out)
	if !strings.Contains(errOut, "decoding registry") {
		t.Errorf("stderr = %q, want the decode failure", errOut)
	}
	assertNoTransportFault(t, errOut)
}

func TestMissingRegistryErrorIsSanitized(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const tailnet = "corp-secret"
	env := map[string]string{"TAILSCALE_SANITIZE": "identity"}
	code, out, errOut := runRegistryCommand(t, env,
		commandSubnet, commandList, "--registry-dir", dir, "--tailnet", tailnet)
	assertUsageFailure(t, code, out)
	if !strings.Contains(errOut, "no registry for tailnet ") {
		t.Errorf("stderr = %q, want the missing-registry frame", errOut)
	}
	assertNoLeak(t, errOut, tailnet)
	if _, err := os.Stat(filepath.Join(dir, tailnet+".ipam.hujson")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("list created a registry: %v", err)
	}
}

// assertNoTransportFault fails when the loopback transport's undeclared-error
// envelope reaches the operator instead of the service's own message.
func assertNoTransportFault(t *testing.T, errOut string) {
	t.Helper()
	for _, leak := range []string{"invalid response code", `"name":"fault"`, `"temporary":`} {
		if strings.Contains(errOut, leak) {
			t.Errorf("stderr leaks transport fault %q: %s", leak, errOut)
		}
	}
}
