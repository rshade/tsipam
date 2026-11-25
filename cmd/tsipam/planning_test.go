package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rshade/tsacl/pkg/policycore/cliflags"

	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/diagnostics"
)

func TestPlanningInvalidInvocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, registry, message string
		env                     map[string]string
		extra                   []string
	}{
		{"missing source", `{}`, "TAILSCALE_TAILNET is required", nil, nil},
		{"missing credentials", `{}`, "TAILSCALE_API_KEY", map[string]string{"TAILSCALE_TAILNET": "unused"}, nil},
		{"incomplete OAuth", `{}`, "TAILSCALE_API_KEY", map[string]string{
			"TAILSCALE_TAILNET": "unused", "TAILSCALE_OAUTH_CLIENT_ID": "unused"}, nil},
		{"missing registry", "", "no such file", map[string]string{
			"TAILSCALE_TAILNET": "unused", "TAILSCALE_API_KEY": "unused"}, nil},
		{"invalid registry", `{"siteIDs":false}`, "siteIDs", map[string]string{
			"TAILSCALE_TAILNET": "unused", "TAILSCALE_API_KEY": "unused"}, nil},
		{"unexpected argument", `{}`, "unknown command", nil, []string{"extra"}},
		{"invalid format", `{}`, "format", nil, []string{"--format", "invalid"}},
	}
	for _, tc := range cases {
		for _, command := range []string{commandPlan, commandCheck} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				if tc.registry != "" {
					writeAggregateRegistry(t, dir, "alias", tc.registry)
				}
				args := append([]string{command, "--registry-dir", dir, "--tailnet", "alias"}, tc.extra...)
				code, out, errOut := runPlanning(t.Context(), tc.env, args...)
				assertUsageFailure(t, code, out)
				if !strings.Contains(errOut, tc.message) {
					t.Errorf("stderr = %q, want %q", errOut, tc.message)
				}
			})
		}
	}
}

type planningErrorWriter struct{}

func (planningErrorWriter) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}

func TestPlanningOutputFailureOverridesDrift(t *testing.T) {
	t.Parallel()
	for _, format := range []string{formatHuman, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeAggregateRegistry(t, dir, "alias", `{"siteIDs":{"1":"branch"}}`)
			state := &commandState{}
			root := newRootCommandWithDependencies(state, commandDependencies{
				getenv: devicesEnv(map[string]string{"TAILSCALE_TAILNET": "live"}),
				collectLive: func(context.Context, string, diagnostics.Logger) (devices.Snapshot, error) {
					return devices.Snapshot{}, nil
				},
			})
			root.SetArgs(cliflags.Normalize(root, []string{commandCheck,
				"--registry-dir", dir, "--tailnet", "alias", "--format", format}))
			root.SetOut(planningErrorWriter{})
			var stderr bytes.Buffer
			root.SetErr(&stderr)
			err := root.ExecuteContext(t.Context())
			if err == nil || exitCodeFor(err) != exitUsage || !strings.Contains(err.Error(), "output unavailable") {
				t.Fatalf("writer failure = %v, exit %d; want output error and exit 2", err, exitCodeFor(err))
			}
		})
	}
}

func TestPlanningCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, "alias", `{}`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	env := map[string]string{"TAILSCALE_TAILNET": "unused", "TAILSCALE_API_KEY": "unused"}
	code, out, errOut := runPlanning(ctx, env, commandPlan, "--registry-dir", dir, "--tailnet", "alias")
	assertUsageFailure(t, code, out)
	if !strings.Contains(errOut, "context canceled") {
		t.Errorf("stderr = %q, want cancellation", errOut)
	}
}
