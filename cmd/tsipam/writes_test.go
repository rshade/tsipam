package main

import (
	"bytes"
	"context"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

const promptInterruptDelay = 50 * time.Millisecond

// dryRunCase is one write, rehearsed against writesRegistry.
type dryRunCase struct {
	args []string
	// would is how the rehearsal's human message begins.
	would string
	// repeat is a write that changes nothing.
	repeat []string
	// refused is a write the registry refuses, with the code it fails with.
	refused     []string
	refusedCode string
}

const writesRegistry = `{
	// Kept as written.
	"siteIDs": {"1": "hq"},
	"subnetPools": {"10.0.0.0/24": {"site": "hq", "ticket": "OPS-1", "reservedAt": "2026-11-25T12:00:00Z"}},
	"serviceAddresses": {"100.100.100.7": "svc:payments"},
	"udpPorts": {"hq": {"41642": "fw01"}}
}
`

// dryRunCases covers every command whose contract says it writes.
func dryRunCases() map[string]dryRunCase {
	return map[string]dryRunCase{
		commandSiteIDReserve: {
			args: []string{"2", "branch"}, would: "dry run: would reserve site ID 2 for \"branch\"",
			repeat: []string{"1", "hq"}, refused: []string{"1", "branch"}, refusedCode: codeRegistryCollision,
		},
		commandSiteIDRelease: {
			args: []string{"1"}, would: "dry run: would release site ID 1",
			repeat: []string{"9"}, refused: []string{"65536"}, refusedCode: codeValidationError,
		},
		commandSubnetReserve: {
			args: []string{"10.1.0.0/24", "branch"}, would: "dry run: would reserve subnet CIDR 10.1.0.0/24",
			repeat: []string{"10.0.0.0/24", "hq"}, refused: []string{"10.0.0.0/25", "branch"},
			refusedCode: codeRegistryCollision,
		},
		commandSubnetAllocate: {
			args:    []string{"10.0.0.0/16", "branch", "--size", "24"},
			would:   "dry run: would allocate subnet CIDR 10.0.2.0/24",
			repeat:  []string{"10.0.0.0/16", "hq", "--size", "24", "--ticket", "OPS-1"},
			refused: []string{"10.0.0.0/24", "branch", "--size", "24"}, refusedCode: codePoolExhausted,
		},
		commandSubnetRelease: {
			args: []string{"10.0.0.0/24"}, would: "dry run: would release subnet CIDR 10.0.0.0/24",
			repeat: []string{"10.9.0.0/24"}, refused: []string{"10.0.0.1/24"}, refusedCode: codeValidationError,
		},
		commandServiceReserve: {
			args: []string{"100.100.100.8", "search"}, would: "dry run: would reserve service address 100.100.100.8",
			repeat: []string{"100.100.100.7", "payments"}, refused: []string{"100.100.100.7", "search"},
			refusedCode: codeRegistryCollision,
		},
		commandServiceRelease: {
			args: []string{"100.100.100.7"}, would: "dry run: would release service address 100.100.100.7",
			repeat: []string{"100.100.100.9"}, refused: []string{"100.100.100.0/24"}, refusedCode: codeValidationError,
		},
		commandPortReserve: {
			args: []string{"41643", "fw02", "--site", "hq"}, would: "dry run: would reserve UDP port 41643",
			repeat: []string{"41642", "fw01", "--site", "hq"}, refused: []string{"41642", "fw02", "--site", "hq"},
			refusedCode: codeRegistryCollision,
		},
		commandPortRelease: {
			args: []string{"41642", "--site", "hq"}, would: "dry run: would release UDP port 41642",
			repeat: []string{"41650", "--site", "hq"}, refused: []string{"80", "--site", "hq"},
			refusedCode: codeValidationError,
		},
	}
}

func TestEveryWriteHasADryRunCase(t *testing.T) {
	t.Parallel()
	var writes []string
	for name, contract := range leafContracts() {
		if contract.writes() {
			writes = append(writes, name)
		}
	}
	slices.Sort(writes)
	if covered := slices.Sorted(maps.Keys(dryRunCases())); !slices.Equal(writes, covered) {
		t.Errorf("commands that write are %v, dry-run cases cover %v", writes, covered)
	}
}

// TestCapabilityClassesArePublished pins each class through __schema, so a class
// cannot change or disappear unnoticed.
func TestCapabilityClassesArePublished(t *testing.T) {
	t.Parallel()
	for name, contract := range leafContracts() {
		want := "read-only"
		switch name[strings.LastIndex(name, " ")+1:] {
		case "reserve":
			want = "mutate"
		case "allocate":
			want = "create"
		case "release":
			want = "delete"
		}
		if got := publishedCommandAt(t, name).Capability.Class; got != want || string(contract.class) != want {
			t.Errorf("%s publishes class %q and declares %q, want %q", name, got, contract.class, want)
		}
	}
}

// writeRun is one run against a fresh copy of the registry.
type writeRun struct {
	code           int
	stdout, stderr string
	before, after  map[string]string
}

func (r writeRun) unchanged() bool { return maps.Equal(r.before, r.after) }

// runWrite runs the command name with args in a directory holding registry, or
// an empty one when registry is "".
func runWrite(t *testing.T, env map[string]string, registry, name string, args ...string) writeRun {
	t.Helper()
	dir := t.TempDir()
	if registry != "" {
		writeAggregateRegistry(t, dir, localTailnet, registry)
	}
	return runWriteIn(t, dir, env, name, args...)
}

func runWriteIn(t *testing.T, dir string, env map[string]string, name string, args ...string) writeRun {
	t.Helper()
	listDevices := func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
		return []deviceRecord{{Name: "router", AdvertisedRoutes: []string{"10.0.1.0/24"}}}, nil
	}
	run := writeRun{before: directoryFiles(t, dir)}
	var stdout, stderr bytes.Buffer
	run.code = runCLI(t.Context(), slices.Concat(strings.Fields(name), args, []string{"--registry-dir", dir}),
		devicesEnv(env), &stdout, &stderr, listDevices)
	run.stdout, run.stderr, run.after = stdout.String(), stderr.String(), directoryFiles(t, dir)
	return run
}

func directoryFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string, len(entries))
	for _, entry := range entries {
		raw, readErr := os.ReadFile(dir + "/" + entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		files[entry.Name()] = string(raw)
	}
	return files
}

// decodeRun returns a successful JSON run's result and whether meta marks it as
// a rehearsal.
func decodeRun(t *testing.T, run writeRun) (outputEnvelope, bool) {
	t.Helper()
	if run.code != exitOK {
		t.Fatalf("exit = %d: %s", run.code, run.stderr)
	}
	document, err := decodeDocument([]byte(run.stdout))
	if err != nil {
		t.Fatal(err)
	}
	var data outputEnvelope
	if err = decodeData([]byte(run.stdout), &data); err != nil {
		t.Fatal(err)
	}
	dryRun, _ := document.Meta["dry_run"].(bool)
	return data, dryRun
}

func TestDryRunReportsTheOutcomeAndWritesNothing(t *testing.T) {
	t.Parallel()
	for name, test := range dryRunCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rehearsed := runWrite(t, nil, writesRegistry, name, append(test.args, "--dry-run")...)
			data, dryRun := decodeRun(t, rehearsed)
			if !dryRun || data.Changed == nil || !*data.Changed || !rehearsed.unchanged() {
				t.Errorf("rehearsal = %+v, want changed, marked, and nothing written", rehearsed)
			}
			committed := runWrite(t, nil, writesRegistry, name, append(test.args, "--yes")...)
			actual, actualDryRun := decodeRun(t, committed)
			if actualDryRun || committed.unchanged() {
				t.Errorf("real run = %+v, want a write and no mark", committed)
			}
			data.Message, actual.Message = "", ""
			if got, want := marshalOutput(t, data), marshalOutput(t, actual); got != want {
				t.Errorf("rehearsed data %s, real data %s", got, want)
			}

			human := runWrite(t, nil, writesRegistry, name, append(test.args, "--dry-run", "--format", formatHuman)...)
			if human.code != exitOK || !strings.HasPrefix(human.stdout, test.would) || !human.unchanged() {
				t.Errorf("human rehearsal = %+v, want %q", human, test.would)
			}
		})
	}
}

func TestDryRunOfANoOp(t *testing.T) {
	t.Parallel()
	for name, test := range dryRunCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := runWrite(t, nil, writesRegistry, name, append(test.repeat, "--dry-run")...)
			data, dryRun := decodeRun(t, run)
			if !dryRun || data.Changed == nil || *data.Changed || !run.unchanged() ||
				!strings.HasPrefix(data.Message, "dry run: would") {
				t.Errorf("rehearsal = %+v, want unchanged", run)
			}
		})
	}
}

func TestDryRunRefusals(t *testing.T) {
	t.Parallel()
	for name, test := range dryRunCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := runWrite(t, nil, writesRegistry, name, append(test.refused, "--dry-run")...)
			failure := decodeFailure(t, run.stderr)
			if failure.ErrorCode != test.refusedCode || run.code != (failureExit(test.refusedCode)) ||
				run.stdout != "" || !run.unchanged() {
				t.Errorf("rehearsal = %+v, want %s", run, test.refusedCode)
			}
		})
	}
}

func failureExit(code string) int { return failure{code: code}.exitCode() }

func TestDryRunWithoutARegistry(t *testing.T) {
	t.Parallel()
	for name, test := range dryRunCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := runWrite(t, nil, "", name, append(test.args, "--dry-run")...)
			if len(run.after) != 0 {
				t.Errorf("the rehearsal left %v", run.after)
			}
			if contractedRelease(name) {
				if failure := decodeFailure(
					t,
					run.stderr,
				); run.code != exitUsage ||
					failure.ErrorCode != codeRegistryFailed {
					t.Errorf("run = %+v, want %s", run, codeRegistryFailed)
				}
				return
			}
			if data, _ := decodeRun(t, run); data.Changed == nil || !*data.Changed {
				t.Errorf("run = %+v, want changed", run)
			}
		})
	}
}

func contractedRelease(name string) bool { return leafContracts()[name].removes() }

func TestDryRunExhaustionCreatesNothing(t *testing.T) {
	t.Parallel()
	run := runWrite(t, nil, "", commandSubnetAllocate, "10.0.1.0/24", "hq", "--size", "24", "--dry-run")
	if failure := decodeFailure(t, run.stderr); run.code != exitUsage || failure.ErrorCode != codePoolExhausted ||
		len(run.after) != 0 {
		t.Errorf("run = %+v, want %s and no file", run, codePoolExhausted)
	}
}

// TestRehearsedBlockIsNotHeld proves a rehearsal reserves nothing for the run
// that follows it.
func TestRehearsedBlockIsNotHeld(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	allocate := func(site string, extra ...string) string {
		data, _ := decodeRun(t, runWriteIn(t, dir, nil, commandSubnetAllocate,
			append([]string{"10.0.0.0/16", site, "--size", "24"}, extra...)...))
		return data.CIDR
	}
	rehearsed := allocate("hq", "--dry-run")
	if other := allocate("branch"); other != rehearsed {
		t.Fatalf("another caller got %s, want the rehearsed %s", other, rehearsed)
	}
	if got := allocate("hq"); got == rehearsed {
		t.Errorf("the real run returned %s, which another caller holds", got)
	}
}

func TestDryRunOnAReadOnlyCommand(t *testing.T) {
	t.Parallel()
	for _, name := range []string{commandSiteIDList, commandSubnetList, commandServiceList, commandPortList} {
		plain, _ := decodeRun(t, runWrite(t, nil, writesRegistry, name))
		run := runWrite(t, nil, writesRegistry, name, "--dry-run")
		rehearsed, marked := decodeRun(t, run)
		if !marked || marshalOutput(t, plain) != marshalOutput(t, rehearsed) || run.stderr != "" {
			t.Errorf("%s --dry-run = %+v, want the same data", name, run)
		}
	}
}

// TestWritesAreAuditedOnlyWhenVerbose covers the lines around a write: none by
// default, one notice for a rehearsal, and an identity-free pair with --verbose.
func TestWritesAreAuditedOnlyWhenVerbose(t *testing.T) {
	t.Parallel()
	for name, test := range dryRunCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			quiet := runWrite(t, nil, writesRegistry, name, append(test.args, "--yes")...)
			if quiet.code != exitOK || quiet.stderr != "" {
				t.Errorf("default run = %+v, want quiet", quiet)
			}
			rehearsed := runWrite(t, nil, writesRegistry, name, append(test.args, "--dry-run")...)
			if events := logEvents(t, rehearsed.stderr); len(events) != 1 ||
				!hasLogEvent(events, "info", "dry-run: side effect suppressed") {
				t.Errorf("rehearsal logged %v, want one notice", events)
			}
			verbose := runWrite(t, nil, writesRegistry, name, append(test.args, "--yes", "--verbose")...)
			assertAudited(t, logEvents(t, verbose.stderr), name)
		})
	}
}

func assertAudited(t *testing.T, events []map[string]any, name string) {
	t.Helper()
	if !hasLogEvent(events, "info", "ax: about to run effect") || !hasLogEvent(events, "info", "ax: effect succeeded") {
		t.Errorf("verbose run logged %v, want the audit pair", events)
	}
	for _, event := range events {
		if description, audited := event["description"]; audited && description != name {
			t.Errorf("audit description = %v, want %q", description, name)
		}
	}
}

func TestAuditedFailureIsSanitized(t *testing.T) {
	t.Parallel()
	env := map[string]string{sanitize.LevelEnv: "identity"}
	run := runWrite(t, env, strings.ReplaceAll(writesRegistry, `"hq"`, `"hq-secret"`),
		commandSiteIDReserve, "1", "branch-secret", "--verbose")
	events := logEvents(t, run.stderr)
	if run.code != exitFailure || !hasLogEvent(events, "info", "ax: about to run effect") ||
		!hasLogEvent(events, "error", "ax: effect failed") {
		t.Fatalf("run = %+v, want an audited refusal", run)
	}
	for _, secret := range []string{"hq-secret", "branch-secret", `"tailnet"`} {
		if strings.Contains(run.stderr, secret) {
			t.Errorf("stderr exposes %q: %s", secret, run.stderr)
		}
	}
}

// approvalCase is one way of running a release.
type approvalCase struct {
	name     string
	args     []string
	terminal bool
	stdin    string
	approved bool
	prompted bool
}

func approvalCases() []approvalCase {
	human := []string{"--format", formatHuman}
	return []approvalCase{
		{name: "JSON"},
		{name: "JSON at a terminal", args: []string{"--format", formatJSON}, terminal: true, stdin: "y\n"},
		{name: "JSON with yes", args: []string{"--yes"}, approved: true},
		{name: "human without a terminal", args: human, stdin: "y\n"},
		{name: "human with yes", args: append([]string{"--yes"}, human...), approved: true},
		{name: "terminal answer y", args: human, terminal: true, stdin: "y\n", prompted: true, approved: true},
		{name: "terminal answer YES", args: human, terminal: true, stdin: " YES \n", prompted: true, approved: true},
		{name: "terminal answer n", args: human, terminal: true, stdin: "n\n", prompted: true},
		{name: "terminal empty answer", args: human, terminal: true, stdin: "\n", prompted: true},
		{name: "terminal other answer", args: human, terminal: true, stdin: "yep\n", prompted: true},
		{name: "terminal end of input", args: human, terminal: true, prompted: true},
		{name: "terminal cut-off answer", args: human, terminal: true, stdin: "y", prompted: true},
	}
}

func TestReleaseNeedsApproval(t *testing.T) {
	t.Parallel()
	for name, write := range dryRunCases() {
		if !contractedRelease(name) {
			continue
		}
		for _, test := range approvalCases() {
			t.Run(name+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				assertApproval(t, name, write.args, test)
			})
		}
	}
}

func assertApproval(t *testing.T, name string, args []string, test approvalCase) {
	t.Helper()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, localTailnet)
	writeAggregateRegistry(t, dir, localTailnet, writesRegistry)
	var stdout, stderr bytes.Buffer
	code := invocation{
		args:   slices.Concat(strings.Fields(name), args, []string{"--registry-dir", dir}, test.args),
		getenv: emptyEnvironment, stdin: strings.NewReader(test.stdin), stdinIsTTY: test.terminal,
		stdout: &stdout, stderr: &stderr, stdoutIsTTY: test.terminal,
	}.run(t.Context())
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if prompted := strings.Contains(stderr.String(), "Continue? [y/N]"); prompted != test.prompted {
		t.Errorf("prompted = %t, want %t: %s", prompted, test.prompted, stderr.String())
	}
	if removed := string(after) != writesRegistry; removed != test.approved {
		t.Errorf("registry changed = %t, want %t", removed, test.approved)
	}
	if test.approved {
		if code != exitOK {
			t.Errorf("exit = %d: %s", code, stderr.String())
		}
		return
	}
	failure := decodeFailure(t, stderr.String())
	if code != exitUsage || stdout.Len() != 0 || failure.ErrorCode != "confirmation_required" ||
		!strings.Contains(failure.ActionableFix, "--yes") {
		t.Errorf("exit = %d, stdout = %q, failure = %+v", code, stdout.String(), failure)
	}
}

// TestInterruptEndsThePrompt uses a stdin that never answers.
func TestInterruptEndsThePrompt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, localTailnet)
	writeAggregateRegistry(t, dir, localTailnet, writesRegistry)
	silent, unused := io.Pipe()
	defer func() { _ = unused.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(promptInterruptDelay, cancel)
	var stdout, stderr bytes.Buffer
	code := invocation{
		args:   []string{commandSiteID, "release", "1", "--registry-dir", dir, "--format", formatHuman},
		getenv: emptyEnvironment, stdin: silent, stdinIsTTY: true, stdout: &stdout, stderr: &stderr,
	}.run(ctx)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	failure := decodeFailure(t, stderr.String())
	if code != exitUsage || failure.ErrorCode != codeCanceled || string(after) != writesRegistry {
		t.Errorf("exit = %d, failure = %+v, registry = %s", code, failure, after)
	}
}

// TestInvalidReleaseIsReportedBeforeApproval keeps a typo from hiding behind
// the confirmation.
func TestInvalidReleaseIsReportedBeforeApproval(t *testing.T) {
	t.Parallel()
	for name, test := range dryRunCases() {
		if !contractedRelease(name) {
			continue
		}
		run := runWrite(t, nil, writesRegistry, name, test.refused...)
		if failure := decodeFailure(t, run.stderr); failure.ErrorCode != codeValidationError {
			t.Errorf("%s: failure = %+v, want %s", name, failure, codeValidationError)
		}
	}
}

func TestAdditiveWritesNeedNoApproval(t *testing.T) {
	t.Parallel()
	for name, test := range dryRunCases() {
		if contractedRelease(name) {
			continue
		}
		if run := runWrite(t, nil, writesRegistry, name, test.args...); run.code != exitOK || run.unchanged() {
			t.Errorf("%s: run = %+v, want a write without --yes", name, run)
		}
	}
}

func TestIdempotencyKeyIsEchoedNotStored(t *testing.T) {
	t.Parallel()
	run := runWrite(t, nil, writesRegistry, commandSiteIDReserve, "2", "branch", "--idempotency-key", "job-42")
	document, err := decodeDocument([]byte(run.stdout))
	if err != nil {
		t.Fatal(err)
	}
	if document.Meta["idempotency_key"] != "job-42" {
		t.Errorf("meta = %v, want the supplied key", document.Meta)
	}
	for file, content := range run.after {
		if strings.Contains(content, "job-42") {
			t.Errorf("%s holds the key: %s", file, content)
		}
	}
}

func TestIdempotencyKeyDoesNotReplayAnAllocation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var blocks []string
	for range 2 {
		data, _ := decodeRun(t, runWriteIn(t, dir, nil, commandSubnetAllocate,
			"10.0.0.0/16", "hq", "--size", "24", "--idempotency-key", "job-42"))
		blocks = append(blocks, data.CIDR)
	}
	if blocks[0] == blocks[1] {
		t.Errorf("both runs reserved %s, want different blocks", blocks[0])
	}
}

func TestFreshIdempotencyKeyPerRun(t *testing.T) {
	t.Parallel()
	keys := map[any]bool{}
	for range 2 {
		document, err := decodeDocument([]byte(runWrite(t, nil, writesRegistry, commandSiteIDList).stdout))
		if err != nil {
			t.Fatal(err)
		}
		keys[document.Meta["idempotency_key"]] = true
	}
	if len(keys) != 2 || keys[""] || keys[nil] {
		t.Errorf("keys = %v, want two fresh ones", keys)
	}
}
