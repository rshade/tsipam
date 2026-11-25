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
)

func TestCalcNextFreeIPv6HumanOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "large pool returns one bare CIDR",
			args: []string{"2001:db8::/32", "--size", "48"},
			want: "2001:db8::/48\n",
		},
		{
			name: "host bits are masked",
			args: []string{"2001:db8:abcd::1234/48", "--size", "64"},
			want: "2001:db8:abcd::/64\n",
		},
		{
			name: "count returns ordered children",
			args: []string{"2001:db8::/32", "--size", "48", "--count", "3"},
			want: "2001:db8::/48\n2001:db8:1::/48\n2001:db8:2::/48\n",
		},
		{
			name: "all returns both /128 children",
			args: []string{"2001:db8::/127", "--size", "128", "--all"},
			want: "2001:db8::/128\n2001:db8::1/128\n",
		},
		{
			name: "count stops at exhaustion",
			args: []string{"2001:db8::/128", "--size", "128", "-n", "3"},
			want: "2001:db8::/128\n",
		},
		{
			name: "maximum /127 does not wrap",
			args: []string{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe/127", "--size", "127", "--all"},
			want: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe/127\n",
		},
		{
			name: "mapped IPv4 remains normalized",
			args: []string{"::ffff:10.0.0.1/120", "--size", "26"},
			want: "10.0.0.0/26\n",
		},
		{
			name: "short mapped prefix remains IPv6",
			args: []string{"::ffff:10.0.0.1/80", "--size", "96"},
			want: "::/96\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeNextFreeRegistry(t, dir, `{}`)
			out, errOut, err := runCalcNextFree(t, dir, nil, test.args...)
			if err != nil {
				t.Fatalf("next-free: %v (stderr = %q)", err, errOut)
			}
			if out != test.want || errOut != "" {
				t.Errorf("stdout = %q, stderr = %q; want %q, empty", out, errOut, test.want)
			}
		})
	}
}

func TestCalcNextFreeMixedFamilyExclusions(t *testing.T) {
	t.Parallel()
	const registry = `{
  // Preserve the operator's registry bytes during planning.
  "subnetPools": {"10.0.0.0/26":"v4", "2001:db8::/48":"v6"}
}`
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "IPv6 search",
			args: []string{"2001:db8::/32", "--size", "48"},
			want: "2001:db8:2::/48\n",
		},
		{
			name: "IPv4 search",
			args: []string{"10.0.0.0/24", "--size", "26"},
			want: "10.0.0.128/26\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeNextFreeRegistry(t, dir, registry)
			devices := []deviceRecord{{AdvertisedRoutes: []string{
				"10.0.0.64/26", "2001:db8:1::/48", "2001:db9::/32",
			}}}
			out, errOut, err := runCalcNextFree(t, dir, devices, test.args...)
			if err != nil {
				t.Fatalf("next-free: %v (stderr = %q)", err, errOut)
			}
			if out != test.want || errOut != "" {
				t.Errorf("stdout = %q, stderr = %q; want %q, empty", out, errOut, test.want)
			}
			after, readErr := os.ReadFile(filepath.Join(dir, "local.ipam.hujson"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(after) != registry {
				t.Errorf("registry changed: %s", after)
			}
		})
	}
}

func TestCalcNextFreeIPv6JSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeNextFreeRegistry(t, dir, `{}`)
	out, errOut, err := runCalcNextFree(t, dir, nil,
		"2001:db8::/127", "--size", "128", "--all", "--format", formatJSON)
	if err != nil {
		t.Fatalf("next-free: %v (stderr = %q)", err, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want empty", errOut)
	}
	var result outputEnvelope
	if decodeErr := decodeData([]byte(out), &result); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if result.SchemaVersion != outputSchemaVersion ||
		result.Command != commandCalcNextFree || result.Status != statusOK {
		t.Errorf("unexpected envelope: %s", out)
	}
	want := []string{"2001:db8::/128", "2001:db8::1/128"}
	if result.FreeBlocks == nil || !slices.Equal(*result.FreeBlocks, want) ||
		result.Message != "suggested 2 free block(s)" {
		t.Errorf("result = %s, want blocks %v", out, want)
	}
}

// TestCalcNextFreeIPv6JSONExhaustion covers the exhaustion path for an IPv6
// pool under --format json: exit 2, empty stdout, message on stderr. See
// TestCalcNextFreeIPv6ExhaustionSanitizesStderr for the sanitized variant and
// TestCalcNextFreeMultipleAndExhaustedOutput for the IPv4 case.
func TestCalcNextFreeIPv6JSONExhaustion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeNextFreeRegistry(t, dir, `{"subnetPools":{"2001:db8::/127":"reserved"}}`)
	code, out, errOut := runCalcNextFreeCLI(t, dir, nil,
		"2001:db8::/127", "--size", "128", "--all", "--format", formatJSON)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d (exitUsage)", code, exitUsage)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty on exhaustion", out)
	}
	if !strings.Contains(errOut, "no free /128 block in 2001:db8::/127") {
		t.Errorf("stderr = %q, want exhaustion explanation", errOut)
	}
}

func TestCalcNextFreeIPv6Sanitization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		format   string
		registry string
	}{
		{name: "human suggestion", format: formatHuman, registry: `{}`},
		{name: "JSON suggestion", format: formatJSON, registry: `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeNextFreeRegistry(t, dir, test.registry)
			out, errOut, err := runCalcNextFree(t, dir, nil,
				"2001:db8::1/128", "--size", "128", "--format", test.format, "--sanitize", "identity")
			if err != nil {
				t.Fatalf("next-free: %v (stderr = %q)", err, errOut)
			}
			if errOut != "tsipam: sanitize identity is active; output is sanitized\n" {
				t.Errorf("stderr = %q, want sanitization notice", errOut)
			}
			if strings.Contains(out, "2001:db8") || !strings.Contains(out, "cidr-") {
				t.Errorf("expected sanitized CIDR: %s", out)
			}
			if test.format == formatJSON && !json.Valid([]byte(out)) {
				t.Errorf("invalid JSON: %s", out)
			}
		})
	}
}

// TestCalcNextFreeIPv6ExhaustionSanitizesStderr exercises the same identity
// sanitization as TestCalcNextFreeIPv6Sanitization, but for the exhaustion
// path: since exhaustion returns before emitOutput ever runs (see
// executeCalcNextFree), the pool CIDR is only sanitized by runCLI's
// sanitizeErrorIfNeeded on the way to real stderr -- root.Execute() alone
// (as runCalcNextFree uses) never exercises that path, so this needs runCLI.
func TestCalcNextFreeIPv6ExhaustionSanitizesStderr(t *testing.T) {
	t.Parallel()
	for _, format := range []string{formatHuman, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeNextFreeRegistry(t, dir, `{"subnetPools":{"2001:db8::1/128":"reserved"}}`)
			code, out, errOut := runCalcNextFreeCLI(t, dir, nil,
				"2001:db8::1/128", "--size", "128", "--format", format, "--sanitize", "identity")
			if code != exitUsage {
				t.Errorf("exit code = %d, want %d (exitUsage)", code, exitUsage)
			}
			if out != "" {
				t.Errorf("stdout = %q, want empty on exhaustion", out)
			}
			if !strings.Contains(errOut, "no free /128 block") {
				t.Errorf("stderr = %q, want exhaustion explanation", errOut)
			}
			if strings.Contains(errOut, "2001:db8") || !strings.Contains(errOut, "cidr-") {
				t.Errorf("expected sanitized CIDR in stderr: %s", errOut)
			}
		})
	}
}

func TestCalcNextFreeIPv6InvalidTargetExitCode(t *testing.T) {
	t.Parallel()
	for _, size := range []string{"31", "129"} {
		t.Run(size, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			code := runCLI(context.Background(),
				[]string{"calc", "next-free", "2001:db8::/32", "--size", size},
				func(string) string { return "" }, &out, &errOut, nil)
			if code != 2 || out.Len() != 0 ||
				!strings.Contains(errOut.String(), "must be between pool /32 and address length /128") {
				t.Errorf("exit = %d, stdout = %q, stderr = %q", code, out.String(), errOut.String())
			}
		})
	}
}
