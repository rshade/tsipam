package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/spf13/cobra"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
)

const commandCheckPolicy = "check-policy"

var errPolicyDrift = errors.New("authored ipsets differ from reserved subnet pools")

func newPolicyCheckCommand(state *commandState) *cobra.Command {
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:   "check-policy <acl-file>",
		Short: "Check authored ipsets against subnet reservations; exit 1 on drift.",
		Long: "Compare every ipset definition in a local HuJSON policy with reserved subnet pools.\n\n" +
			"Reads local files only; no API credentials, live collection, or file changes.\n" +
			"Supports addresses, CIDRs, ranges, host: aliases, nested ipset: references, and ordered add/remove.\n" +
			"Autogroups (including autogroup:internet) and dynamic selectors are unsupported and cause exit 2.\n" +
			"Each effective CIDR needs one containing reservation; adjacent pools do not combine.\n" +
			"A pool is referenced by any overlapping effective CIDR, even in an unused ipset definition.\n" +
			"ACL/grant reachability and other allocation kinds are outside this check.\n" +
			"Expansion is limited to 128 nesting levels and 65536 prefixes across definitions.\n" +
			"Exits 0 for consistency, 1 for drift, or 2 for incomplete input or output errors.\n" +
			"--sanitize identity or strict redacts ipset names, CIDRs, sites, and registry aliases.",
		Args: cobra.ExactArgs(1), SilenceErrors: true, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return executePolicyCheck(cmd, state, registryDir, tailnetFlag, args[0])
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}

func executePolicyCheck(
	cmd *cobra.Command, state *commandState, registryDir, tailnetFlag, policyPath string,
) error {
	tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
	outputSanitizer, err := state.outputSanitizer(tailnet)
	if err != nil {
		return err
	}
	registerSweepValue(outputSanitizer, kindPath, policyPath)
	policy, err := os.ReadFile(policyPath)
	if err != nil {
		return fmt.Errorf("reading policy: %w", err)
	}
	// JSON string encoding repairs invalid UTF-8; reject it before transport
	// so the service compares the actual authored policy, never repaired text.
	if !utf8.Valid(policy) {
		return errors.New("policy must contain valid UTF-8")
	}
	var report *ipam.PolicyComparison
	err = state.registryTarget(registryDir).run(cmd.Context(),
		func(ctx context.Context, client *ipamclient.Client) error {
			result, callErr := client.CheckPolicy()(
				ctx,
				&ipam.CheckPolicyPayload{Tailnet: tailnet, Policy: string(policy)},
			)
			if callErr != nil {
				return callErr
			}
			var ok bool
			report, ok = result.(*ipam.PolicyComparison)
			if !ok || report == nil {
				return fmt.Errorf("unexpected policy comparison result type %T", result)
			}
			return nil
		})
	if err != nil {
		return err
	}
	if err = emitOutput(
		cmd.OutOrStdout(),
		state.format,
		policyComparisonEnvelope(tailnet, report),
		outputSanitizer,
	); err != nil {
		return err
	}
	if report.DriftCount > 0 {
		return errPolicyDrift
	}
	return nil
}
