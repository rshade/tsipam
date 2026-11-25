package main

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// inputCase is one command called with its inputs by position and by flag.
type inputCase struct {
	command []string
	// flags holds each input in position order: its flag, then its value.
	flags [][2]string
	extra []string
	// many is true for a command that takes any number of one input.
	many bool
}

func (c inputCase) name() string { return strings.Join(c.command, " ") }

func (c inputCase) byPosition() []string {
	args := slices.Clone(c.command)
	for _, input := range c.flags {
		args = append(args, input[1])
	}
	return append(args, c.extra...)
}

func (c inputCase) byFlag(dash string) []string {
	args := slices.Clone(c.command)
	for _, input := range c.flags {
		args = append(args, dash+input[0], input[1])
	}
	return append(args, c.extra...)
}

const inputsRegistry = `{
	"siteIDs": {"1": "hq"},
	"subnetPools": {"10.0.0.0/24": "hq"},
	"serviceAddresses": {"100.100.100.7": "svc:payments"},
	"udpPorts": {"hq": {"41642": "fw01"}}
}`

func inputCases() []inputCase {
	return []inputCase{
		{command: []string{commandSiteID, "reserve"}, flags: [][2]string{{"id", "2"}, {"site", "branch"}}},
		{command: []string{commandSiteID, "release"}, flags: [][2]string{{"id", "1"}}, extra: []string{"--yes"}},
		{command: []string{commandSubnet, "reserve"}, flags: [][2]string{{"cidr", "10.1.0.0/24"}, {"site", "branch"}}},
		{
			command: []string{commandSubnet, "allocate"},
			flags:   [][2]string{{"pool", "10.0.0.0/16"}, {"site", "branch"}}, extra: []string{"--size", "24"},
		},
		{
			command: []string{commandSubnet, "release"}, flags: [][2]string{{"cidr", "10.0.0.0/24"}},
			extra: []string{"--yes"},
		},
		{
			command: []string{commandService, "reserve"},
			flags:   [][2]string{{"address", "100.100.100.8"}, {"service", "search"}},
		},
		{
			command: []string{commandService, "release"}, flags: [][2]string{{"address", "100.100.100.7"}},
			extra: []string{"--yes"},
		},
		{
			command: []string{commandPort, "reserve"},
			flags:   [][2]string{{"port", "41643"}, {"device", "fw02"}}, extra: []string{"--site", "hq"},
		},
		{
			command: []string{commandPort, "release"}, flags: [][2]string{{"port", "41642"}},
			extra: []string{"--site", "hq", "--yes"},
		},
		{
			command: []string{commandCalc, "split"}, flags: [][2]string{{"cidr", "10.0.0.0/24"}},
			extra: []string{"--prefix", "26"},
		},
		{command: []string{commandCalc, "info"}, flags: [][2]string{{"cidr", "10.0.0.0/24"}}},
		{
			command: []string{commandCalc, "next-free"}, flags: [][2]string{{"pool", "10.0.0.0/16"}},
			extra: []string{"--size", "24"},
		},
		{
			command: []string{commandCalc, "aggregate"}, many: true,
			flags: [][2]string{{"cidr", "10.0.0.0/25"}, {"cidr", "10.0.0.128/25"}},
		},
		{command: []string{commandCheckPolicy}, flags: [][2]string{{"policy-file", "private-policy.hujson"}}},
	}
}

// inputRun is everything a run leaves behind.
type inputRun struct {
	code     int
	stdout   string
	stderr   string
	registry string
}

// runInput runs args against a fresh registry. Registry flags are added for the
// commands that take them.
func runInput(t *testing.T, env map[string]string, args []string) inputRun {
	t.Helper()
	dir := t.TempDir()
	path := writeAggregateRegistry(t, dir, localTailnet, inputsRegistry)
	policy := policyFile(t, dir, `{"ipsets":{"ipset:hq":["10.0.0.0/25"]}}`)
	args = slices.Clone(args)
	for index, arg := range args {
		if arg == "private-policy.hujson" {
			args[index] = policy
		}
	}
	if args[0] != commandCalc || args[1] == "next-free" {
		args = append(args, "--registry-dir", dir)
	}
	listDevices := func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
		return []deviceRecord{{Name: "router", AdvertisedRoutes: []string{"10.0.1.0/24"}}}, nil
	}
	var stdout, stderr bytes.Buffer
	code := runCLI(t.Context(), args, devicesEnv(env), &stdout, &stderr, listDevices)
	registry, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return inputRun{
		code:     code,
		stdout:   maskRun(t, stdout.String()),
		stderr:   stderr.String(),
		registry: string(registry),
	}
}

// TestEveryPositionalArgumentHasACase keeps the table complete: a command whose
// usage line names an argument must be in it.
func TestEveryPositionalArgumentHasACase(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{}
	for _, test := range inputCases() {
		covered[test.name()] = true
	}
	for _, command := range commandSchema(invocation{stdout: &bytes.Buffer{}}.commandTree(&commandState{})) {
		takesInput := strings.ContainsAny(command.Usage, "<[") && !strings.HasPrefix(command.Name, "help")
		if takesInput != covered[command.Name] {
			t.Errorf("%s: usage %q, covered = %t", command.Name, command.Usage, covered[command.Name])
		}
	}
}

func TestFlagInputsEqualPositions(t *testing.T) {
	t.Parallel()
	for _, test := range inputCases() {
		t.Run(test.name(), func(t *testing.T) {
			t.Parallel()
			want := runInput(t, nil, test.byPosition())
			if want.code == exitUsage || want.stdout == "" {
				t.Fatalf("positional run failed: %+v", want)
			}
			for _, dash := range []string{"--", "-"} {
				got := runInput(t, nil, test.byFlag(dash))
				if got.code != want.code || got.stdout != want.stdout || got.registry != want.registry {
					t.Errorf("%s flags gave %+v, positions gave %+v", dash, got, want)
				}
			}
		})
	}
}

func TestMixedInputsAreRefused(t *testing.T) {
	t.Parallel()
	for _, test := range inputCases() {
		first, last := test.flags[0], test.flags[len(test.flags)-1]
		mixes := map[string][]string{"both for one input": {first[1], "--" + first[0], first[1]}}
		if len(test.flags) > 1 {
			mixes["one of each"] = []string{first[1], "--" + last[0], last[1]}
		}
		for name, inputs := range mixes {
			t.Run(test.name()+"/"+name, func(t *testing.T) {
				t.Parallel()
				got := runInput(t, nil, slices.Concat(test.command, inputs, test.extra))
				assertInputRefused(t, got, "not both")
			})
		}
	}
}

func assertInputRefused(t *testing.T, got inputRun, want ...string) {
	t.Helper()
	failure := decodeFailure(t, got.stderr)
	if got.code != exitUsage || got.stdout != "" || failure.ErrorCode != codeValidationError {
		t.Errorf("run = %+v, want a %s", got, codeValidationError)
	}
	for _, text := range want {
		if !strings.Contains(failure.Message, text) {
			t.Errorf("message = %q, want %q", failure.Message, text)
		}
	}
	if got.registry != inputsRegistry {
		t.Errorf("registry changed: %s", got.registry)
	}
}

func TestMissingInputsAreNamed(t *testing.T) {
	t.Parallel()
	for _, test := range inputCases() {
		t.Run(test.name(), func(t *testing.T) {
			t.Parallel()
			names := map[string]bool{}
			for _, input := range test.flags {
				names["--"+input[0]] = true
			}
			got := runInput(t, nil, slices.Concat(test.command, test.extra))
			assertInputRefused(t, got, slices.Collect(maps.Keys(names))...)

			if len(test.flags) < 2 || test.many {
				return
			}
			first, last := test.flags[0], test.flags[1]
			for _, partial := range [][]string{{first[1]}, {"--" + first[0], first[1]}} {
				got = runInput(t, nil, slices.Concat(test.command, partial, test.extra))
				assertInputRefused(t, got, "--"+last[0])
				if failure := decodeFailure(t, got.stderr); strings.Contains(failure.Message, "--"+first[0]) {
					t.Errorf("message = %q names an input that was given", failure.Message)
				}
			}
		})
	}
}

func TestExtraPositionalArgumentIsRefused(t *testing.T) {
	t.Parallel()
	for _, test := range inputCases() {
		if test.many {
			continue
		}
		t.Run(test.name(), func(t *testing.T) {
			t.Parallel()
			got := runInput(t, nil, append(test.byPosition(), "extra"))
			assertInputRefused(t, got, "accepts")
		})
	}
}

func TestEmptyInputReachesValidation(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"position": {commandSubnet, "reserve", "", "hq"},
		"flag":     {commandSubnet, "reserve", "--cidr", "", "--site", "hq"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := runInput(t, nil, args)
			if failure := decodeFailure(t, got.stderr); got.code != exitUsage ||
				failure.Message != "subnet CIDR must not be empty" {
				t.Errorf("exit = %d, message = %q", got.code, failure.Message)
			}
		})
	}
}

func TestRefusedInputCreatesNoFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, _, stderr := runWith(t, nil, commandSiteID, "reserve", "1", "--site", "hq", "--registry-dir", dir)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 || code != exitUsage {
		t.Errorf("exit = %d, directory = %v, %v; stderr: %s", code, entries, err, stderr)
	}
}

// TestFlagInputsAreSanitized gives each command secrets by flag, in runs that
// fail at every stage, and looks for them on both streams.
func TestFlagInputsAreSanitized(t *testing.T) {
	t.Parallel()
	const secret = "branch-secret"
	dir := t.TempDir()
	secretPath := filepath.Join(dir, secret)
	tests := map[string][]string{
		"site ID collision":    {commandSiteID, "reserve", "--id", "1", "--site", secret},
		"invalid site ID":      {commandSiteID, "reserve", "--id", secret, "--site", "hq"},
		"invalid site":         {commandSiteID, "reserve", "--id", "2", "--site", secret + "\x01"},
		"subnet overlap":       {commandSubnet, "reserve", "--cidr", "10.0.0.0/25", "--site", secret},
		"invalid CIDR":         {commandSubnet, "reserve", "--cidr", secret, "--site", "hq"},
		"invalid release":      {commandSubnet, "release", "--cidr", secret, "--yes"},
		"service collision":    {commandService, "reserve", "--address", "100.100.100.7", "--service", secret},
		"invalid address":      {commandService, "reserve", "--address", secret, "--service", "x"},
		"port collision":       {commandPort, "reserve", "--port", "41642", "--device", secret, "--site", "hq"},
		"invalid port":         {commandPort, "reserve", "--port", secret, "--device", "fw02", "--site", "hq"},
		"positional port":      {commandPort, "reserve", secret, "fw02", "--site", "hq"},
		"invalid port release": {commandPort, "release", "--port", secret, "--site", "hq", "--yes"},
		"invalid pool":         {commandCalc, "next-free", "--pool", secret, "--size", "24"},
		"invalid allocate":     {commandSubnet, "allocate", "--pool", secret, "--site", "hq", "--size", "24"},
		"missing policy file":  {commandCheckPolicy, "--policy-file", secretPath},
		"mixed inputs":         {commandSiteID, "reserve", "1", "--site", secret},
		"missing input":        {commandSiteID, "reserve", "--site", secret},
	}
	for name, args := range tests {
		for _, level := range []string{"identity", "strict"} {
			t.Run(name+"/"+level, func(t *testing.T) {
				t.Parallel()
				for _, run := range []inputRun{
					runInput(t, map[string]string{sanitize.LevelEnv: level}, args),
					runInput(t, nil, append(slices.Clone(args), "--sanitize", level)),
				} {
					if run.code == exitOK {
						t.Fatalf("run succeeded: %+v", run)
					}
					text := run.stdout + run.stderr + failureText(t, run.stderr)
					if strings.Contains(text, secret) || strings.Contains(text, "10.0.0.0/25") {
						t.Errorf("output exposes an input: %s", text)
					}
				}
			})
		}
	}
}

// TestFlagInputsAreSanitizedOnSuccess covers the result itself.
func TestFlagInputsAreSanitizedOnSuccess(t *testing.T) {
	t.Parallel()
	env := map[string]string{sanitize.LevelEnv: "identity"}
	for _, format := range []string{formatHuman, formatJSON} {
		got := runInput(t, env, []string{
			commandSubnet, "reserve", "--cidr", "10.77.0.0/24", "--site", "branch-secret", "--format", format,
		})
		text := got.stdout + got.stderr
		if got.code != exitOK || strings.Contains(text, "branch-secret") || strings.Contains(text, "10.77.0.0") {
			t.Errorf("%s run = %+v", format, got)
		}
	}
}
