package main

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/spf13/cobra"

	"github.com/rshade/tailscale-utils/policycore/ipcalc"
)

// wrapSplitError wraps a split error with context about 4-in-6 normalization if applicable.
func wrapSplitError(cidrArg string, prefix netip.Prefix, normalizedFrom4In6 bool, err error) error {
	if !normalizedFrom4In6 {
		return err
	}
	return fmt.Errorf("%s is an IPv4-mapped prefix, split as %s: %w", cidrArg, prefix, err)
}

func newCalcCommand(state *commandState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandCalc,
		Short: "Subnet and IP arithmetic (stateless).",
	}
	cmd.AddCommand(newCalcSplitCommand(state))
	cmd.AddCommand(newCalcInfoCommand(state))
	return cmd
}

func newCalcInfoCommand(state *commandState) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "info <cidr>",
		Short:         "Report network, broadcast, usable range, and host count for an IPv4 CIDR.",
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
			if errors.Is(err, ipcalc.ErrIPv6NotSupported) {
				return fmt.Errorf(
					"calc info does not support IPv6 yet (tracked in rshade/tailscale-utils#251): %s: %w",
					cidrArg, err)
			}
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
