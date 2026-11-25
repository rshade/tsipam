package main

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"slices"
	"testing"

	"github.com/rshade/ax-go"
	"github.com/rshade/ax-go/axtest"
	"github.com/spf13/cobra"
)

// outputModeCases has a row for each combination of flag, environment, and
// terminal that decides differently.
func outputModeCases() []struct {
	name, flag, agentMode string
	terminal, wantJSON    bool
} {
	return []struct {
		name, flag, agentMode string
		terminal, wantJSON    bool
	}{
		{name: "pipe", wantJSON: true},
		{name: "terminal", terminal: true},
		{name: "agent mode on a terminal", agentMode: "1", terminal: true, wantJSON: true},
		{name: "agent mode in a pipe", agentMode: "1", wantJSON: true},
		{name: "human agent mode in a pipe", agentMode: "human"},
		{name: "human agent mode on a terminal", agentMode: "human", terminal: true},
		{name: "human flag in a pipe", flag: formatHuman},
		{name: "human flag over agent mode", flag: formatHuman, agentMode: "1"},
		{name: "json flag on a terminal", flag: formatJSON, terminal: true, wantJSON: true},
		{name: "json flag over agent mode", flag: formatJSON, agentMode: "human", terminal: true, wantJSON: true},
	}
}

func TestOutputModePrecedence(t *testing.T) {
	t.Parallel()
	for _, test := range outputModeCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			args := []string{commandCalc, "split", "10.0.0.0/30", "--prefix", "31"}
			if test.flag != "" {
				args = append(args, "--format", test.flag)
			}
			root := invocation{stdout: io.Discard}.commandTree(&commandState{})
			root.SetArgs(args)
			result := axtest.Run(t.Context(), t, root, args,
				ax.WithEnv(devicesEnv(map[string]string{"AGENT_MODE": test.agentMode})),
				ax.WithStdoutIsTTY(test.terminal))
			if result.ExitCode != exitOK {
				t.Fatalf("exit = %d: %s", result.ExitCode, result.Stderr)
			}
			if test.wantJSON {
				data := axtest.Decode[outputEnvelope](t, result.Stdout)
				if data.SchemaVersion != outputSchemaVersion || data.Subnets == nil || len(*data.Subnets) != 2 {
					t.Errorf(
						"result = %s, want two subnets in a version %s document",
						result.Stdout,
						outputSchemaVersion,
					)
				}
				return
			}
			if got, want := string(result.Stdout), "10.0.0.0/31\n10.0.0.2/31\n"; got != want {
				t.Errorf("stdout = %q, want %q", got, want)
			}
		})
	}
}

// TestBufferIsNotATerminal covers the detection itself: anything but an
// interactive terminal selects JSON.
func TestBufferIsNotATerminal(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runWith(t, nil, commandCalc, "info", "10.0.0.0/24")
	if code != exitOK || stderr != "" {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	if _, err := decodeDocument([]byte(stdout)); err != nil {
		t.Errorf("stdout = %q: %v", stdout, err)
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	for _, stream := range []any{&bytes.Buffer{}, null, nil} {
		if isTerminal(stream) {
			t.Errorf("isTerminal(%T) = true", stream)
		}
	}
}

func TestUnknownOutputModeStopsBeforeAnyWork(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		env   map[string]string
		extra []string
	}{
		"flag":         {extra: []string{"--format", "bogus"}},
		"other name":   {extra: []string{"--format", "agent"}},
		"empty flag":   {extra: []string{"--format="}},
		"unset value":  {extra: []string{"--format", ""}},
		"environment":  {env: map[string]string{"AGENT_MODE": "sometimes"}},
		"blank format": {extra: []string{"--format", " "}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			args := append([]string{commandSiteID, "reserve", "1", "hq", "--registry-dir", dir}, test.extra...)
			code, stdout, stderr := runWith(t, test.env, args...)
			failure := decodeFailure(t, stderr)
			if code != exitUsage || stdout != "" || failure.ErrorCode != codeValidationError {
				t.Errorf("exit = %d, stdout = %q, failure = %+v", code, stdout, failure)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Errorf("directory holds %v, %v; want nothing", entries, err)
			}
		})
	}
}

func TestFormatPublishesItsValues(t *testing.T) {
	t.Parallel()
	_, stdout, _ := runWith(t, nil, "__schema")
	var document struct {
		Command struct {
			Flags []struct {
				Name string   `json:"name"`
				Enum []string `json:"enum"`
			} `json:"flags"`
		} `json:"command"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatal(err)
	}
	for _, flag := range document.Command.Flags {
		if flag.Name == "format" {
			if !slices.Equal(flag.Enum, []string{formatHuman, formatJSON}) {
				t.Errorf("format values = %v", flag.Enum)
			}
			return
		}
	}
	t.Error("__schema does not list --format")
}

// isLeaf reports whether cmd is a leaf command: one that runs and writes a
// result document.
func isLeaf(cmd *cobra.Command) bool {
	if !cmd.Runnable() || !cmd.HasParent() {
		return false
	}
	for ancestor := cmd; ancestor.HasParent(); ancestor = ancestor.Parent() {
		switch ancestor.Name() {
		case "__schema", "mcp-server", "help", "completion":
			return false
		}
	}
	return true
}

func leafNames() []string {
	var names []string
	walkCommands(invocation{stdout: io.Discard}.commandTree(&commandState{}), func(cmd *cobra.Command) {
		if isLeaf(cmd) {
			names = append(names, commandName(cmd))
		}
	})
	slices.Sort(names)
	return names
}

func TestEveryLeafHasAContract(t *testing.T) {
	t.Parallel()
	declared := slices.Sorted(maps.Keys(leafContracts()))
	if names := leafNames(); !slices.Equal(names, declared) {
		t.Errorf("leaf commands are %v, contracts cover %v", names, declared)
	}
}

func TestEveryLeafPublishesItsRunVaryingFields(t *testing.T) {
	t.Parallel()
	meta := []string{"meta.idempotency_key", "meta.span_id", "meta.trace_id"}
	for name, contract := range leafContracts() {
		want := meta
		if contract.stamps {
			want = append([]string{"data.reserved_at"}, meta...)
		}
		if got := publishedFields(t, name); !slices.Equal(got, want) {
			t.Errorf("%s publishes %v, want %v", name, got, want)
		}
	}
}

func TestIdenticalReadsMatchAfterMasking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, localTailnet,
		`{"siteIDs":{"1":{"site":"hq","owner":"netops","reservedAt":"2026-11-25T12:00:00Z"}}}`)
	args := []string{commandSiteID, commandList, "--registry-dir", dir, "--format", formatJSON}
	_, first, _ := runWith(t, nil, args...)
	_, second, _ := runWith(t, nil, args...)
	if first == second {
		t.Error("two runs share their metadata")
	}
	if maskRun(t, first) != maskRun(t, second) {
		t.Errorf("masked runs differ: %s and %s", maskRun(t, first), maskRun(t, second))
	}
}

func TestResultMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := map[string]string{"TRACEPARENT": testTraceParent}
	code, stdout, stderr := runWith(t, env,
		commandSiteID, "reserve", "1", "hq", "--registry-dir", dir, "--format", formatJSON)
	if code != exitOK {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	document, err := decodeDocument([]byte(stdout))
	if err != nil {
		t.Fatal(err)
	}
	if document.Meta["trace_id"] != testTraceID {
		t.Errorf("trace_id = %v, want the caller's", document.Meta["trace_id"])
	}
	if key, _ := document.Meta["idempotency_key"].(string); key == "" {
		t.Error("meta has no idempotency key")
	}
	if _, present := document.Meta["dry_run"]; present {
		t.Error("a real run is marked as a rehearsal")
	}
}

// TestNonLeafOutputKeepsItsForm covers the surfaces the envelope leaves alone.
func TestNonLeafOutputKeepsItsForm(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"completion": {"completion", "bash"},
		"-schema":    {"-schema"},
		"__schema":   {"__schema"},
		"--version":  {"--version"},
		"--help":     {"--help"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runWith(t, nil, args...)
			if code != exitOK || stdout == "" {
				t.Fatalf("exit = %d: %s", code, stderr)
			}
			if _, err := decodeDocument([]byte(stdout)); err == nil {
				t.Errorf("stdout is enveloped: %s", stdout)
			}
		})
	}
}
