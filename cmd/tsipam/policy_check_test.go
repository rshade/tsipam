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

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/diagnostics"
)

func policyFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "private-policy.hujson")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runPolicyCommand(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(t.Context(), args, devicesEnv(env), &stdout, &stderr,
		func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
			return nil, errors.New("check-policy unexpectedly collected devices")
		})
	return code, stdout.String(), stderr.String()
}

type policyContractCase struct {
	name, policy, registry string
	drift                  int
	report                 string
}

func TestPolicyCheckOfflineContract(t *testing.T) {
	t.Parallel()
	cases := []policyContractCase{
		{
			"matched",
			`{"ipsets":{"ipset:office":["192.0.2.0/25"]}}`,
			`{"subnetPools":{"192.0.2.0/24":"office"},"siteIDs":false,"serviceAddresses":false,"udpPorts":false}`,
			0,
			`{"drift_count":0,"authored":[{"ipset":"ipset:office","cidr":"192.0.2.0/25","state":"matched","pools":["192.0.2.0/24"]}],"reserved":[{"cidr":"192.0.2.0/24","site":"office","state":"matched","ipsets":["ipset:office"]}]}`,
		},
		{
			"both directions",
			`{"ipsets":{"ipset:office":["198.51.100.0/24"]}}`,
			`{"subnetPools":{"192.0.2.0/24":"office"}}`,
			2,
			`{"drift_count":2,"authored":[{"ipset":"ipset:office","cidr":"198.51.100.0/24","state":"authored_but_unreserved","pools":[]}],"reserved":[{"cidr":"192.0.2.0/24","site":"office","state":"reserved_but_unreferenced","ipsets":[]}]}`,
		},
		{"empty", `{}`, `{}`, 0, `{"drift_count":0,"authored":[],"reserved":[]}`},
	}
	for _, tc := range cases {
		for _, format := range []string{formatJSON, formatHuman} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				t.Parallel()
				checkPolicyOfflineContract(t, tc, format)
			})
		}
	}
}

func checkPolicyOfflineContract(t *testing.T, tc policyContractCase, format string) {
	t.Helper()
	dir := t.TempDir()
	path := policyFile(t, dir, tc.policy)
	regPath := writeAggregateRegistry(t, dir, "alias", tc.registry)
	code, out, errOut := runPolicyCommand(t, nil, commandCheckPolicy, path,
		"--registry-dir", dir, "--tailnet", "alias", "--format", format)
	wantCode := exitOK
	if tc.drift > 0 {
		wantCode = exitFailure
	}
	if code != wantCode {
		t.Fatalf("exit = %d, want %d: %s", code, wantCode, errOut)
	}
	if format == formatJSON {
		assertPolicyJSONContract(t, out, tc)
	} else if !strings.Contains(out, "comparison=authored_ipsets") || !strings.Contains(out, "drift_count=") {
		t.Fatalf("missing human comparison: %s", out)
	}
	for file, original := range map[string]string{path: tc.policy, regPath: tc.registry} {
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("input file changed: %s", file)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("created extra files: %v", entries)
	}
}

func assertPolicyJSONContract(t *testing.T, out string, tc policyContractCase) {
	t.Helper()
	var result struct {
		Command          string          `json:"command"`
		Tailnet          string          `json:"tailnet"`
		Comparison       json.RawMessage `json:"comparison"`
		Changed          json.RawMessage `json:"changed"`
		PolicyComparison json.RawMessage `json:"policy_comparison"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Command != commandCheckPolicy || result.Tailnet != "alias" || result.PolicyComparison == nil {
		t.Fatalf("wrong envelope: %s", out)
	}
	var report bytes.Buffer
	if err := json.Compact(&report, result.PolicyComparison); err != nil {
		t.Fatal(err)
	}
	if report.String() != tc.report {
		t.Fatalf("policy_comparison = %s, want %s", report.String(), tc.report)
	}
	if result.Comparison != nil || result.Changed != nil {
		t.Fatalf("unexpected fields: %s", out)
	}
}

func TestPolicyCheckFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, policy, registry, message string
		extra                           []string
	}{
		{"missing policy", "", "{}", "reading policy", nil},
		{"missing registry", "{}", "", "no such file", nil},
		{"invalid policy", "{", "{}", "valid HuJSON", nil},
		{"invalid UTF-8", "{\"ipsets\":{\"ipset:\xff\":[]}}", "{}", "valid UTF-8", nil},
		{"unsupported member", `{"ipsets":{"ipset:secret":["autogroup:internet"]}}`, "{}", "unsupported", nil},
		{"invalid registry", "{}", `{"subnetPools":false}`, "subnetPools", nil},
		{"invalid format", "{}", "{}", "format", []string{"--format", "bad"}},
		{"extra argument", "{}", "{}", "accepts 1 arg", []string{"extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "missing.hujson")
			if tc.policy != "" {
				path = policyFile(t, dir, tc.policy)
			}
			if tc.registry != "" {
				writeAggregateRegistry(t, dir, "alias", tc.registry)
			}
			args := append([]string{commandCheckPolicy, path, "--registry-dir", dir, "--tailnet", "alias"}, tc.extra...)
			code, out, errOut := runPolicyCommand(t, nil, args...)
			if code != exitUsage || out != "" || !strings.Contains(errOut, tc.message) {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want exit 2 and %q", code, out, errOut, tc.message)
			}
		})
	}
	code, out, errOut := runPolicyCommand(t, nil, commandCheckPolicy)
	if code != exitUsage || out != "" || !strings.Contains(errOut, "accepts 1 arg") {
		t.Fatalf("missing argument: %d %q %q", code, out, errOut)
	}
}

func TestPolicyCheckOutputFailureOverridesDrift(t *testing.T) {
	t.Parallel()
	for _, format := range []string{formatJSON, formatHuman} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := policyFile(t, dir, "{}")
			writeAggregateRegistry(t, dir, "alias", `{"subnetPools":{"192.0.2.0/24":"office"}}`)
			var stderr bytes.Buffer
			code := runCLI(t.Context(), []string{commandCheckPolicy, path, "--registry-dir", dir,
				"--tailnet", "alias", "--format", format}, devicesEnv(nil), planningErrorWriter{}, &stderr, nil)
			if code != exitUsage || !strings.Contains(stderr.String(), "output unavailable") {
				t.Fatalf("output error = %d, %s", code, &stderr)
			}
		})
	}
}

func TestPolicyCheckSanitization(t *testing.T) {
	t.Parallel()
	for _, format := range []string{formatJSON, formatHuman} {
		for _, level := range []string{"identity", "strict"} {
			t.Run(format+"/"+level, func(t *testing.T) {
				t.Parallel()
				checkPolicySanitization(t, format, level)
			})
		}
	}
}

func checkPolicySanitization(t *testing.T, format, level string) {
	t.Helper()
	dir := t.TempDir()
	path := policyFile(t, dir, `{"ipsets":{"ipset:secret":["192.0.2.0/25","198.51.100.0/24"]}}`)
	writeAggregateRegistry(t, dir, "secret-alias", `{"subnetPools":{"192.0.2.0/24":"secret-site"}}`)
	env := map[string]string{
		"TAILSCALE_SANITIZE": level, "TAILSCALE_SANITIZE_KEY": strings.Repeat("1", 64),
		"TAILSCALE_TAILNET": "different-live-tailnet",
	}
	args := []string{commandCheckPolicy, path, "--registry-dir", dir,
		"--tailnet", "secret-alias", "--format", format, "--sanitize", "off"}
	code, out, errOut := runPolicyCommand(t, env, args...)
	if code != exitFailure {
		t.Fatalf("exit=%d stderr=%s", code, errOut)
	}
	for _, raw := range []string{"ipset:secret", "secret-alias", "secret-site", "192.0.2.0", "198.51.100.0", "different-live-tailnet"} {
		if strings.Contains(out+errOut, raw) {
			t.Fatalf("leaked %q: %s %s", raw, out, errOut)
		}
	}
	_, again, _ := runPolicyCommand(t, env, args...)
	if out != again {
		t.Fatal("fixed-key sanitization changed across invocations")
	}
	if format == formatJSON {
		var result outputEnvelope
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		report := result.PolicyComparison
		if report == nil || report.DriftCount != 1 || len(report.Authored) != 2 || len(report.Reserved) != 1 ||
			len(report.Authored[0].Pools) != 1 ||
			len(report.Reserved[0].IPSets) != 1 ||
			report.Authored[0].IPSet != report.Reserved[0].IPSets[0] ||
			report.Authored[0].Pools[0] != report.Reserved[0].CIDR {
			t.Fatalf("inconsistent sanitized evidence: %s", out)
		}
	}
}

func TestPolicyCheckSanitizedErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := policyFile(t, dir, `{"ipsets":{"ipset:secret":["host:private"]}}`)
	writeAggregateRegistry(t, dir, "alias", "{}")
	code, out, errOut := runPolicyCommand(t, map[string]string{"TAILSCALE_SANITIZE": "identity"},
		commandCheckPolicy, path, "--registry-dir", dir, "--tailnet", "alias")
	if code != exitUsage || out != "" || !strings.Contains(errOut, "missing host reference") {
		t.Fatalf("error = %d %q %q", code, out, errOut)
	}
	if strings.Contains(errOut, "secret") || strings.Contains(errOut, "private") {
		t.Fatalf("leaked policy: %s", errOut)
	}
	missing := filepath.Join(dir, "missing-private.hujson")
	code, out, errOut = runPolicyCommand(t, map[string]string{"TAILSCALE_SANITIZE": "identity"},
		commandCheckPolicy, missing, "--registry-dir", dir, "--tailnet", "alias")
	if code != exitUsage || out != "" || strings.Contains(errOut, missing) {
		t.Fatalf("path error = %d %q %q", code, out, errOut)
	}
}

func TestPolicyComparisonSanitizationFieldDecisions(t *testing.T) {
	t.Parallel()
	if fields := sanitize.UnhandledFields[policyComparisonOutput](
		"DriftCount", "Authored", "Reserved",
	); len(fields) > 0 {
		t.Fatalf("unhandled report fields: %v", fields)
	}
	if fields := sanitize.UnhandledFields[policyAuthoredOutput](
		"IPSet", "CIDR", "State", "Pools",
	); len(fields) > 0 {
		t.Fatalf("unhandled authored fields: %v", fields)
	}
	if fields := sanitize.UnhandledFields[policyReservedOutput](
		"CIDR", "Site", "State", "IPSets",
	); len(fields) > 0 {
		t.Fatalf("unhandled reserved fields: %v", fields)
	}
}
