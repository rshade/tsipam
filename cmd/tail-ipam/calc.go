package main

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/spf13/cobra"

	"github.com/rshade/tailscale-utils/policycore/ipcalc"
)

var errIPv6Unsupported = errors.New("IPv6 is not yet supported for 'calc split'; use an IPv4 CIDR")

func newCalcCommand(state *commandState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandCalc,
		Short: "Subnet and IP arithmetic (stateless).",
	}
	cmd.AddCommand(newCalcSplitCommand(state))
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

			if prefix.Addr().Is4In6() {
				normalized, ok := ipcalc.Unmap4In6(prefix)
				if !ok {
					return errIPv6Unsupported
				}
				prefix = normalized
			}

			if !prefix.Addr().Is4() {
				return errIPv6Unsupported
			}

			children, count, err := ipcalc.Split(prefix, prefixFlag)

			if errors.Is(err, ipcalc.ErrTooManyChildren) {
				return fmt.Errorf(
					"splitting %s into /%d blocks would produce %s subnets; too many to list (max %d); "+
						"use a shorter target or narrower parent: %w",
					cidrArg, prefixFlag, count.String(), ipcalc.MaxMaterializedChildren, err,
				)
			}

			if err != nil {
				return err
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
