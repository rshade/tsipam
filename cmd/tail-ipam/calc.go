package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/spf13/cobra"

	"github.com/rshade/tailscale-utils/policycore/ipcalc"
	ipamclient "github.com/rshade/tailscale-utils/tail-ipam/gen/http/ipam/client"
	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/loopback"
)

// wrapSplitError wraps a split error with context about 4-in-6 normalization if applicable.
func wrapSplitError(cidrArg string, prefix netip.Prefix, normalizedFrom4In6 bool, err error) error {
	if !normalizedFrom4In6 {
		return err
	}
	return fmt.Errorf("%s is an IPv4-mapped prefix, split as %s: %w", cidrArg, prefix, err)
}

func newCalcCommand(state *commandState, listDevices devicesListFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandCalc,
		Short: "Subnet and IP arithmetic and allocation planning.",
	}
	cmd.AddCommand(newCalcSplitCommand(state))
	cmd.AddCommand(newCalcInfoCommand(state))
	cmd.AddCommand(newCalcAggregateCommand(state))
	cmd.AddCommand(newCalcNextFreeCommand(state, listDevices))
	return cmd
}

func newCalcNextFreeCommand(state *commandState, listDevices devicesListFunc) *cobra.Command {
	var registryDir, tailnetFlag string
	var size, count int
	var all bool

	cmd := &cobra.Command{
		Use:           "next-free <pool-cidr>",
		Short:         "Suggest available IPv4 or IPv6 child CIDRs from a subnet pool.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeCalcNextFree(
				cmd, state, listDevices, args[0], registryDir, tailnetFlag, size, count, all,
				cmd.Flags().Changed("count"),
			)
		},
	}

	cmd.Flags().IntVar(&size, "size", 0, "target prefix length (number of bits in each suggested child subnet)")
	cmd.Flags().IntVarP(&count, "count", "n", 1, "number of suggestions to return")
	cmd.Flags().BoolVar(&all, "all", false, "return every available child subnet")
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	_ = cmd.MarkFlagRequired("size")

	return cmd
}

func executeCalcNextFree(
	cmd *cobra.Command,
	state *commandState,
	listDevices devicesListFunc,
	poolArg, registryDir, tailnetFlag string,
	size, count int,
	all, countChanged bool,
) error {
	pool, err := nextFreePool(poolArg)
	if err != nil {
		return err
	}
	if validationErr := validateNextFreeOptions(
		pool, size, count, all, countChanged, listDevices,
	); validationErr != nil {
		return validationErr
	}

	tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
	outputSanitizer, err := state.outputSanitizer(tailnet)
	if err != nil {
		return err
	}
	// Exhaustion includes pool in its message but has no output block to
	// register, so register the canonical pool before rendering either form.
	registerSweepValue(outputSanitizer, kindCIDR, pool.String())

	var result *outputEnvelope
	err = loopback.RunWithLogger(cmd.Context(), registryDir, state.logger,
		func(ctx context.Context, client *ipamclient.Client) error {
			output, outputErr := nextFreeOutput(ctx, client, tailnet, listDevices, state.logger, pool, size, count, all)
			if outputErr != nil {
				return outputErr
			}
			result = output
			return nil
		})
	if err != nil {
		return err
	}
	// Mirrors calc split's ipcalc.ErrTooManyChildren precedent: a refusal
	// returns before anything reaches stdout, so the message lands on
	// stderr alone and downstream pipe consumers see empty input.
	if result.Status == statusExhausted {
		return errors.New(result.Message)
	}
	return emitOutput(cmd.OutOrStdout(), state.format, result, outputSanitizer)
}

func nextFreePool(arg string) (netip.Prefix, error) {
	pool, err := netip.ParsePrefix(arg)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid pool CIDR %q: %w", arg, err)
	}
	if normalized, ok := ipcalc.Unmap4In6(pool); ok {
		pool = normalized
	}
	return pool.Masked(), nil
}

func validateNextFreeOptions(
	pool netip.Prefix,
	size, count int,
	all, countChanged bool,
	listDevices devicesListFunc,
) error {
	if size < pool.Bits() || size > pool.Addr().BitLen() {
		return fmt.Errorf(
			"target prefix length /%d must be between pool /%d and address length /%d",
			size, pool.Bits(), pool.Addr().BitLen())
	}
	if count < 1 {
		return fmt.Errorf("count must be positive, got %d", count)
	}
	if all && countChanged {
		return errors.New("--all and --count cannot be used together")
	}
	if listDevices == nil {
		return errors.New("device collector is not configured")
	}
	return nil
}

func nextFreeOutput(
	ctx context.Context,
	client *ipamclient.Client,
	tailnet string,
	listDevices devicesListFunc,
	logger diagnostics.Logger,
	pool netip.Prefix,
	size, count int,
	all bool,
) (*outputEnvelope, error) {
	reservations, err := listSubnetPoolReservations(ctx, client, tailnet)
	if err != nil {
		return nil, err
	}
	devices, err := listDevices(ctx, logger)
	if err != nil {
		return nil, err
	}
	blocks := nextFreeBlocks(pool, size, nextFreeTaken(reservations, devices), count, all)
	message := fmt.Sprintf("suggested %d free block(s)", len(blocks))
	status := statusOK
	if len(blocks) == 0 {
		message = fmt.Sprintf("no free /%d block in %s", size, pool)
		status = statusExhausted
	}
	result := newOutput(commandCalcNextFree, status, message, tailnet)
	result.FreeBlocks = &blocks
	return result, nil
}

func listSubnetPoolReservations(
	ctx context.Context,
	client *ipamclient.Client,
	tailnet string,
) ([]*ipam.SubnetPoolReservation, error) {
	result, err := client.ListSubnetPools()(ctx, &ipam.ListSubnetPoolsPayload{Tailnet: tailnet})
	if err != nil {
		return nil, err
	}
	reservations, ok := result.([]*ipam.SubnetPoolReservation)
	if !ok {
		return nil, fmt.Errorf("unexpected list_subnet_pools result type %T", result)
	}
	return reservations, nil
}

func nextFreeTaken(reservations []*ipam.SubnetPoolReservation, devices []deviceRecord) []netip.Prefix {
	taken := make([]netip.Prefix, 0, len(reservations))
	for _, reservation := range reservations {
		prefix, err := netip.ParsePrefix(reservation.CIDR)
		if err == nil {
			taken = append(taken, prefix)
		}
	}
	for _, device := range devices {
		for _, route := range device.AdvertisedRoutes {
			prefix, err := netip.ParsePrefix(route)
			if err == nil {
				taken = append(taken, prefix)
			}
		}
	}
	return taken
}

func nextFreeBlocks(
	pool netip.Prefix,
	size int,
	taken []netip.Prefix,
	count int,
	all bool,
) []string {
	blocks := []string{}
	for all || len(blocks) < count {
		block, ok := ipcalc.NextFree(pool, size, taken)
		if !ok {
			return blocks
		}
		blocks = append(blocks, block.String())
		taken = append(taken, block)
	}
	return blocks
}

func newCalcInfoCommand(state *commandState) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "info <cidr>",
		Short:         "Report network, usable range, and host count for a CIDR.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cidrArg := args[0]

			outputSanitizer, err := state.outputSanitizer("")
			if err != nil {
				return err
			}

			prefix, err := netip.ParsePrefix(cidrArg)
			if err != nil {
				return fmt.Errorf("invalid CIDR %q: %w", cidrArg, err)
			}

			if normalized, ok := ipcalc.Unmap4In6(prefix); ok {
				prefix = normalized
			}

			masked := prefix.Masked()

			info, err := ipcalc.Info(prefix)
			if err != nil {
				return err
			}

			blockInfo := &blockInfoOutput{
				Prefix:      masked.String(),
				Network:     info.Network.String(),
				FirstUsable: info.FirstUsable.String(),
				LastUsable:  info.LastUsable.String(),
				UsableHosts: info.UsableHosts.String(),
			}
			if info.Broadcast.IsValid() {
				blockInfo.Broadcast = info.Broadcast.String()
			}

			message := fmt.Sprintf("block facts for %s", masked)
			output := newOutput(commandCalcInfo, statusOK, message, "")
			output.BlockInfo = blockInfo

			return emitOutput(cmd.OutOrStdout(), state.format, output, outputSanitizer)
		},
	}
	return cmd
}

func newCalcSplitCommand(state *commandState) *cobra.Command {
	var prefixFlag int

	cmd := &cobra.Command{
		Use:           "split <cidr>",
		Short:         "Split a CIDR into equal-sized child prefixes.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cidrArg := args[0]

			outputSanitizer, err := state.outputSanitizer("")
			if err != nil {
				return err
			}

			prefix, err := netip.ParsePrefix(cidrArg)
			if err != nil {
				return fmt.Errorf("invalid CIDR %q: %w", cidrArg, err)
			}

			var normalizedFrom4In6 bool
			if prefix.Addr().Is4In6() {
				normalized, ok := ipcalc.Unmap4In6(prefix)
				if ok {
					normalizedFrom4In6 = true
					prefix = normalized
				}
			}

			children, count, err := ipcalc.Split(prefix, prefixFlag)

			if errors.Is(err, ipcalc.ErrTooManyChildren) {
				err = fmt.Errorf(
					"splitting %s into /%d blocks would produce %s subnets; too many to list (max %d); "+
						"use a shorter target or narrower parent: %w",
					cidrArg, prefixFlag, count.String(), ipcalc.MaxMaterializedChildren, err,
				)
			}

			if err != nil {
				return wrapSplitError(cidrArg, prefix, normalizedFrom4In6, err)
			}

			subnets := make([]string, len(children))
			for i, child := range children {
				subnets[i] = child.String()
			}

			message := fmt.Sprintf("split %s into %d /%d subnets", cidrArg, len(children), prefixFlag)
			output := newOutput(commandCalcSplit, statusOK, message, "")
			output.Subnets = &subnets

			return emitOutput(cmd.OutOrStdout(), state.format, output, outputSanitizer)
		},
	}

	cmd.Flags().IntVar(&prefixFlag, "prefix", 0,
		"target prefix length (number of bits in each child subnet)")
	_ = cmd.MarkFlagRequired("prefix")

	return cmd
}
