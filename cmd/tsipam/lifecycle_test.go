package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/diagnostics"
)

const (
	testTraceID     = "0af7651916cd43dd8448eb211c80319c"
	testTraceParent = "00-" + testTraceID + "-b7ad6b7169203331-01"
)

// runWith runs the command line with env and returns its exit code and streams.
func runWith(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(t.Context(), args, devicesEnv(env), &stdout, &stderr, nil)
	return code, stdout.String(), stderr.String()
}

// commandPaths lists every command that can run, as command-line words.
func commandPaths() [][]string {
	var paths [][]string
	root := invocation{stdout: io.Discard}.commandTree(&commandState{})
	walkCommands(root, func(cmd *cobra.Command) {
		paths = append(paths, strings.Fields(cmd.CommandPath())[1:])
	})
	return paths
}

// TestNoFailureEscapesTheBoundary drives every command into each kind of
// failure Cobra raises itself. An escaped one would report internal_error and
// exit 1, 3, or 4, which a caller could mistake for a verdict.
func TestNoFailureEscapesTheBoundary(t *testing.T) {
	t.Parallel()
	failures := map[string][]string{
		"unknown flag":   {"--no-such-flag"},
		"extra argument": {"extra-1", "extra-2", "extra-3"},
		"missing input":  {},
	}
	for _, path := range commandPaths() {
		for name, extra := range failures {
			t.Run(strings.Join(path, " ")+"/"+name, func(t *testing.T) {
				t.Parallel()
				assertBoundedFailure(t, slices.Concat(path, extra), name == "unknown flag")
			})
		}
	}
}

func assertBoundedFailure(t *testing.T, args []string, mustFail bool) {
	t.Helper()
	code, stdout, stderr := runWith(t, nil, args...)
	if code != exitUsage && (code != exitOK || mustFail) {
		t.Errorf("exit = %d, want %d; stderr: %s", code, exitUsage, stderr)
	}
	if strings.Contains(stderr, "internal_error") {
		t.Errorf("a failure escaped the boundary: %s", stderr)
	}
	if code == exitOK {
		return
	}
	failure := decodeFailure(t, stderr)
	if mustFail && failure.ErrorCode != codeValidationError {
		t.Errorf("error_code = %s, want %s", failure.ErrorCode, codeValidationError)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", stdout)
	}
}

func TestUsageFailures(t *testing.T) {
	t.Parallel()
	tests := map[string][]string{
		"missing required flag":     {commandCalc, "split", "10.0.0.0/24"},
		"built-in command argument": {"completion", "bash", "extra"},
		"unknown command":           {"bogus"},
		"no command":                {},
		"unknown format":            {commandSiteID, commandList, "--format", "bogus"},
		"unknown sanitize level":    {commandSiteID, commandList, "--sanitize", "bogus"},
		"unknown schema adapter":    {"__schema", "--as=bogus"},
		"schema argument":           {"__schema", "extra"},
		"bad flag value":            {commandCalc, "split", "10.0.0.0/24", "--prefix", "bogus"},
		"flag without a value":      {commandSiteID, commandList, "--tailnet"},
		"malformed CIDR":            {commandCalc, "info", "not-a-cidr"},
		"malformed pool":            {commandCalc, "next-free", "not-a-cidr", "--size", "24"},
		"malformed timestamp":       {commandSiteID, "reserve", "1", "hq", "--reserved-at", "bogus"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if len(args) > 0 && args[0] == commandSiteID {
				args = append(slices.Clone(args), "--registry-dir="+dir)
			}
			if name == "flag without a value" {
				args = tests[name]
			}
			code, stdout, stderr := runWith(t, nil, args...)
			failure := decodeFailure(t, stderr)
			if code != exitUsage || stdout != "" || failure.ErrorCode != codeValidationError {
				t.Errorf("exit = %d, stdout = %q, failure = %+v; want a %s",
					code, stdout, failure, codeValidationError)
			}
		})
	}
}

func TestBareCompletionRequestSucceeds(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runWith(t, nil, cobra.ShellCompRequestCmd)
	if code != exitOK || !strings.Contains(stdout, commandSiteID) {
		t.Errorf("exit = %d, stdout = %q, stderr = %q; want the command names", code, stdout, stderr)
	}
}

// TestFailuresBeforeTheCommandBodyAreSanitized covers errors raised while no
// command has had a chance to register the identities on its command line.
func TestFailuresBeforeTheCommandBodyAreSanitized(t *testing.T) {
	t.Parallel()
	const secret = "hq-secret"
	tests := map[string][]string{
		"stray argument":           {commandSiteID, commandList, secret},
		"unknown command":          {secret},
		"unknown flag":             {commandSiteID, commandList, "--" + secret},
		"unknown single-dash flag": {commandSiteID, commandList, "-x" + secret},
		"unknown flag value":       {commandSiteID, commandList, "--bogus=" + secret},
		"non-numeric flag value":   {commandCalc, "split", "10.0.0.0/24", "--prefix", secret},
		"non-boolean flag value":   {commandSiteID, commandList, "--dry-run=" + secret},
		"unknown format":           {commandSiteID, commandList, "--format", secret},
		"unknown schema adapter":   {"__schema", "--as", secret},
		"completion argument":      {"completion", "bash", secret},
		"malformed pool":           {commandCalc, "next-free", secret, "--size", "24"},
		"tab in an argument":       {commandSiteID, commandList, secret + "\tx"},
		"quote in an argument":     {commandSiteID, commandList, secret + `"x`},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{sanitize.LevelEnv: "identity"}
			assertSecretHidden(t, env, secret, args)
			assertSecretHidden(t, nil, secret, append([]string{"--sanitize", "identity"}, args...))
			assertSecretHidden(t, nil, secret, append(slices.Clone(args), "--sanitize=strict"))
		})
	}
}

func assertSecretHidden(t *testing.T, env map[string]string, secret string, args []string) {
	t.Helper()
	code, stdout, stderr := runWith(t, env, args...)
	if code != exitUsage || stdout != "" {
		t.Errorf("%v: exit = %d, stdout = %q; want %d and empty", args, code, stdout, exitUsage)
	}
	for _, text := range []string{stderr, failureText(t, stderr)} {
		if strings.Contains(text, secret) {
			t.Errorf("%v: stderr exposes %q: %s", args, secret, text)
		}
	}
}

// TestInvalidSanitizeKeyIsReported keeps a bad key diagnosable: the failure it
// hides may itself be the only other message.
func TestInvalidSanitizeKeyIsReported(t *testing.T) {
	t.Parallel()
	env := map[string]string{sanitize.LevelEnv: "identity", sanitize.KeyEnv: "short"}
	code, _, stderr := runWith(t, env, commandSiteID, commandList, "hq-secret")
	failure := decodeFailure(t, stderr)
	if code != exitUsage || !strings.Contains(failure.Message, sanitize.KeyEnv) ||
		strings.Contains(failure.Message, "hq-secret") {
		t.Errorf("exit = %d, message = %q; want the key named and the argument hidden", code, failure.Message)
	}
}

func TestCompletionScriptGoesToTheGivenStdout(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runWith(t, nil, "completion", "bash")
	if code != exitOK || !strings.Contains(stdout, "__tsipam_") || stderr != "" {
		t.Errorf("exit = %d, %d bytes on stdout, stderr = %q; want the script", code, len(stdout), stderr)
	}
}

func TestFailureContinuesTheCallersTrace(t *testing.T) {
	t.Parallel()
	env := map[string]string{"TRACEPARENT": testTraceParent}
	for name, args := range map[string][]string{
		"command body": {commandCalc, "info", "not-a-cidr"},
		"usage":        {commandSiteID, commandList, "extra"},
		"flag enum":    {commandSiteID, commandList, "--format", "bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, stderr := runWith(t, env, args...)
			if failure := decodeFailure(t, stderr); failure.TraceID != testTraceID {
				t.Errorf("trace_id = %s, want %s", failure.TraceID, testTraceID)
			}
		})
	}
}

// debugSpan is the part of an AX_OTEL_DEBUG span dump these tests read.
type debugSpan struct {
	Name        string       `json:"Name"`
	SpanContext debugSpanRef `json:"SpanContext"`
	Parent      debugSpanRef `json:"Parent"`
}

type debugSpanRef struct {
	TraceID string `json:"TraceID"`
	SpanID  string `json:"SpanID"`
}

func decodeSpans(t *testing.T, stderr string) map[string]debugSpan {
	t.Helper()
	spans := map[string]debugSpan{}
	decoder := json.NewDecoder(strings.NewReader(stderr))
	for {
		var span debugSpan
		err := decoder.Decode(&span)
		if errors.Is(err, io.EOF) {
			return spans
		}
		if err != nil {
			t.Fatalf("stderr holds more than spans: %v\n%s", err, stderr)
		}
		spans[span.Name] = span
	}
}

// TestServiceCallIsAChildOfTheCommandSpan is not parallel: the runtime's tracer
// provider is process-wide, and another run would replace it.
//
//nolint:paralleltest // see the doc comment
func TestServiceCallIsAChildOfTheCommandSpan(t *testing.T) {
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, localTailnet, `{"siteIDs":{"1":"hq"}}`)
	args := []string{commandSiteID, commandList, "--registry-dir", dir, "--format", formatHuman}
	_, plain, _ := runWith(t, nil, args...)
	code, stdout, stderr := runWith(t, map[string]string{"AX_OTEL_DEBUG": "1"}, args...)
	if code != exitOK || stdout != plain {
		t.Fatalf("exit = %d, stdout = %q; want %q", code, stdout, plain)
	}
	spans := decodeSpans(t, stderr)
	command, call := spans["tsipam site-id list"], spans["list_site_ids"]
	if call.Name == "" || call.Parent.SpanID != command.SpanContext.SpanID ||
		call.SpanContext.TraceID != command.SpanContext.TraceID {
		t.Errorf("spans = %+v, want list_site_ids as a child of the command", spans)
	}
}

// TestDebugSpanDumpHidesIdentities is not parallel for the same reason.
//
//nolint:paralleltest // see TestServiceCallIsAChildOfTheCommandSpan
func TestDebugSpanDumpHidesIdentities(t *testing.T) {
	const registryText = `{
		"siteIDs": {"1": {"site": "site-private", "owner": "owner-private"}},
		"subnetPools": {"10.77.0.0/16": "site-private"},
		"serviceAddresses": {"100.100.77.7": "svc:service-private"},
		"udpPorts": {"site-private": {"41642": "device-private"}}
	}`
	dir := filepath.Join(t.TempDir(), "path-private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeAggregateRegistry(t, dir, "tailnet-private", registryText)
	env := map[string]string{"AX_OTEL_DEBUG": "1", sanitize.LevelEnv: "identity"}
	collisions := map[string][]string{
		commandSiteIDReserve:  {commandSiteID, "reserve", "1", "wanted-private"},
		commandSubnetReserve:  {commandSubnet, "reserve", "10.77.1.0/24", "wanted-private"},
		commandServiceReserve: {commandService, "reserve", "100.100.77.7", "wanted-private"},
		commandPortReserve:    {commandPort, "reserve", "41642", "wanted-private", "--site", "site-private"},
		"stray argument":      {commandSiteID, commandList, "wanted-private"},
		"missing registry":    {commandSiteID, commandList, "--registry-dir", dir, "--tailnet", "absent-private"},
	}
	for name, args := range collisions {
		t.Run(name, func(t *testing.T) {
			if name != "missing registry" {
				args = append(args, "--registry-dir", dir, "--tailnet", "tailnet-private", "--verbose")
			}
			code, _, stderr := runWith(t, env, args...)
			if code == exitOK || !strings.Contains(stderr, `"SpanContext"`) {
				t.Fatalf("exit = %d, want a failure with a span dump: %s", code, stderr)
			}
			for _, secret := range []string{"private", "10.77.", "100.100.77."} {
				if strings.Contains(stderr, secret) {
					t.Errorf("the dump exposes %q: %s", secret, stderr)
				}
			}
		})
	}
}

// TestTelemetryNeverLeavesTheMachine sets the exporter endpoint in the process
// environment too, where the OpenTelemetry SDK would read it directly.
func TestTelemetryNeverLeavesTheMachine(t *testing.T) {
	var requests atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer collector.Close()
	t.Setenv(otlpEndpointEnv, collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", collector.URL)

	var stdout, stderr bytes.Buffer
	code := runCLI(t.Context(), []string{commandCalc, "info", "10.0.0.0/24"}, os.Getenv, &stdout, &stderr, nil)
	if code != exitOK || stderr.Len() != 0 {
		t.Errorf("exit = %d, stderr = %q; want a quiet success", code, stderr.String())
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("the collector received %d requests, want none", got)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// TestStrictChangesNothing covers --strict, which no command can escalate yet.
func TestStrictChangesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := policyFile(t, dir, `{"ipsets":{"ipset:lab":["10.0.0.0/24"]}}`)
	writeAggregateRegistry(t, dir, localTailnet, `{"subnetPools":{"10.9.0.0/16":"hq"}}`)
	for _, extra := range [][]string{nil, {"--strict"}} {
		t.Run(strings.Join(extra, ""), func(t *testing.T) {
			t.Parallel()
			args := append(
				[]string{commandCheckPolicy, policy, "--registry-dir", dir, "--format", formatHuman},
				extra...)
			code, stdout, stderr := runWith(t, nil, args...)
			failure := decodeFailure(t, stderr)
			if code != exitFailure || !strings.Contains(stdout, "drift_count=2") ||
				failure.ErrorCode != codePolicyDriftDetected || failure.Context[contextDriftCount] != float64(2) {
				t.Errorf(
					"exit = %d, stdout = %q, failure = %+v; want the report and the verdict",
					code,
					stdout,
					failure,
				)
			}

			list := append([]string{commandSubnet, commandList, "--registry-dir", dir}, extra...)
			var errBuf bytes.Buffer
			code = runCLI(t.Context(), list, emptyEnvironment, brokenWriter{}, &errBuf, nil)
			if failure = decodeFailure(t, errBuf.String()); code != exitUsage || failure.ErrorCode != codeOutputFailed {
				t.Errorf("full device: exit = %d, failure = %+v; want %s", code, failure, codeOutputFailed)
			}

			code, stdout, stderr = runWith(t, nil, list...)
			if code != exitOK || stdout == "" || stderr != "" {
				t.Errorf("success: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
			}
		})
	}
}

// TestStrictKeepsTheRootsResult covers the one command that has no parent.
func TestStrictKeepsTheRootsResult(t *testing.T) {
	t.Parallel()
	_, plain, _ := runWith(t, nil, "-schema")
	code, strict, stderr := runWith(t, nil, "--strict", "-schema")
	if code != exitOK || strict != plain || stderr != "" {
		t.Errorf("exit = %d, %d bytes, stderr = %q; want the same %d bytes", code, len(strict), stderr, len(plain))
	}
}

// TestOutputFailureHasOneCode covers stdout writes outside emitOutput.
func TestOutputFailureHasOneCode(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"result":          {commandCalc, "info", "10.0.0.0/24"},
		"-schema":         {"-schema"},
		"strict -schema":  {"--strict", "-schema"},
		"__schema":        {"__schema"},
		"strict __schema": {"--strict", "__schema"},
		"completion":      {"completion", "bash"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			code := runCLI(t.Context(), args, emptyEnvironment, brokenWriter{}, &stderr, nil)
			if failure := decodeFailure(
				t,
				stderr.String(),
			); code != exitUsage ||
				failure.ErrorCode != codeOutputFailed {
				t.Errorf("exit = %d, failure = %+v; want %s", code, failure, codeOutputFailed)
			}
		})
	}
}

func TestCanceledRunHasItsOwnCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, localTailnet, `{}`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stdout, stderr bytes.Buffer
	code := runCLI(ctx, []string{commandSiteID, commandList, "--registry-dir", dir},
		emptyEnvironment, &stdout, &stderr, nil)
	if failure := decodeFailure(t, stderr.String()); code != exitUsage || failure.ErrorCode != codeCanceled {
		t.Errorf("exit = %d, failure = %+v; want %s", code, failure, codeCanceled)
	}
}

// logEvents decodes the structured log lines on stderr.
func logEvents(t *testing.T, stderr string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for line := range strings.Lines(stderr) {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) == nil && event["level"] != nil {
			events = append(events, event)
		}
	}
	return events
}

func hasLogEvent(events []map[string]any, level, message string) bool {
	return slices.ContainsFunc(events, func(event map[string]any) bool {
		return event["level"] == level && event["message"] == message
	})
}

func TestLogLinesAreStructuredAndQuietByDefault(t *testing.T) {
	t.Parallel()
	listDevices := func(_ context.Context, logger diagnostics.Logger) ([]deviceRecord, error) {
		diagnostics.Debug(logger, "fixture detail", "count", 1)
		diagnostics.Warn(logger, "fixture warning", "error", errors.New("boom"))
		return nil, nil
	}
	env := devicesEnv(map[string]string{"TRACEPARENT": testTraceParent})
	for _, verbose := range []bool{false, true} {
		args := []string{commandDevices, commandList}
		if verbose {
			args = append(args, "--verbose")
		}
		var stdout, stderr bytes.Buffer
		if code := runCLI(t.Context(), args, env, &stdout, &stderr, listDevices); code != exitOK {
			t.Fatalf("exit = %d: %s", code, stderr.String())
		}
		events := logEvents(t, stderr.String())
		if !hasLogEvent(events, "warn", "fixture warning") ||
			hasLogEvent(events, "debug", "fixture detail") != verbose {
			t.Errorf("verbose = %t logged %v", verbose, events)
		}
		for _, event := range events {
			assertFixtureEvent(t, event)
		}
	}

	code, _, stderr := runWith(t, nil, commandSiteID, "reserve", "1", "hq", "--registry-dir", t.TempDir())
	if code != exitOK || stderr != "" {
		t.Errorf("default run: exit = %d, stderr = %q; want quiet", code, stderr)
	}
}

// assertFixtureEvent checks that a line keeps its trace ID and attributes.
func assertFixtureEvent(t *testing.T, event map[string]any) {
	t.Helper()
	if event["trace_id"] != testTraceID {
		t.Errorf("event %v lacks the run's trace ID", event)
	}
	if event["message"] == "fixture detail" && event["count"] != float64(1) {
		t.Errorf("event %v lost its attribute", event)
	}
	if event["message"] == "fixture warning" && event["error"] != "boom" {
		t.Errorf("event %v lost its error", event)
	}
}
