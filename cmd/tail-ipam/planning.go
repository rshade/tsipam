package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	ipamclient "github.com/rshade/tailscale-utils/tail-ipam/gen/http/ipam/client"
	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/devices"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/ipamsvc"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/loopback"
)

const (
	commandPlan  = "plan"
	commandCheck = "check"
	statusDrift  = "drift"
)

var errRegistryDrift = errors.New("registry differs from live allocations")

type liveSnapshotFunc func(context.Context, string, diagnostics.Logger) (devices.Snapshot, error)

func newPlanningCommand(state *commandState, name string, collect liveSnapshotFunc) *cobra.Command {
	var registryDir, tailnetFlag string
	summary := "Compare registry allocations with live state (read-only)."
	if name == commandCheck {
		summary = "Check registry allocations against live state; exit 1 on drift."
	}
	cmd := &cobra.Command{
		Use: name, Short: summary,
		Long: summary + "\n\n" +
			"Compare site IDs, subnet pools, and service IPv4 addresses with live devices and Services.\n" +
			"Requires TAILSCALE_TAILNET and API-key or OAuth credentials, including Services read access.\n" +
			"--tailnet selects the registry alias; TAILSCALE_TAILNET selects the live source.\n" +
			"A pool matches contained advertised routes. Site labels do not identify physical routers.\n" +
			"UDP listen ports are excluded: the control plane does not expose their live configuration.\n" +
			"Plan exits 0 after comparison; check exits 1 on drift. Incomplete collection exits 2.\n" +
			"Comparison identities are redacted by --sanitize identity or strict.",
		Args: cobra.NoArgs, SilenceErrors: true, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executePlanning(cmd, state, collect, registryDir, tailnetFlag)
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}

func executePlanning(
	cmd *cobra.Command, state *commandState, collect liveSnapshotFunc, registryDir, tailnetFlag string,
) error {
	tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
	liveTailnet := state.getenv("TAILSCALE_TAILNET")
	outputSanitizer, err := state.outputSanitizer(tailnet)
	if err != nil {
		return err
	}
	registerSweepValue(outputSanitizer, kindTailnet, liveTailnet)
	if liveTailnet == "" {
		return errors.New("TAILSCALE_TAILNET is required")
	}
	if collect == nil {
		collect = func(ctx context.Context, source string, logger diagnostics.Logger) (devices.Snapshot, error) {
			getenv := func(key string) string {
				if key == "TAILSCALE_TAILNET" {
					return source
				}
				return state.getenv(key)
			}
			return devices.CollectSnapshot(ctx, getenv, logger)
		}
	}
	svc := &ipamsvc.Service{RegistryDir: registryDir, Logger: state.logger, CollectLive: collect}
	var report *ipam.ComparisonReport
	err = loopback.RunWithService(cmd.Context(), svc, func(ctx context.Context, client *ipamclient.Client) error {
		result, callErr := client.Plan()(ctx, &ipam.PlanPayload{Tailnet: tailnet, LiveTailnet: liveTailnet})
		if callErr != nil {
			return callErr
		}
		var ok bool
		report, ok = result.(*ipam.ComparisonReport)
		if !ok || report == nil {
			return fmt.Errorf("unexpected plan result type %T", result)
		}
		return nil
	})
	if err != nil {
		return err
	}
	result := comparisonEnvelope(cmd.Name(), tailnet, report)
	if err = emitOutput(cmd.OutOrStdout(), state.format, result, outputSanitizer); err != nil {
		return err
	}
	if cmd.Name() == commandCheck && report.DriftCount > 0 {
		return errRegistryDrift
	}
	return nil
}
