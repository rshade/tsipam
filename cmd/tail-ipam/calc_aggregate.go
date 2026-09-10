package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/spf13/cobra"

	"github.com/rshade/tailscale-utils/policycore/ipcalc"
	ipamclient "github.com/rshade/tailscale-utils/tail-ipam/gen/http/ipam/client"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/loopback"
)

func newCalcAggregateCommand(state *commandState) *cobra.Command {
	var fromRegistry bool
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:   "aggregate [cidr...]",
		Short: "Summarize supplied or reserved CIDRs into their minimal covering set.",
		Long: "Summarize supplied CIDRs, or use --from-registry to read subnet-pool reservations.\n" +
			"Positional CIDRs and --from-registry are mutually exclusive. Registry selectors\n" +
			"--registry-dir and --tailnet apply only with --from-registry; no live routes are read.",
		Args: func(cmd *cobra.Command, args []string) error {
			if fromRegistry {
				if len(args) != 0 {
					return errors.New("--from-registry and positional CIDRs cannot be used together")
				}
				return nil
			}
			if cmd.Flags().Changed("registry-dir") || cmd.Flags().Changed("tailnet") {
				return errors.New("--registry-dir and --tailnet require --from-registry")
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tailnet := ""
			if fromRegistry {
				tailnet = resolveTailnetWithEnv(tailnetFlag, state.getenv)
			}
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return err
			}

			cidrs := args
			if fromRegistry {
				cidrs, err = aggregateRegistryCIDRs(cmd.Context(), state, registryDir, tailnet)
				if err != nil {
					return err
				}
			}
			output, err := aggregateOutput(cidrs, tailnet)
			if err != nil {
				return err
			}
			output.supernetsFromRegistry = fromRegistry
			return emitOutput(cmd.OutOrStdout(), state.format, output, outputSanitizer)
		},
	}
	cmd.Flags().BoolVar(&fromRegistry, "from-registry", false,
		"aggregate subnet-pool reservations instead of positional CIDRs")
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}

func aggregateRegistryCIDRs(
	ctx context.Context, state *commandState, registryDir, tailnet string,
) ([]string, error) {
	var cidrs []string
	err := loopback.RunWithLogger(ctx, registryDir, state.logger,
		func(callCtx context.Context, client *ipamclient.Client) error {
			reservations, listErr := listSubnetPoolReservations(callCtx, client, tailnet)
			if listErr != nil {
				return listErr
			}
			cidrs = make([]string, len(reservations))
			for i, reservation := range reservations {
				cidrs[i] = reservation.CIDR
			}
			return nil
		})
	return cidrs, err
}

func aggregateOutput(cidrs []string, tailnet string) (*outputEnvelope, error) {
	prefixes := make([]netip.Prefix, len(cidrs))
	for i, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
		if normalized, ok := ipcalc.Unmap4In6(prefix); ok {
			prefix = normalized
		}
		prefixes[i] = prefix.Masked()
	}

	aggregated := ipcalc.Aggregate(prefixes)
	supernets := make([]string, len(aggregated))
	for i, prefix := range aggregated {
		supernets[i] = prefix.String()
	}
	output := newOutput(
		commandCalcAggregate, statusOK,
		fmt.Sprintf("aggregated %d CIDR(s) into %d supernet(s)", len(cidrs), len(supernets)), tailnet,
	)
	output.Supernets = &supernets
	return output, nil
}
