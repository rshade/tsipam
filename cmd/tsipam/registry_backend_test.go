package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/diagnostics"
)

const dryRunFlag = "--dry-run"

// backendRun is a run without what varies between identical runs. stderr is the
// text a person would read.
type backendRun struct {
	code                    int
	stdout, stderr, failure string
}

func newBackendRun(t *testing.T, code int, stdout, stderr string) backendRun {
	t.Helper()
	run := backendRun{
		code: code, stdout: maskRun(t, stdout),
		stderr: regexp.MustCompile(`"(trace|span)_id":"[0-9a-f]+"`).ReplaceAllString(failureText(t, stderr), ""),
	}
	if object, _ := splitFailure(t, stderr); object != nil {
		run.failure = object.ErrorCode
	}
	return run
}

// runBackendCLI runs the command line in human mode, with a live route at
// 10.40.1.0/24 and no other devices.
func runBackendCLI(t *testing.T, env map[string]string, args ...string) backendRun {
	t.Helper()
	router := func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
		return []deviceRecord{{Name: "router", AdvertisedRoutes: []string{"10.40.1.0/24"}}}, nil
	}
	var stdout, stderr bytes.Buffer
	code := runCLI(t.Context(), args, humanByDefault(devicesEnv(env)), &stdout, &stderr, router)
	return newBackendRun(t, code, stdout.String(), stderr.String())
}

func sqliteURL(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	return "sqlite://" + path, path
}

func TestRegistrySelectionOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, env string
		args      []string
		want      string
	}{
		{name: "default", want: "."},
		{name: "environment", env: "sqlite:///srv/env.db", want: "/srv/env.db"},
		{name: "empty environment", env: "", want: "."},
		{
			name: "registry flag over environment", env: "sqlite:///srv/env.db",
			args: []string{"--registry", "file://./regs"}, want: "./regs",
		},
		{
			name: "registry-dir flag over environment", env: "sqlite:///srv/env.db",
			args: []string{"--registry-dir", "./regs"}, want: "./regs",
		},
		{
			name: "registry-dir given its own default is explicit", env: "sqlite:///srv/env.db",
			args: []string{"--registry-dir", "."}, want: ".",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var tailnet string
			cmd := &cobra.Command{}
			addRegistryFlags(cmd, &tailnet)
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			selector, err := resolveRegistry(cmd.Flags(), devicesEnv(map[string]string{registryEnv: tc.env}))
			if err != nil {
				t.Fatal(err)
			}
			if got := selector.Locations(localTailnet)[0]; got != tc.want {
				t.Errorf("location = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSQLiteSelectedByFlag(t *testing.T) {
	t.Parallel()
	url, path := sqliteURL(t)
	if got := runBackendCLI(t, nil, "site-id", "reserve", "7", "hq-vpc", "--registry", url); got.code != exitOK {
		t.Fatalf("reserve = %+v", got)
	}
	got := runBackendCLI(t, nil, "site-id", "list", "--registry", url)
	if got.code != exitOK || got.stdout != "7\thq-vpc\n" {
		t.Errorf("list = %+v, want the reservation", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "tsipam.db" {
		t.Errorf("directory holds %v, want only tsipam.db", entries)
	}
}

func TestEnvironmentSelectsTheRegistryUnlessAFlagDoes(t *testing.T) {
	t.Parallel()
	url, _ := sqliteURL(t)
	env := map[string]string{registryEnv: url}
	if got := runBackendCLI(t, env, "site-id", "reserve", "7", "from-env"); got.code != exitOK {
		t.Fatalf("reserve = %+v", got)
	}
	dir := t.TempDir()
	if got := runBackendCLI(
		t, env, "site-id", "reserve", "8", "from-flag", "--registry-dir", dir,
	); got.code != exitOK {
		t.Fatalf("reserve = %+v", got)
	}
	if got := runBackendCLI(t, env, "site-id", "list"); got.stdout != "7\tfrom-env\n" {
		t.Errorf("environment list = %+v", got)
	}
	if got := runBackendCLI(t, env, "site-id", "list", "--registry", "file://"+dir); got.stdout != "8\tfrom-flag\n" {
		t.Errorf("flag list = %+v", got)
	}
}

func TestInvalidSelectionsTouchNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{
			name: "both flags", args: []string{"--registry", "sqlite://./tsipam.db", "--registry-dir", "."},
			want: "--registry and --registry-dir cannot be used together",
		},
		{
			name: "unknown scheme", args: []string{"--registry", "postgres://user:secret@db.example.com/ipam"},
			want: "file://<directory> or sqlite://<path>",
		},
		{name: "empty path", args: []string{"--registry", "sqlite://"}, want: "sqlite://<path>"},
		{name: "empty flag", args: []string{"--registry", ""}, want: "sqlite://<path>"},
		{
			name: "invalid environment", env: map[string]string{registryEnv: "tsipam.db"},
			want: "sqlite://<path>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A release refuses a bad selection before it asks for approval.
			for _, command := range [][]string{
				{"site-id", "reserve", "7", "hq-vpc"}, {"subnet", "list"}, {"site-id", "release", "7"},
			} {
				got := runBackendCLI(t, tc.env, append(command, tc.args...)...)
				assertUsageFailure(t, got.code, got.stdout)
				if !strings.Contains(got.stderr, tc.want) || strings.Contains(got.stderr, "secret") ||
					got.failure != codeValidationError {
					t.Errorf("run = %+v, want %q and no URL", got, tc.want)
				}
			}
		})
	}
	for _, name := range []string{"tsipam.db", "local.ipam.hujson"} {
		if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s was created", name)
		}
	}
}

func TestStatelessCalcIgnoresTheRegistrySelection(t *testing.T) {
	t.Parallel()
	env := map[string]string{registryEnv: "invalid"}
	if got := runBackendCLI(t, env, "calc", "aggregate", "10.0.0.0/25", "10.0.0.128/25"); got.code != exitOK {
		t.Errorf("positional aggregate = %+v", got)
	}
	got := runBackendCLI(t, nil, "calc", "aggregate", "10.0.0.0/25", "--registry", "file://.")
	assertUsageFailure(t, got.code, got.stdout)
	if !strings.Contains(got.stderr, "--registry requires --from-registry") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestSQLiteMissingDatabaseIsNotCreated(t *testing.T) {
	t.Parallel()
	url, path := sqliteURL(t)
	for _, command := range [][]string{
		{"site-id", "list"}, {"site-id", "release", "7", "--yes"}, {"site-id", "release", "7", "--dry-run"}, {"subnet", "list"}, {"service", "list"}, {"port", "list"},
		{"calc", "aggregate", "--from-registry"},
	} {
		got := runBackendCLI(t, nil, append(command, "--registry", url)...)
		assertUsageFailure(t, got.code, got.stdout)
		if !strings.Contains(got.stderr, `no registry for tailnet "local" in the selected sqlite database`) {
			t.Errorf("%v: stderr = %q", command, got.stderr)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a read created the database")
	}
}

// Every command family gives the same exit code, output, and error on both
// backends for the same sequence, rehearsed and then run.
func TestBackendsShareCommandBehavior(t *testing.T) {
	t.Parallel()
	policy := policyFile(t, t.TempDir(), `{"ipsets":{"ipset:hq":["10.20.0.0/24"]}}`)
	stamp := []string{"--owner", "alice@example.com", "--ticket", "OPS-1", "--reserved-at", "2026-03-01T00:00:00Z"}
	commands := [][]string{
		append([]string{"site-id", "reserve", "7", "hq-vpc"}, stamp...),
		{"site-id", "reserve", "007", "hq-vpc"},
		{"site-id", "reserve", "7", "branch-office"},
		{"site-id", "reserve", "70000", "hq-vpc"},
		{"site-id", "reserve", "12", "lab"},
		{"site-id", "list"},
		{"site-id", "release", "12"},
		{"site-id", "release", "12"},
		append([]string{"subnet", "reserve", "10.20.0.0/24", "hq-vpc"}, stamp...),
		{"subnet", "reserve", "10.20.0.0/24", "hq-vpc", "--ticket", "OPS-2"},
		{"subnet", "reserve", "10.20.0.0/25", "branch-office"},
		{"subnet", "reserve", "10.20.0.1/24", "hq-vpc"},
		{"subnet", "reserve", "2001:db8::/48", "hq-vpc"},
		append([]string{"subnet", "allocate", "10.40.0.0/16", "preview", "--size", "24"}, stamp...),
		append([]string{"subnet", "allocate", "10.40.0.0/16", "preview", "--size", "24"}, stamp...),
		{"subnet", "allocate", "10.40.0.0/16", "lab", "--size", "24"},
		{"subnet", "allocate", "10.40.0.0/24", "lab", "--size", "24"},
		{"subnet", "list"},
		{"calc", "next-free", "10.40.0.0/16", "--size", "24", "--count", "2"},
		{"calc", "aggregate", "--from-registry"},
		{"check-policy", policy},
		{"subnet", "release", "10.20.0.0/24"},
		{"subnet", "release", "10.20.0.0/24"},
		append([]string{"service", "reserve", "100.100.100.8", "dashboard"}, stamp...),
		{"service", "reserve", "100.100.100.8/32", "svc:dashboard"},
		{"service", "reserve", "100.100.100.8", "search"},
		{"service", "list"},
		{"service", "release", "100.100.100.8"},
		{"service", "list"},
		{"port", "reserve", "41642", "hq-fw01", "--site", "hq-vpc"},
		{"port", "reserve", "41642", "hq-fw01", "--site", "hq-vpc"},
		{"port", "reserve", "41642", "hq-fw02", "--site", "hq-vpc"},
		{"port", "reserve", "80", "hq-fw01", "--site", "hq-vpc"},
		{"port", "list"},
		{"port", "list", "--site", "absent"},
		{"port", "release", "41642", "--site", "hq-vpc"},
		{"port", "list"},
	}
	for _, format := range []string{formatHuman, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			url, path := sqliteURL(t)
			dir := t.TempDir()
			for _, command := range commands {
				for _, mode := range []string{dryRunFlag, "--yes"} {
					common := append([]string{"--format", format, "--tailnet", "alias", mode}, command...)
					file := runBackendCLI(t, nil, append(common, "--registry-dir", dir)...)
					before, stamp := directoryState(t, filepath.Dir(path))
					sqlite := runBackendCLI(t, nil, append(common, "--registry", url)...)
					after, newStamp := directoryState(t, filepath.Dir(path))
					if file != sqlite {
						t.Errorf("%s %v:\n  file   %+v\n  sqlite %+v", mode, command, file, sqlite)
					}
					if rehearses(mode, command) && (!maps.Equal(before, after) || stamp != newStamp) {
						t.Errorf("%v changed the database directory in a dry run", command)
					}
				}
			}
		})
	}
}

// rehearses reports whether command, run with the mode flag, is a rehearsed
// write. A dry run of any other command is an ordinary run.
func rehearses(mode string, command []string) bool {
	return mode == dryRunFlag && slices.Contains([]string{"reserve", "allocate", "release"}, command[1])
}

// directoryState is a directory's files and its modification time, which changes
// when a file is created or removed, even one that is gone again.
func directoryState(t *testing.T, dir string) (map[string]string, time.Time) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	return directoryFiles(t, dir), info.ModTime()
}

func TestBackendsSharePlanning(t *testing.T) {
	t.Parallel()
	url, _ := sqliteURL(t)
	dir := t.TempDir()
	selections := [][]string{{"--registry-dir", dir}, {"--registry", url}}
	for _, selection := range selections {
		for _, command := range [][]string{
			{"site-id", "reserve", "7", "hq-vpc"},
			{"subnet", "reserve", "10.20.0.0/16", "hq-vpc"},
			{"service", "reserve", "100.100.100.8", "dashboard"},
		} {
			if got := runBackendCLI(t, nil, append(command, selection...)...); got.code != exitOK {
				t.Fatalf("%v = %+v", command, got)
			}
		}
	}
	for command, wantCode := range map[string]int{commandPlan: exitOK, commandCheck: exitFailure} {
		for _, format := range []string{formatHuman, formatJSON} {
			var results []backendRun
			for _, selection := range selections {
				args := append([]string{command, "--format", format, "--tailnet", localTailnet}, selection...)
				results = append(results, runPlanningWithRoute(t, "10.20.5.0/24", args...))
			}
			file, sqlite := results[0], results[1]
			if file != sqlite {
				t.Errorf("%s %s:\n  file   %+v\n  sqlite %+v", command, format, file, sqlite)
			}
			if sqlite.code != wantCode || !strings.Contains(sqlite.stdout, "10.20.0.0/16") ||
				!strings.Contains(sqlite.stdout, "matched") {
				t.Errorf("%s %s = %+v, want a comparison with exit %d", command, format, sqlite, wantCode)
			}
		}
	}
}

// runPlanningWithRoute runs plan or check against a tailnet that advertises
// one route and no Services.
func runPlanningWithRoute(t *testing.T, route string, args ...string) backendRun {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := invocation{
		args: args, stdout: &stdout, stderr: &stderr,
		getenv: devicesEnv(map[string]string{"TAILSCALE_TAILNET": "live.example.com"}),
		collectLive: func(context.Context, string, diagnostics.Logger) (devices.Snapshot, error) {
			return devices.Snapshot{Devices: []devices.Device{{Name: "router", AdvertisedRoutes: []string{route}}}}, nil
		},
	}.run(t.Context())
	return newBackendRun(t, code, stdout.String(), stderr.String())
}

func TestSQLiteLocationIsSanitized(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "acme-ipam", "registry-acme.db")
	for _, command := range [][]string{{"site-id", "list"}, {"site-id", "reserve", "7", "hq-vpc"}} {
		got := runBackendCLI(t, map[string]string{
			sanitize.LevelEnv: string(sanitize.Identity), sanitize.KeyEnv: testSanitizeKey,
		}, append(command, "--registry", "sqlite://"+path)...)
		assertUsageFailure(t, got.code, got.stdout)
		for _, private := range []string{path, "acme-ipam", "registry-acme.db"} {
			if strings.Contains(got.stderr, private) {
				t.Errorf("%v: stderr leaks %q: %s", command, private, got.stderr)
			}
		}
	}
}

func TestSQLiteIdentitiesAreSanitizedInErrors(t *testing.T) {
	t.Parallel()
	url, _ := sqliteURL(t)
	if got := runBackendCLI(
		t, nil, "subnet", "reserve", "10.20.0.0/24", "acme-private-site", "--registry", url,
	); got.code != exitOK {
		t.Fatalf("reserve = %+v", got)
	}
	got := runBackendCLI(t, map[string]string{
		sanitize.LevelEnv: string(sanitize.Identity), sanitize.KeyEnv: testSanitizeKey,
	}, "subnet", "reserve", "10.20.0.0/25", "other-private-site", "--registry", url)
	if got.code != exitFailure || got.stdout != "" {
		t.Fatalf("collision = %+v, want exit 1 with empty stdout", got)
	}
	for _, private := range []string{"acme-private-site", "other-private-site", "10.20.0.0"} {
		assertNoLeak(t, got.stderr, private)
	}
}

// A failed rehearsal's error text is sanitized from the registry's own values,
// which must not need a connection either.
func TestSQLiteRehearsalInAReadOnlyDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind this user")
	}
	url, path := sqliteURL(t)
	dir := filepath.Dir(path)
	mustRun(t, "subnet", "reserve", "10.20.0.0/24", "acme-private-site", "--registry", url)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	// The owner needs access again for the test directory to be removed.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	got := runBackendCLI(t, nil, "subnet", "reserve", "10.30.0.0/24", "branch", "--dry-run", "--registry", url)
	if got.code != exitOK || !strings.HasPrefix(got.stdout, "dry run: would reserve subnet CIDR 10.30.0.0/24") {
		t.Errorf("rehearsal = %+v", got)
	}
	got = runBackendCLI(t, map[string]string{
		sanitize.LevelEnv: string(sanitize.Identity), sanitize.KeyEnv: testSanitizeKey,
	}, "subnet", "reserve", "10.20.0.0/25", "other-private-site", "--dry-run", "--registry", url)
	if got.code != exitFailure || got.failure != codeRegistryCollision {
		t.Fatalf("rehearsed collision = %+v", got)
	}
	for _, private := range []string{"acme-private-site", "other-private-site", "10.20.0.0"} {
		assertNoLeak(t, got.stderr, private)
	}
}

func TestVerboseOutputOmitsTheDatabase(t *testing.T) {
	t.Parallel()
	url, path := sqliteURL(t)
	for _, command := range [][]string{{"site-id", "reserve", "7", "hq-vpc"}, {"site-id", "list"}} {
		got := runBackendCLI(t, nil, append(command, "--verbose", "--registry", url)...)
		if got.code != exitOK || !strings.Contains(got.stderr, "registry loaded") &&
			!strings.Contains(got.stderr, "registry created") {
			t.Fatalf("%v = %+v, want diagnostics", command, got)
		}
		for _, private := range []string{path, filepath.Base(path), "sqlite://"} {
			if strings.Contains(got.stderr, private) {
				t.Errorf("%v: diagnostics name %q: %s", command, private, got.stderr)
			}
		}
	}
}
