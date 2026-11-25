package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/spf13/cobra"

	"github.com/rshade/tsacl/pkg/ipcalc"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
)

func newCalcAggregateCommand(state *commandState) *cobra.Command {
	var fromRegistry bool
	var registryDir, tailnetFlag string
	var cidrFlags []string
	cmd := &cobra.Command{
		Use:   "aggregate [cidr...]",
		Short: "Summarize supplied or reserved CIDRs into their minimal covering set.",
		Long: "Summarize supplied CIDRs, or use --from-registry to read subnet-pool reservations.\n" +
			"Supplied CIDRs and --from-registry are mutually exclusive. Give CIDRs by position or\n" +
			"with a repeated --cidr, not both. Registry selectors --registry-dir and --tailnet\n" +
			"apply only with --from-registry; no live routes are read.",
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cidrs, err := aggregateSource(cmd, args, cidrFlags, fromRegistry)
			if err != nil {
				return err
			}
			tailnet := ""
			if fromRegistry {
				tailnet = resolveTailnetWithEnv(tailnetFlag, state.getenv)
			}
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return err
			}

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
			return emitOutput(cmd, output, outputSanitizer)
		},
	}
	cmd.Flags().StringArrayVar(&cidrFlags, flagCIDR, nil,
		"CIDR prefix to aggregate; repeat for each one (instead of [cidr...])")
	cmd.Flags().BoolVar(&fromRegistry, "from-registry", false,
		"aggregate subnet-pool reservations instead of positional CIDRs")
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}

// aggregateSource returns the supplied CIDRs, or none in registry mode, after
// refusing any combination of sources that contradicts itself.
func aggregateSource(cmd *cobra.Command, args, cidrFlags []string, fromRegistry bool) ([]string, error) {
	byFlag := cmd.Flags().Changed(flagCIDR)
	if fromRegistry {
		if len(args) != 0 || byFlag {
			return nil, invalidInput(errors.New("--from-registry and supplied CIDRs cannot be used together"))
		}
		return nil, nil
	}
	if cmd.Flags().Changed("registry-dir") || cmd.Flags().Changed("tailnet") {
		return nil, invalidInput(errors.New("--registry-dir and --tailnet require --from-registry"))
	}
	if len(args) != 0 {
		if byFlag {
			return nil, mixedInputs(flagCIDR)
		}
		return args, nil
	}
	if len(cidrFlags) == 0 {
		return nil, missingInputs([]inputSpec{cidrInput()})
	}
	return cidrFlags, nil
}

func aggregateRegistryCIDRs(
	ctx context.Context, state *commandState, registryDir, tailnet string,
) ([]string, error) {
	var cidrs []string
	err := state.registryTarget(registryDir).run(ctx,
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
			return nil, invalidInput(fmt.Errorf("invalid CIDR %q: %w", cidr, err))
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
