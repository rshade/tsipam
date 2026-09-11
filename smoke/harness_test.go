//go:build smoke

// Package smoke_test exercises a built tail-ipam binary end to end against
// the contract the README documents: exit codes, human and JSON output,
// registry side effects, sanitization, and, when enabled, the live tailnet
// commands. It is configured through the environment so the same invocation
// works under `make`, under `esc run`, and in CI:
//
//	TAIL_IPAM_SMOKE_BINARY  binary under test; when unset, the host binary is
//	                        extracted from TAIL_IPAM_SMOKE_DIST, or built from
//	                        source when that is unset too
//	TAIL_IPAM_SMOKE_DIST    goreleaser dist directory; enables artifact checks
//	TAIL_IPAM_SMOKE_LIVE    non-empty runs the commands that need TAILSCALE_*
//
//	go test -tags smoke ./smoke
//	TAIL_IPAM_SMOKE_DIST=dist go test -tags smoke ./smoke
//	esc run <env> -- env TAIL_IPAM_SMOKE_LIVE=1 go test -tags smoke ./smoke
package smoke_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	exitOK      = 0
	exitRefused = 1
	exitInvalid = 2

	flagFormat      = "--format"
	formatJSON      = "json"
	flagRegistryDir = "--registry-dir"
	flagTailnet     = "--tailnet"
	defaultAlias    = "alias"

	envBinary = "TAIL_IPAM_SMOKE_BINARY"
	envDist   = "TAIL_IPAM_SMOKE_DIST"
	envLive   = "TAIL_IPAM_SMOKE_LIVE"
)

// TestMain resolves the binary under test once, before the parallel phase,
// and publishes its absolute path through the environment so helpers need no
// package state.
func TestMain(m *testing.M) {
	path, cleanup, err := resolveBinary(os.Getenv(envBinary), os.Getenv(envDist))
	if err != nil {
		fmt.Fprintln(os.Stderr, "smoke:", err)
		os.Exit(exitInvalid)
	}
	if err = os.Setenv(envBinary, path); err != nil {
		fmt.Fprintln(os.Stderr, "smoke:", err)
		os.Exit(exitInvalid)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func binary() string { return os.Getenv(envBinary) }

// resolveBinary returns the binary under test: the configured path, else the
// host archive's binary from dist, else a fresh build of the module so the
// suite has no setup step in the common case.
func resolveBinary(configured, dist string) (string, func(), error) {
	if configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return "", nil, err
		}
		if _, err = os.Stat(abs); err != nil {
			return "", nil, err
		}
		return abs, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "tail-ipam-smoke-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	out := filepath.Join(dir, "tail-ipam")
	if dist != "" {
		err = extractHostBinary(dist, out)
	} else {
		build := exec.Command("go", "build", "-o", out, filepath.Join(moduleDir, "cmd", "tail-ipam"))
		build.Stderr = os.Stderr
		if err = build.Run(); err != nil {
			err = fmt.Errorf("building tail-ipam: %w", err)
		}
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return out, cleanup, nil
}

// result is one invocation's observable outcome.
type result struct {
	code   int
	stdout string
	stderr string
}

// run executes the binary with a scrubbed environment plus env, so a
// developer's own TAILSCALE_* variables never leak into offline cases.
func run(t *testing.T, env map[string]string, args ...string) result {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary(), args...)
	cmd.Env = scrubbedEnv(env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := exitOK
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("running %s %v: %v", binary(), args, err)
	}
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// scrubbedEnv keeps the inherited environment (PATH, HOME, TLS roots) but
// drops every TAILSCALE_* variable unless env sets it explicitly.
func scrubbedEnv(env map[string]string) []string {
	out := []string{}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "TAILSCALE_") {
			continue
		}
		out = append(out, kv)
	}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		out = append(out, key+"="+env[key])
	}
	return out
}

// envelope is the versioned JSON contract every successful command emits.
// Command-specific payloads stay raw so each test decodes only what it asserts.
type envelope struct {
	SchemaVersion string `json:"schema_version"`
	Tool          string `json:"tool"`
	Command       string `json:"command"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	Tailnet       string `json:"tailnet"`

	raw map[string]json.RawMessage
}

// decodeEnvelope fails the test unless stdout is exactly one JSON document
// carrying the six envelope fields.
func decodeEnvelope(t *testing.T, stdout string) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if err := json.Unmarshal([]byte(stdout), &env.raw); err != nil {
		t.Fatalf("stdout is not a JSON object: %v", err)
	}
	if env.SchemaVersion != "1" || env.Tool != "tail-ipam" || env.Command == "" || env.Status == "" {
		t.Fatalf("envelope header incomplete: %+v", env)
	}
	return env
}

// field decodes one command-specific payload member into out.
func (e envelope) field(t *testing.T, name string, out any) {
	t.Helper()
	raw, ok := e.raw[name]
	if !ok {
		t.Fatalf("envelope has no %q field; keys: %v", name, slices.Sorted(maps.Keys(e.raw)))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decoding %q: %v", name, err)
	}
}

// registry is a temporary registry directory addressed by alias.
type registry struct {
	dir   string
	alias string
}

func newRegistry(t *testing.T) registry {
	t.Helper()
	return registry{dir: t.TempDir(), alias: defaultAlias}
}

// args appends the registry selection flags to a command line.
func (r registry) args(command ...string) []string {
	return append(command, flagRegistryDir, r.dir, flagTailnet, r.alias)
}

func (r registry) path() string {
	return filepath.Join(r.dir, r.alias+".ipam.hujson")
}

func (r registry) exists() bool {
	_, err := os.Stat(r.path())
	return err == nil
}

func (r registry) contents(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(r.path())
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	return string(data)
}

func (r registry) write(t *testing.T, contents string) {
	t.Helper()
	if err := os.WriteFile(r.path(), []byte(contents), 0o600); err != nil {
		t.Fatalf("writing registry: %v", err)
	}
}

// expect asserts the exit code and, for failures, that stdout stayed empty so
// a JSON consumer never receives a partial document.
func expect(t *testing.T, res result, code int) {
	t.Helper()
	if res.code != code {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, code, res.stdout, res.stderr)
	}
	if code != exitOK && res.stdout != "" {
		t.Fatalf("failure wrote to stdout: %s", res.stdout)
	}
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func containsAll(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			t.Errorf("missing %q in:\n%s", needle, haystack)
		}
	}
}

func containsNone(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			t.Errorf("unexpected %q in:\n%s", needle, haystack)
		}
	}
}
