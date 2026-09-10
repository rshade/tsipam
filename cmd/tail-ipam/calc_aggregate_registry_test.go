package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rshade/tailscale-utils/policycore/sanitize"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
)

func runAggregateWithEnvironment(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), append([]string{"calc", "aggregate"}, args...),
		func(name string) string { return env[name] }, &stdout, &stderr,
		func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
			t.Error("aggregate must not collect live devices")
			return nil, nil
		})
	return code, stdout.String(), stderr.String()
}

func writeAggregateRegistry(t *testing.T, dir, tailnet, content string) string {
	t.Helper()
	path := filepath.Join(dir, tailnet+".ipam.hujson")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCalcAggregateFromRegistryMatchesPositionalAndPreservesFile(t *testing.T) {
	t.Parallel()
	for _, format := range []string{formatHuman, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			testAggregateFromRegistryPreservesFile(t, format)
		})
	}
}

func testAggregateFromRegistryPreservesFile(t *testing.T, format string) {
	t.Helper()
	dir := t.TempDir()
	const content = `{
	// Keep this allocation note and the unrelated map unchanged.
	"subnetPools": {
		"10.2.0.0/24": "remote",
		"10.0.0.128/25": "branch",
		"10.0.0.0/25": "hq",
	},
	"siteIDs": {"7": "hq"},
}` + "\n"
	path := writeAggregateRegistry(t, dir, "production", content)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runAggregateWithEnvironment(t, nil,
		"--from-registry", "--registry-dir", dir, "--tailnet", "production", "--format", format)
	if code != exitOK || errOut != "" {
		t.Fatalf("aggregate exit=%d, stderr=%q", code, errOut)
	}
	positional, positionalErr, runErr := runCalcAggregate(t,
		"10.2.0.0/24", "10.0.0.128/25", "10.0.0.0/25", "--format", format)
	if runErr != nil || positionalErr != "" {
		t.Fatalf("positional aggregate: %v, stderr=%q", runErr, positionalErr)
	}
	assertAggregateSourcesEquivalent(t, format, out, positional)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != content || !info.ModTime().Equal(before.ModTime()) {
		t.Errorf("read-only aggregation changed the registry: %s", after)
	}
}

func assertAggregateSourcesEquivalent(t *testing.T, format, out, positional string) {
	t.Helper()
	if format == formatHuman {
		if out != "10.0.0.0/24\n10.2.0.0/24\n" || out != positional {
			t.Errorf("registry=%q, positional=%q, want the same ordered supernets", out, positional)
		}
		return
	}
	var result, supplied outputEnvelope
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(positional), &supplied); err != nil {
		t.Fatal(err)
	}
	if result.Supernets == nil || supplied.Supernets == nil || !slices.Equal(*result.Supernets, *supplied.Supernets) {
		t.Errorf("registry=%s, positional=%s, want matching supernets", out, positional)
	}
	if result.Tailnet != "production" || result.Command != commandCalcAggregate ||
		result.SchemaVersion != outputSchemaVersion || result.Status != statusOK {
		t.Errorf("unexpected registry envelope: %s", out)
	}
}

func TestCalcAggregateFromRegistryEmptyResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, content, format string
	}{
		{"absent map human", `{}`, formatHuman},
		{"absent map JSON", `{}`, formatJSON},
		{"empty map human", `{"subnetPools":{}}`, formatHuman},
		{"empty map JSON", `{"subnetPools":{}}`, formatJSON},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeAggregateRegistry(t, dir, "local", test.content)
			code, out, errOut := runAggregateWithEnvironment(t, nil,
				"--from-registry", "--registry-dir", dir, "--format", test.format)
			if code != exitOK || errOut != "" {
				t.Fatalf("empty %s aggregate exit=%d, stderr=%q", test.format, code, errOut)
			}
			if test.format == formatHuman {
				if out != "" {
					t.Errorf("empty human output=%q", out)
				}
				return
			}
			var result outputEnvelope
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.Supernets == nil || *result.Supernets == nil || len(*result.Supernets) != 0 ||
				result.Status != statusOK || result.Tailnet != "local" {
				t.Errorf("empty registry result must carry supernets=[]: %s", out)
			}
		})
	}
}

func TestCalcAggregateFromRegistryTailnetSelectionAndFlagSpellings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{name: "local default", want: "10.0.0.0/24\n"},
		{name: "environment fallback", env: map[string]string{"TAILSCALE_TAILNET": "staging"}, want: "10.1.0.0/24\n"},
		{
			name: "explicit flag wins", env: map[string]string{"TAILSCALE_TAILNET": "staging"},
			args: []string{"--tailnet", "production"}, want: "10.2.0.0/24\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeAggregateRegistry(t, dir, "local", `{"subnetPools":{"10.0.0.0/24":"local-site"}}`)
			writeAggregateRegistry(t, dir, "staging", `{"subnetPools":{"10.1.0.0/24":"staging-site"}}`)
			writeAggregateRegistry(t, dir, "production", `{"subnetPools":{"10.2.0.0/24":"production-site"}}`)
			for _, dash := range []string{"-", "--"} {
				args := append([]string{dash + "from-registry", dash + "registry-dir", dir}, test.args...)
				code, out, errOut := runAggregateWithEnvironment(t, test.env, args...)
				if code != exitOK || out != test.want || errOut != "" {
					t.Errorf("%s flags: exit=%d, stdout=%q, stderr=%q; want %q", dash, code, out, errOut, test.want)
				}
			}
		})
	}
}

func TestCalcAggregateRejectsConflictingSources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no input", want: "requires at least 1 arg"},
		{name: "both sources", args: []string{"--from-registry", "10.0.0.0/24"}, want: "cannot be used together"},
		{
			name: "directory without mode", args: []string{"--registry-dir", "does-not-exist", "10.0.0.0/24"},
			want: "require --from-registry",
		},
		{
			name: "tailnet without mode", args: []string{"--tailnet", "production", "10.0.0.0/24"},
			want: "require --from-registry",
		},
		{name: "false mode without input", args: []string{"--from-registry=false"}, want: "requires at least 1 arg"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := runAggregateWithEnvironment(t, nil, test.args...)
			if code != exitUsage || out != "" || !strings.Contains(errOut, test.want) {
				t.Errorf("exit=%d, stdout=%q, stderr=%q; want usage error %q", code, out, errOut, test.want)
			}
		})
	}
}

func TestCalcAggregateFromRegistryErrorsPreserveState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
	}{
		{name: "missing file"},
		{name: "malformed document", content: `{invalid json}`},
		{name: "invalid subnet map", content: `{"subnetPools":{"10.0.0.0/24":""}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "local.ipam.hujson")
			if test.content != "" {
				writeAggregateRegistry(t, dir, "local", test.content)
			}
			code, out, errOut := runAggregateWithEnvironment(t, nil,
				"--from-registry", "--registry-dir", dir, "--format", formatJSON)
			if code != exitUsage || out != "" || errOut == "" {
				t.Errorf("exit=%d, stdout=%q, stderr=%q; want a visible registry error", code, out, errOut)
			}
			after, err := os.ReadFile(path)
			if test.content == "" {
				if !os.IsNotExist(err) {
					t.Errorf("missing registry was created or changed: read error=%v", err)
				}
			} else if err != nil || string(after) != test.content {
				t.Errorf("failed read changed registry: bytes=%q, error=%v", after, err)
			}
		})
	}
}

func TestCalcAggregateFromRegistrySanitization(t *testing.T) {
	t.Parallel()
	for _, level := range []string{"identity", "strict"} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeAggregateRegistry(t, dir, "production",
				`{"subnetPools":{"10.0.0.0/25":"hq","10.0.0.128/25":"branch"}}`)
			env := map[string]string{sanitize.KeyEnv: strings.Repeat("ab", 32)}
			for _, format := range []string{formatHuman, formatJSON} {
				args := []string{"--from-registry", "--registry-dir", dir, "--tailnet", "production",
					"--sanitize", level, "--format", format, "--verbose"}
				code, out, errOut := runAggregateWithEnvironment(t, env, args...)
				if code != exitOK {
					t.Fatalf("sanitized aggregate exit=%d, stderr=%q", code, errOut)
				}
				assertRegistryAggregateSanitized(t, format, dir, out, errOut)
				repeatedCode, repeated, repeatedErr := runAggregateWithEnvironment(t, env, args...)
				if repeatedCode != exitOK || repeated != out {
					t.Errorf("keyed output changed: first=%s, second=%s, exit=%d, stderr=%s",
						out, repeated, repeatedCode, repeatedErr)
				}
			}
		})
	}
}

func assertRegistryAggregateSanitized(t *testing.T, format, dir, out, errOut string) {
	t.Helper()
	for _, secret := range []string{"10.0.0", "production", "hq", "branch", dir} {
		if strings.Contains(out+errOut, secret) {
			t.Errorf("sanitized output leaked %q: stdout=%s, stderr=%s", secret, out, errOut)
		}
	}
	if !strings.Contains(out, "cidr-") {
		t.Errorf("expected a CIDR placeholder: %s", out)
	}
	if format != formatJSON {
		return
	}
	var result outputEnvelope
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Tailnet, "tailnet-") || result.Sanitization == nil ||
		result.Supernets == nil || len(*result.Supernets) != 1 {
		t.Errorf("unexpected sanitized envelope: %s", out)
	}
}

func TestCalcAggregateFromRegistrySanitizesErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, content, diagnostic string
	}{
		{
			name: "overlapping pools", diagnostic: "overlap",
			content: `{"subnetPools":{"10.23.0.0/24":"hq-private","10.23.0.0/25":"branch-private"}}`,
		},
		{
			name: "canonical suggestion", diagnostic: "not canonical; use",
			content: `{"subnetPools":{"10.23.0.7/24":"hq-private"}}`,
		},
		{
			name: "trimmed key and canonical suggestion", diagnostic: "not canonical; use",
			content: `{"subnetPools":{" 10.23.0.7/24 ":"hq-private"}}`,
		},
		{
			name: "invalid owner", diagnostic: "must not contain control characters",
			content: `{"subnetPools":{"10.23.0.0/24":{"site":"hq-private","owner":"secret-owner\u0007"}}}`,
		},
		{
			name: "case-insensitive owner field", diagnostic: "must not contain control characters",
			content: `{"subnetPools":{"10.23.0.0/24":{"site":"hq-private","Owner":"secret-owner\u0007"}}}`,
		},
	}
	for _, test := range tests {
		for _, level := range []string{"identity", "strict"} {
			t.Run(test.name+"/"+level, func(t *testing.T) {
				t.Parallel()
				testAggregateRegistryErrorSanitization(t, level, test.content, test.diagnostic)
			})
		}
	}
}

func testAggregateRegistryErrorSanitization(t *testing.T, level, content, diagnostic string) {
	t.Helper()
	dir := t.TempDir()
	path := writeAggregateRegistry(t, dir, "private-tailnet", content)
	code, out, errOut := runAggregateWithEnvironment(t, nil,
		"--from-registry", "--registry-dir", dir, "--tailnet", "private-tailnet", "--sanitize", level)
	if code != exitUsage || out != "" || !strings.Contains(errOut, diagnostic) {
		t.Fatalf("expected visible %q error: exit=%d, stdout=%q, stderr=%q", diagnostic, code, out, errOut)
	}
	for _, secret := range []string{dir, "private-tailnet", "10.23.0", "hq-private", "branch-private", "secret-owner"} {
		if strings.Contains(errOut, secret) {
			t.Errorf("error leaked %q: %s", secret, errOut)
		}
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != content {
		t.Errorf("failed read changed registry: bytes=%q, error=%v", after, err)
	}
}

func TestCalcAggregateFromRegistrySanitizationIsTailnetScoped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := map[string]string{sanitize.KeyEnv: strings.Repeat("ab", 32)}
	outputs := make([]string, 0, 2)
	for _, tailnet := range []string{"production", "staging"} {
		writeAggregateRegistry(t, dir, tailnet, `{"subnetPools":{"10.0.0.0/24":"hq"}}`)
		code, out, errOut := runAggregateWithEnvironment(t, env,
			"--from-registry", "--registry-dir", dir, "--tailnet", tailnet, "--sanitize", "identity")
		if code != exitOK || !strings.HasPrefix(out, "cidr-") {
			t.Fatalf("aggregate exit=%d, stdout=%q, stderr=%q", code, out, errOut)
		}
		outputs = append(outputs, out)
	}
	if outputs[0] == outputs[1] {
		t.Errorf("different registry tailnets produced the same CIDR placeholder: %q", outputs[0])
	}
}

func TestCalcAggregatePositionalErrorSkipsRegistryInventory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const site = "private-registry-site"
	writeAggregateRegistry(t, dir, "local", `{"subnetPools":{"10.0.0.0/24":"`+site+`"}}`)
	state := &commandState{}
	root := newRootCommandWithDependencies(state, commandDependencies{getenv: emptyEnvironment})
	cmd, _, err := root.Find([]string{"calc", "aggregate"})
	if err != nil {
		t.Fatal(err)
	}
	// Point the selector's default at our fixture without explicitly supplying
	// a registry flag: positional mode must not inventory this file on failure.
	if setErr := cmd.Flags().Lookup("registry-dir").Value.Set(dir); setErr != nil {
		t.Fatal(setErr)
	}
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"calc", "aggregate", site, "--sanitize", "identity"})
	executed, executeErr := root.ExecuteC()
	if executeErr == nil || state.sanitizer == nil {
		t.Fatalf("expected invalid positional CIDR after sanitizer initialization: %v", executeErr)
	}
	registerKnownIdentityInventory(executed, state)
	if got := state.sanitizer.Text(site); got != site {
		t.Errorf("positional failure read unrelated registry identities: %q became %q", site, got)
	}
}

func TestCalcAggregateExplicitFalseRetainsPositionalExemption(t *testing.T) {
	t.Parallel()
	code, out, errOut := runAggregateWithEnvironment(t, map[string]string{"TAILSCALE_TAILNET": "private-tailnet"},
		"--from-registry=false", "10.0.0.0/25", "10.0.0.128/25", "--sanitize", "strict", "--format", formatJSON)
	if code != exitOK {
		t.Fatalf("positional aggregate exit=%d, stderr=%q", code, errOut)
	}
	var result outputEnvelope
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Tailnet != "" || result.Supernets == nil || !slices.Equal(*result.Supernets, []string{"10.0.0.0/24"}) {
		t.Errorf("positional output changed: %s", out)
	}
}
