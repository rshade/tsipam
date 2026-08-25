package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
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

func TestCalcSplitIPv6Rejected(t *testing.T) {
	t.Parallel()

	// Test IPv6 rejection.
	out, errOut, err := runCalcSplit(t, "fd7a:115c:a1e0::/48", "--prefix", "64")
	if err == nil {
		t.Fatalf("runCalcSplit() error = nil, want error")
	}

	// Error should contain IPv6 message (could be in stdout or stderr)
	combined := out + errOut + err.Error()
	if !strings.Contains(combined, "IPv6") {
		t.Errorf("output = %q/%q, error = %q, want to contain %q", out, errOut, err, "IPv6")
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

func TestCalcSplitTooShort4in6Rejected(t *testing.T) {
	t.Parallel()

	// Test that a 4-in-6 address with Bits() < 96 (too few to represent valid IPv4 prefix)
	// is rejected with the same error message as plain IPv6.
	out, errOut, err := runCalcSplit(t, "::ffff:10.0.0.0/95", "--prefix", "100")
	if err == nil {
		t.Fatalf("runCalcSplit() error = nil, want error for too-short 4-in-6")
	}

	combined := out + errOut + err.Error()
	if !strings.Contains(combined, "IPv6") {
		t.Errorf("too-short 4-in-6 output = %q/%q, error = %q, want to contain %q",
			out, errOut, err, "IPv6")
	}

	// Verify it's the same error message as plain IPv6 rejection
	expectedMsg := "IPv6 is not yet supported for 'calc split'; use an IPv4 CIDR"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("error message = %q, want %q", err.Error(), expectedMsg)
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
