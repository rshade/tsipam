package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
)

const (
	commandPlan  = "plan"
	commandCheck = "check"
	statusDrift  = "drift"
)

var errRegistryDrift = errors.New("registry differs from live allocations")

func newPlanningCommand(state *commandState, name string, collect liveSnapshotFunc) *cobra.Command {
	var tailnetFlag string
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
			return executePlanning(cmd, state, collect, tailnetFlag)
		},
	}
	addRegistryFlags(cmd, &tailnetFlag)
	return cmd
}

func executePlanning(
	cmd *cobra.Command, state *commandState, collect liveSnapshotFunc, tailnetFlag string,
) error {
	tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
	liveTailnet := state.getenv("TAILSCALE_TAILNET")
	outputSanitizer, err := state.outputSanitizer(tailnet)
	if err != nil {
		return err
	}
	registerSweepValue(outputSanitizer, kindTailnet, liveTailnet)
	if liveTailnet == "" {
		return &credentialsError{err: errors.New("TAILSCALE_TAILNET is required")}
	}
	if collect == nil {
		// Collection runs behind the loopback, which keeps no Go types, so a
		// missing credential is reported before the call.
		if err = checkLiveCredentials(state.getenv); err != nil {
			return err
		}
		collect = liveCollector(state.getenv)
	}
	target, err := state.registryTarget(cmd)
	if err != nil {
		return err
	}
	target.collectLive = collect
	var report *ipam.ComparisonReport
	err = target.run(cmd.Context(), func(ctx context.Context, client *ipamclient.Client) error {
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
	if err = emitOutput(cmd, result, outputSanitizer); err != nil {
		return err
	}
	if cmd.Name() == commandCheck && report.DriftCount > 0 {
		return &driftError{sentinel: errRegistryDrift, count: report.DriftCount}
	}
	return nil
}
