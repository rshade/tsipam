package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/devices"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
)

type devicesListFunc func(context.Context, diagnostics.Logger) ([]deviceRecord, error)

// newDevicesCommand is the first real (non-scaffold) command: a read-only
// proof that tail-ipam can reach the control plane through jaxxstorm/tscli's
// client construction rather than a hand-rolled one. It has no allocation
// logic yet -- see ROADMAP.md ("Future Vision" -> tail-ipam) for that design.
func newDevicesCommand(state *commandState, listDevices devicesListFunc) *cobra.Command {
	devicesCmd := &cobra.Command{
		Use:   commandDevices,
		Short: "Inspect devices on the tailnet (read-only).",
	}
	devicesCmd.AddCommand(newDevicesListCommand(state, listDevices))
	return devicesCmd
}

func newDevicesListCommand(state *commandState, listDevices devicesListFunc) *cobra.Command {
	return &cobra.Command{
		Use:           commandList,
		Short:         "List devices with their addresses and advertised routes.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tailnet := state.getenv("TAILSCALE_TAILNET")
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return err
			}
			if listDevices == nil {
				return errors.New("device collector is not configured")
			}
			records, err := listDevices(cmd.Context(), state.logger)
			if err != nil {
				return err
			}
			result := newOutput(
				commandDevicesList, statusOK,
				fmt.Sprintf("listed %d device(s)", len(records)), tailnet,
			)
			result.Devices = &records
			return emitOutput(cmd.OutOrStdout(), state.format, result, outputSanitizer)
		},
	}
}

// collectDevices adapts internal/devices' collected records into the CLI's
// output-envelope shape.
func collectDevices(
	ctx context.Context,
	getenv func(string) string,
	logger diagnostics.Logger,
) ([]deviceRecord, error) {
	collected, err := devices.List(ctx, getenv, logger)
	if err != nil {
		return nil, err
	}
	records := make([]deviceRecord, len(collected))
	for index, device := range collected {
		records[index] = deviceRecord{
			Name:             device.Name,
			Addresses:        device.Addresses,
			AdvertisedRoutes: device.AdvertisedRoutes,
		}
	}
	return records, nil
}
