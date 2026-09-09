package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rshade/tailscale-utils/policycore/sanitize"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
)

func runCalcSplit(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	state := &commandState{sanitizeLevel: "off"}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"calc", "split"}, args...))
	err := root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// runCalcSplitCLI drives the full runCLI path (unlike runCalcSplit, which
// calls root.Execute() directly) so the returned exit code and stderr text
// match what a real invocation prints — SilenceErrors hands error printing
// to runCLI, so root.Execute() alone never writes to stderr.
func runCalcSplitCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code := runCLI(
		context.Background(), append([]string{"calc", "split"}, args...),
		func(string) string { return "" }, &outBuf, &errBuf, nil,
	)
	return code, outBuf.String(), errBuf.String()
}

func runCalcAggregate(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	state := &commandState{sanitizeLevel: "off"}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"calc", "aggregate"}, args...))
	err := root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func runCalcAggregateCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code := runCLI(
		context.Background(), append([]string{"calc", "aggregate"}, args...),
		func(string) string { return "" }, &outBuf, &errBuf, nil,
	)
	return code, outBuf.String(), errBuf.String()
}

func runCalcNextFree(t *testing.T, registryDir string, devices []deviceRecord, args ...string) (string, string, error) {
	t.Helper()
	state := &commandState{sanitizeLevel: "off"}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
		listDevices: func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
			return devices, nil
		},
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"calc", "next-free"}, append(args, "--registry-dir", registryDir)...))
	err := root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func writeNextFreeRegistry(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "local.ipam.hujson"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCalcNextFreeCombinesRegistryAndLiveRoutes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeNextFreeRegistry(t, dir, `{"subnetPools":{"10.0.0.0/26":"reserved"}}`)

	out, errOut, err := runCalcNextFree(t, dir, []deviceRecord{{
		Name: "router", AdvertisedRoutes: []string{"10.0.0.64/26"},
	}}, "10.0.0.0/24", "--size", "26")
	if err != nil {
		t.Fatalf("runCalcNextFree() error = %v (stderr = %q)", err, errOut)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
	if out != "10.0.0.128/26\n" {
		t.Errorf("stdout = %q, want one bare CIDR", out)
	}
}

func TestCalcNextFreeExclusionSourcesAndCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		registry   string
		devices    []deviceRecord
		args       []string
		wantOutput string
	}{
		{
			name:       "registry only",
			registry:   `{"subnetPools":{"10.0.0.0/26":"reserved"}}`,
			args:       []string{"10.0.0.0/24", "--size", "26"},
			wantOutput: "10.0.0.64/26\n",
		},
		{
			name:     "live route only",
			registry: `{}`,
			devices: []deviceRecord{{
				Name: "router", AdvertisedRoutes: []string{"10.0.0.0/26"},
			}},
			args:       []string{"10.0.0.0/24", "--size", "26"},
			wantOutput: "10.0.0.64/26\n",
		},
		{
			name:     "malformed live route is ignored",
			registry: `{}`,
			devices: []deviceRecord{{
				Name: "router", AdvertisedRoutes: []string{"not-a-cidr"},
			}},
			args:       []string{"10.0.0.0/24", "--size", "26"},
			wantOutput: "10.0.0.0/26\n",
		},
		{
			name:       "short count flag",
			registry:   `{}`,
			args:       []string{"10.0.0.0/29", "--size", "31", "-n", "2"},
			wantOutput: "10.0.0.0/31\n10.0.0.2/31\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeNextFreeRegistry(t, dir, test.registry)
			out, errOut, err := runCalcNextFree(t, dir, test.devices, test.args...)
			if err != nil {
				t.Fatalf("runCalcNextFree() error = %v (stderr = %q)", err, errOut)
			}
			if out != test.wantOutput {
				t.Errorf("stdout = %q, want %q", out, test.wantOutput)
			}
		})
	}
}

func TestCalcNextFreeMultipleAndExhaustedOutput(t *testing.T) {
	t.Parallel()

	t.Run("all returns ordered blocks", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeNextFreeRegistry(t, dir, `{}`)
		out, errOut, err := runCalcNextFree(t, dir, nil, "10.0.0.0/30", "--size", "31", "--all")
		if err != nil {
			t.Fatalf("runCalcNextFree() error = %v (stderr = %q)", err, errOut)
		}
		if out != "10.0.0.0/31\n10.0.0.2/31\n" {
			t.Errorf("stdout = %q, want ordered free blocks", out)
		}
	})

	t.Run("exhaustion is successful", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeNextFreeRegistry(t, dir, `{}`)
		out, errOut, err := runCalcNextFree(t, dir, []deviceRecord{{
			Name: "router", AdvertisedRoutes: []string{"10.0.0.0/24"},
		}}, "10.0.0.0/24", "--size", "26", "--format", formatJSON)
		if err != nil {
			t.Fatalf("runCalcNextFree() error = %v (stderr = %q)", err, errOut)
		}
		var envelope outputEnvelope
		if decodeErr := json.Unmarshal([]byte(out), &envelope); decodeErr != nil {
			t.Fatalf("decode JSON output: %v\n%s", decodeErr, out)
		}
		if envelope.FreeBlocks == nil || len(*envelope.FreeBlocks) != 0 {
			t.Errorf("FreeBlocks = %#v, want empty list", envelope.FreeBlocks)
		}
		if !strings.Contains(envelope.Message, "no free /26 block") {
			t.Errorf("Message = %q, want exhaustion explanation", envelope.Message)
		}
	})
}

func TestCalcNextFreeRejectsIPv6(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeNextFreeRegistry(t, dir, `{}`)
	_, _, err := runCalcNextFree(t, dir, nil, "2001:db8::/48", "--size", "64")
	if err == nil {
		t.Fatal("runCalcNextFree() error = nil, want IPv6 rejection")
	}
	if !strings.Contains(err.Error(), "#257") {
		t.Errorf("error = %q, want IPv6 follow-up reference", err)
	}
}

func TestCalcNextFreeValidatesOptions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeNextFreeRegistry(t, dir, `{}`)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "target shorter than pool",
			args: []string{"10.0.0.0/24", "--size", "20"},
			want: "must be between",
		},
		{
			name: "count must be positive",
			args: []string{"10.0.0.0/24", "--size", "26", "--count", "0"},
			want: "count must be positive",
		},
		{
			name: "all conflicts with count",
			args: []string{"10.0.0.0/24", "--size", "26", "--all", "--count", "2"},
			want: "cannot be used together",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := runCalcNextFree(t, dir, nil, test.args...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCalcNextFreeSanitizesSuggestedBlocks(t *testing.T) {
	t.Parallel()
	output, err := sanitize.New(sanitize.Options{
		Level: sanitize.Identity, Tool: toolName, Tailnet: "example.ts.net",
		KeyHex: strings.Repeat("ab", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := []string{"10.0.0.0/26"}
	input := newOutput(commandCalcNextFree, statusOK, "suggested 1 free block(s)", "example.ts.net")
	input.FreeBlocks = &blocks

	got := sanitizeOutput(input, output)
	if got.FreeBlocks == nil || len(*got.FreeBlocks) != 1 {
		t.Fatalf("FreeBlocks = %#v, want one sanitized value", got.FreeBlocks)
	}
	if (*got.FreeBlocks)[0] == blocks[0] || !strings.HasPrefix((*got.FreeBlocks)[0], "cidr-") {
		t.Errorf("FreeBlocks = %#v, want CIDR placeholder", got.FreeBlocks)
	}
}

func TestCalcSplitHumanOutput(t *testing.T) {
	t.Parallel()

	// Test successful split with human output.
	out, errOut, err := runCalcSplit(t, "10.0.0.0/24", "--prefix", "26")
	if err != nil {
		t.Fatalf("runCalcSplit() error = %v", err)
	}

	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	// 10.0.0.0/24 -> /26 = 4 children
	if len(lines) != 4 {
		t.Errorf("got %d lines, want 4", len(lines))
	}
	if len(lines) > 0 && lines[0] != "10.0.0.0/26" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "10.0.0.0/26")
	}
	if len(lines) > 1 && lines[1] != "10.0.0.64/26" {
		t.Errorf("lines[1] = %q, want %q", lines[1], "10.0.0.64/26")
	}
	if len(lines) > 2 && lines[2] != "10.0.0.128/26" {
		t.Errorf("lines[2] = %q, want %q", lines[2], "10.0.0.128/26")
	}
	if len(lines) > 3 && lines[3] != "10.0.0.192/26" {
		t.Errorf("lines[3] = %q, want %q", lines[3], "10.0.0.192/26")
	}
}

func TestCalcSplitTooMany(t *testing.T) {
	t.Parallel()

	// A too-many-children split is a refusal of a well-formed request (like
	// a site-ID collision), not a success: exit exitFailure, nothing on
	// stdout, the explanatory message on stderr.
	code, out, errOut := runCalcSplitCLI(t, "10.0.0.0/8", "--prefix", "32")

	if code != exitFailure {
		t.Errorf("exit code = %d, want %d (exitFailure)", code, exitFailure)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty on refusal", out)
	}
	if !strings.Contains(errOut, "too many to list") {
		t.Errorf("stderr = %q, want to contain %q", errOut, "too many to list")
	}
	if !strings.Contains(errOut, "16777216") {
		t.Errorf("stderr = %q, want to contain %q", errOut, "16777216")
	}
}

func TestCalcSplitIPv6HumanOutput(t *testing.T) {
	t.Parallel()

	out, errOut, err := runCalcSplit(t, "fd7a:115c:a1e0::/127", "--prefix", "128")
	if err != nil {
		t.Fatalf("runCalcSplit() error = %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}

	want := "fd7a:115c:a1e0::/128\nfd7a:115c:a1e0::1/128\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestCalcSplitIPv6TooMany(t *testing.T) {
	t.Parallel()

	code, out, errOut := runCalcSplitCLI(t, "2001:db8::/32", "--prefix", "64")

	if code != exitFailure {
		t.Errorf("exit code = %d, want %d (exitFailure)", code, exitFailure)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty on refusal", out)
	}
	if !strings.Contains(errOut, "too many to list") {
		t.Errorf("stderr = %q, want to contain %q", errOut, "too many to list")
	}
	if !strings.Contains(errOut, "4294967296") {
		t.Errorf("stderr = %q, want to contain %q", errOut, "4294967296")
	}
}

func TestCalcSplitNormalized4in6(t *testing.T) {
	t.Parallel()

	// Test that 4-in-6 addresses are normalized and produce same results as IPv4.
	// ::ffff:10.0.0.0/120 is a /24 mapped into IPv6 space; split into /28 should yield 16 subnets.
	out4in6, errOut4in6, err4in6 := runCalcSplit(t, "::ffff:10.0.0.0/120", "--prefix", "28")
	if err4in6 != nil {
		t.Fatalf("4-in-6 split error = %v (stderr = %q)", err4in6, errOut4in6)
	}

	// Compare against plain IPv4 (10.0.0.0/24 split into /28)
	outIPv4, _, errIPv4 := runCalcSplit(t, "10.0.0.0/24", "--prefix", "28")
	if errIPv4 != nil {
		t.Fatalf("IPv4 split error = %v", errIPv4)
	}

	if out4in6 != outIPv4 {
		t.Errorf("4-in-6 output differs from IPv4:\n4-in-6:\n%s\nIPv4:\n%s", out4in6, outIPv4)
	}
}

func TestCalcSplitTooShort4in6FallsBackToIPv6(t *testing.T) {
	t.Parallel()

	// A prefix shorter than /96 cannot be normalized to IPv4 because it
	// contains non-mapped IPv6 addresses. Treat the whole prefix as IPv6.
	out, errOut, err := runCalcSplit(t, "::ffff:10.0.0.0/95", "--prefix", "95")
	if err != nil {
		t.Fatalf("runCalcSplit() error = %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
	if out != "::fffe:0:0/95\n" {
		t.Errorf("stdout = %q, want %q", out, "::fffe:0:0/95\n")
	}
}

func TestCalcSplitJSONOutput(t *testing.T) {
	t.Parallel()

	// Test JSON output format.
	state := &commandState{sanitizeLevel: "off"}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--format", "json", "calc", "split", "10.0.0.0/30", "--prefix", "31"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, errBuf.String())
	}

	var envelope outputEnvelope
	if err = json.Unmarshal(outBuf.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, outBuf.String())
	}

	if envelope.SchemaVersion != outputSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", envelope.SchemaVersion, outputSchemaVersion)
	}
	if envelope.Tool != toolName {
		t.Errorf("Tool = %q, want %q", envelope.Tool, toolName)
	}
	if envelope.Command != commandCalcSplit {
		t.Errorf("Command = %q, want %q", envelope.Command, commandCalcSplit)
	}
	if envelope.Status != statusOK {
		t.Errorf("Status = %q, want %q", envelope.Status, statusOK)
	}
	if envelope.Subnets == nil {
		t.Errorf("Subnets = nil, want non-nil")
	}
	if envelope.Subnets != nil && len(*envelope.Subnets) != 2 {
		t.Errorf("len(Subnets) = %d, want 2", len(*envelope.Subnets))
	}
	if envelope.Subnets != nil && len(*envelope.Subnets) >= 1 && (*envelope.Subnets)[0] != "10.0.0.0/31" {
		t.Errorf("Subnets[0] = %q, want %q", (*envelope.Subnets)[0], "10.0.0.0/31")
	}
	if envelope.Subnets != nil && len(*envelope.Subnets) >= 2 && (*envelope.Subnets)[1] != "10.0.0.2/31" {
		t.Errorf("Subnets[1] = %q, want %q", (*envelope.Subnets)[1], "10.0.0.2/31")
	}
}

func TestCalcSplitJSONFormatTooManyIsStillAPlainTextRefusal(t *testing.T) {
	t.Parallel()

	// A refusal carries no structured envelope in this tool (see the
	// site-ID-collision precedent in main.go's exitCodeFor): --format json
	// changes how success is rendered, not how a refusal is reported.
	var outBuf, errBuf bytes.Buffer
	code := runCLI(
		context.Background(),
		[]string{"--format", "json", "calc", "split", "10.0.0.0/8", "--prefix", "32"},
		func(string) string { return "" }, &outBuf, &errBuf, nil,
	)

	if code != exitFailure {
		t.Errorf("exit code = %d, want %d (exitFailure)", code, exitFailure)
	}
	if outBuf.String() != "" {
		t.Errorf("stdout = %q, want empty on refusal even under --format json", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "too many to list") {
		t.Errorf("stderr = %q, want to contain %q", errBuf.String(), "too many to list")
	}
	if !strings.Contains(errBuf.String(), "16777216") {
		t.Errorf("stderr = %q, want to contain %q", errBuf.String(), "16777216")
	}
}

func TestCalcSplitNotSanitizedUnderIdentity(t *testing.T) {
	t.Parallel()

	// Test that calc split output is not sanitized even with --sanitize identity.
	// Subnets should appear unredacted in both human and JSON output.
	for _, format := range []string{"human", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			state := &commandState{sanitizeLevel: "identity"}
			root := newRootCommandWithDependencies(state, commandDependencies{
				getenv: func(name string) string {
					if name == "TAILSCALE_SANITIZE" {
						return "identity"
					}
					return ""
				},
			})
			var outBuf, errBuf bytes.Buffer
			root.SetOut(&outBuf)
			root.SetErr(&errBuf)
			root.SetArgs([]string{"--format", format, "calc", "split", "10.0.0.0/24", "--prefix", "26"})
			err := root.Execute()
			if err != nil {
				t.Fatalf("Execute() error = %v; stderr = %q", err, errBuf.String())
			}

			output := outBuf.String()

			// Verify that the subnets appear unredacted (not replaced with hashes/placeholders)
			if !strings.Contains(output, "10.0.0.0/26") {
				t.Errorf("output missing first subnet 10.0.0.0/26: %s", output)
			}
		})
	}
}

func TestCalcSplit4In6ErrorWrapping(t *testing.T) {
	t.Parallel()

	// A 4-in-6 prefix (::ffff:0.0.0.0/96 normalizes to 0.0.0.0/0)
	// split into /32 would produce 2^32 subnets, exceeding the materialization limit.
	// Error should mention both the original 4-in-6 form and normalized IPv4 form.
	code, out, errOut := runCalcSplitCLI(t, "::ffff:0.0.0.0/96", "--prefix", "32")

	if code != exitFailure {
		t.Errorf("exit code = %d, want %d (exitFailure)", code, exitFailure)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty on error", out)
	}
	if !strings.Contains(errOut, "::ffff:0.0.0.0/96") {
		t.Errorf("stderr missing original 4-in-6 CIDR: %q", errOut)
	}
	if !strings.Contains(errOut, "0.0.0.0/0") {
		t.Errorf("stderr missing normalized IPv4 form: %q", errOut)
	}
	if !strings.Contains(errOut, "is an IPv4-mapped prefix") {
		t.Errorf("stderr missing normalization note: %q", errOut)
	}
	if !strings.Contains(errOut, "too many to list") {
		t.Errorf("stderr missing underlying too-many-children error: %q", errOut)
	}
}

func runCalcInfo(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	state := &commandState{sanitizeLevel: "off"}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"calc", "info"}, args...))
	err := root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// runCalcInfoCLI drives the full runCLI path for exit-code and stderr
// assertions, for the same reason as runCalcSplitCLI above.
func runCalcInfoCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code := runCLI(
		context.Background(), append([]string{"calc", "info"}, args...),
		func(string) string { return "" }, &outBuf, &errBuf, nil,
	)
	return code, outBuf.String(), errBuf.String()
}

func TestCalcInfoHumanOutput(t *testing.T) {
	t.Parallel()

	out, errOut, err := runCalcInfo(t, "192.168.1.0/24")
	if err != nil {
		t.Fatalf("runCalcInfo() error = %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}

	want := "prefix\t192.168.1.0/24\n" +
		"network\t192.168.1.0\n" +
		"broadcast\t192.168.1.255\n" +
		"first_usable\t192.168.1.1\n" +
		"last_usable\t192.168.1.254\n" +
		"usable_hosts\t254\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestCalcInfoHumanRFC3021OmitsBroadcast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cidr string
		want string
	}{
		{
			name: "slash31_point_to_point",
			cidr: "10.0.0.0/31",
			want: "prefix\t10.0.0.0/31\n" +
				"network\t10.0.0.0\n" +
				"first_usable\t10.0.0.0\n" +
				"last_usable\t10.0.0.1\n" +
				"usable_hosts\t2\n",
		},
		{
			name: "slash32_host_route",
			cidr: "10.0.0.5/32",
			want: "prefix\t10.0.0.5/32\n" +
				"network\t10.0.0.5\n" +
				"first_usable\t10.0.0.5\n" +
				"last_usable\t10.0.0.5\n" +
				"usable_hosts\t1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, err := runCalcInfo(t, tt.cidr)
			if err != nil {
				t.Fatalf("runCalcInfo() error = %v", err)
			}
			if errOut != "" {
				t.Errorf("stderr = %q, want empty", errOut)
			}
			if out != tt.want {
				t.Errorf("stdout = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestCalcInfoJSONOutput(t *testing.T) {
	t.Parallel()

	state := &commandState{sanitizeLevel: "off"}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--format", "json", "calc", "info", "10.1.2.0/30"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, errBuf.String())
	}

	var envelope outputEnvelope
	if err := json.Unmarshal(outBuf.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, outBuf.String())
	}

	if envelope.Command != commandCalcInfo {
		t.Errorf("Command = %q, want %q", envelope.Command, commandCalcInfo)
	}
	if envelope.Status != statusOK {
		t.Errorf("Status = %q, want %q", envelope.Status, statusOK)
	}
	if envelope.BlockInfo == nil {
		t.Fatalf("BlockInfo = nil, want non-nil; output = %q", outBuf.String())
	}
	if envelope.BlockInfo.Prefix != "10.1.2.0/30" {
		t.Errorf("BlockInfo.Prefix = %q, want %q", envelope.BlockInfo.Prefix, "10.1.2.0/30")
	}
	if envelope.BlockInfo.Network != "10.1.2.0" {
		t.Errorf("BlockInfo.Network = %q, want %q", envelope.BlockInfo.Network, "10.1.2.0")
	}
	if envelope.BlockInfo.Broadcast != "10.1.2.3" {
		t.Errorf("BlockInfo.Broadcast = %q, want %q", envelope.BlockInfo.Broadcast, "10.1.2.3")
	}
	if envelope.BlockInfo.FirstUsable != "10.1.2.1" {
		t.Errorf("BlockInfo.FirstUsable = %q, want %q", envelope.BlockInfo.FirstUsable, "10.1.2.1")
	}
	if envelope.BlockInfo.LastUsable != "10.1.2.2" {
		t.Errorf("BlockInfo.LastUsable = %q, want %q", envelope.BlockInfo.LastUsable, "10.1.2.2")
	}

	// The usable-host count is a decimal string in the machine contract,
	// not a JSON number, so the IPv6 follow-up cannot silently degrade
	// consumers that read it as an integer.
	if envelope.BlockInfo.UsableHosts != "2" {
		t.Errorf("BlockInfo.UsableHosts = %q, want %q", envelope.BlockInfo.UsableHosts, "2")
	}
	if !strings.Contains(outBuf.String(), `"usable_hosts": "2"`) {
		t.Errorf("raw JSON %q missing string-encoded usable_hosts", outBuf.String())
	}
}

func TestCalcInfoJSONOmitsBroadcastKeyForRFC3021(t *testing.T) {
	t.Parallel()

	state := &commandState{sanitizeLevel: "off"}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--format", "json", "calc", "info", "10.0.0.0/31"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, errBuf.String())
	}

	if strings.Contains(outBuf.String(), "broadcast") {
		t.Errorf("raw JSON %q carries a broadcast key for a /31; want it omitted", outBuf.String())
	}
}

func TestCalcInfoNormalized4in6(t *testing.T) {
	t.Parallel()

	out4in6, errOut, err := runCalcInfo(t, "::ffff:10.0.0.0/120")
	if err != nil {
		t.Fatalf("4-in-6 info error = %v (stderr = %q)", err, errOut)
	}

	outIPv4, _, errIPv4 := runCalcInfo(t, "10.0.0.0/24")
	if errIPv4 != nil {
		t.Fatalf("IPv4 info error = %v", errIPv4)
	}

	if out4in6 != outIPv4 {
		t.Errorf("4-in-6 output differs from IPv4:\n4-in-6:\n%s\nIPv4:\n%s", out4in6, outIPv4)
	}
}

func TestCalcInfoIPv6HumanOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cidr string
		want string
	}{
		{
			name: "slash48", cidr: "2001:db8:1234:5678::/48",
			want: "prefix\t2001:db8:1234::/48\n" +
				"network\t2001:db8:1234::\n" +
				"first_usable\t2001:db8:1234::\n" +
				"last_usable\t2001:db8:1234:ffff:ffff:ffff:ffff:ffff\n" +
				"usable_hosts\t1208925819614629174706176\n",
		},
		{
			name: "slash64_host_bits_masked", cidr: "2001:db8:1::42/64",
			want: "prefix\t2001:db8:1::/64\n" +
				"network\t2001:db8:1::\n" +
				"first_usable\t2001:db8:1::\n" +
				"last_usable\t2001:db8:1:0:ffff:ffff:ffff:ffff\n" +
				"usable_hosts\t18446744073709551616\n",
		},
		{
			name: "slash96", cidr: "2001:db8:abcd:1234:5678:90ab:cdef:1234/96",
			want: "prefix\t2001:db8:abcd:1234:5678:90ab::/96\n" +
				"network\t2001:db8:abcd:1234:5678:90ab::\n" +
				"first_usable\t2001:db8:abcd:1234:5678:90ab::\n" +
				"last_usable\t2001:db8:abcd:1234:5678:90ab:ffff:ffff\n" +
				"usable_hosts\t4294967296\n",
		},
		{
			name: "slash127", cidr: "2001:db8::4/127",
			want: "prefix\t2001:db8::4/127\n" +
				"network\t2001:db8::4\n" +
				"first_usable\t2001:db8::4\n" +
				"last_usable\t2001:db8::5\n" +
				"usable_hosts\t2\n",
		},
		{
			name: "slash128", cidr: "2001:db8::1/128",
			want: "prefix\t2001:db8::1/128\n" +
				"network\t2001:db8::1\n" +
				"first_usable\t2001:db8::1\n" +
				"last_usable\t2001:db8::1\n" +
				"usable_hosts\t1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, err := runCalcInfo(t, tt.cidr)
			if err != nil {
				t.Fatalf("runCalcInfo() error = %v (stderr = %q)", err, errOut)
			}
			if errOut != "" {
				t.Errorf("stderr = %q, want empty", errOut)
			}
			if out != tt.want {
				t.Errorf("stdout = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestCalcInfoIPv6JSONOmitsBroadcast(t *testing.T) {
	t.Parallel()

	state := &commandState{sanitizeLevel: "off"}
	root := newRootCommandWithDependencies(state, commandDependencies{
		getenv: func(string) string { return "" },
	})
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--format", "json", "calc", "info", "2001:db8:1::42/64"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v; stderr = %q", err, errBuf.String())
	}
	if strings.Contains(outBuf.String(), "broadcast") {
		t.Errorf("raw JSON %q carries a broadcast key for IPv6; want it omitted", outBuf.String())
	}
	if !strings.Contains(outBuf.String(), `"usable_hosts": "18446744073709551616"`) {
		t.Errorf("raw JSON %q missing exact string-encoded IPv6 count", outBuf.String())
	}
}

func TestCalcInfoInvalidCIDR(t *testing.T) {
	t.Parallel()

	code, out, errOut := runCalcInfoCLI(t, "not-a-cidr")

	if code != exitUsage {
		t.Errorf("exit code = %d, want %d (exitUsage)", code, exitUsage)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty on rejection", out)
	}
	if !strings.Contains(errOut, "not-a-cidr") {
		t.Errorf("stderr = %q, want to name the rejected argument", errOut)
	}
}

func TestCalcInfoNotSanitizedUnderIdentity(t *testing.T) {
	t.Parallel()

	// calc info output is pure arithmetic over a CLI-supplied CIDR: no
	// tailnet-derived identity, so nothing to sanitize (same contract as
	// calc split above).
	for _, format := range []string{"human", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			state := &commandState{sanitizeLevel: "identity"}
			root := newRootCommandWithDependencies(state, commandDependencies{
				getenv: func(name string) string {
					if name == "TAILSCALE_SANITIZE" {
						return "identity"
					}
					return ""
				},
			})
			var outBuf, errBuf bytes.Buffer
			root.SetOut(&outBuf)
			root.SetErr(&errBuf)
			root.SetArgs([]string{"--format", format, "calc", "info", "192.168.1.0/24"})
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v; stderr = %q", err, errBuf.String())
			}

			for _, want := range []string{"192.168.1.255", "192.168.1.254"} {
				if !strings.Contains(outBuf.String(), want) {
					t.Errorf("output missing unredacted %s: %s", want, outBuf.String())
				}
			}
		})
	}
}

func TestCalcAggregateHumanOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "adjacent prefixes merge",
			args: []string{"10.0.0.0/25", "10.0.0.128/25"},
			want: "10.0.0.0/24\n",
		},
		{
			name: "covered and disjoint prefixes are normalized and ordered",
			args: []string{"10.1.0.0/24", "10.0.0.1/24", "10.0.0.0/25"},
			want: "10.0.0.0/24\n10.1.0.0/24\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, err := runCalcAggregate(t, tt.args...)
			if err != nil {
				t.Fatalf("runCalcAggregate() error = %v (stderr = %q)", err, errOut)
			}
			if errOut != "" {
				t.Errorf("stderr = %q, want empty", errOut)
			}
			if out != tt.want {
				t.Errorf("stdout = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestCalcAggregateJSONOutput(t *testing.T) {
	t.Parallel()

	out, errOut, err := runCalcAggregate(t,
		"10.0.0.0/25", "10.0.0.128/25", "--format", formatJSON)
	if err != nil {
		t.Fatalf("runCalcAggregate() error = %v (stderr = %q)", err, errOut)
	}
	var envelope outputEnvelope
	if unmarshalErr := json.Unmarshal([]byte(out), &envelope); unmarshalErr != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", unmarshalErr, out)
	}
	if envelope.Command != commandCalcAggregate {
		t.Errorf("Command = %q, want %q", envelope.Command, commandCalcAggregate)
	}
	if envelope.Supernets == nil {
		t.Fatal("Supernets = nil, want non-nil")
	}
	if envelope.Supernets != nil && (len(*envelope.Supernets) != 1 || (*envelope.Supernets)[0] != "10.0.0.0/24") {
		t.Errorf("Supernets = %#v, want [10.0.0.0/24]", envelope.Supernets)
	}
}

func TestCalcAggregateMappedIPv4MatchesPlainIPv4(t *testing.T) {
	t.Parallel()

	mapped, mappedErr, mappedRunErr := runCalcAggregate(t, "::ffff:10.0.0.0/121", "::ffff:10.0.0.128/121")
	if mappedRunErr != nil {
		t.Fatalf("mapped aggregate error = %v (stderr = %q)", mappedRunErr, mappedErr)
	}
	plain, plainErr, plainRunErr := runCalcAggregate(t, "10.0.0.0/25", "10.0.0.128/25")
	if plainRunErr != nil {
		t.Fatalf("plain aggregate error = %v (stderr = %q)", plainRunErr, plainErr)
	}
	if mapped != plain {
		t.Errorf("mapped output = %q, want plain IPv4 output %q", mapped, plain)
	}
}

func TestCalcAggregateRejectsIPv6AndInvalidCIDR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cidr string
		want string
	}{
		{name: "native IPv6", cidr: "2001:db8::/64", want: "#253"},
		{name: "short mapped IPv6", cidr: "::ffff:10.0.0.0/95", want: "#253"},
		{name: "invalid CIDR", cidr: "not-a-cidr", want: "not-a-cidr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, out, errOut := runCalcAggregateCLI(t, tt.cidr)
			if code != exitUsage {
				t.Errorf("exit code = %d, want %d (exitUsage)", code, exitUsage)
			}
			if out != "" {
				t.Errorf("stdout = %q, want empty on refusal", out)
			}
			if !strings.Contains(errOut, tt.want) {
				t.Errorf("stderr = %q, want %q", errOut, tt.want)
			}
		})
	}
}

func TestCalcAggregateNotSanitizedUnderIdentity(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatHuman, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			state := &commandState{sanitizeLevel: "identity"}
			root := newRootCommandWithDependencies(state, commandDependencies{
				getenv: func(name string) string {
					if name == "TAILSCALE_SANITIZE" {
						return "identity"
					}
					return ""
				},
			})
			var outBuf, errBuf bytes.Buffer
			root.SetOut(&outBuf)
			root.SetErr(&errBuf)
			root.SetArgs([]string{"--format", format, "calc", "aggregate", "10.0.0.0/25", "10.0.0.128/25"})
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v; stderr = %q", err, errBuf.String())
			}
			if !strings.Contains(outBuf.String(), "10.0.0.0/24") {
				t.Errorf("output = %q, want unredacted aggregate CIDR", outBuf.String())
			}
		})
	}
}
