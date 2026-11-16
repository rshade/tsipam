package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/diagnostics"
)

type devicesListFunc func(context.Context, diagnostics.Logger) ([]deviceRecord, error)

// newDevicesCommand is the first real (non-scaffold) command: a read-only
// proof that tsipam can reach the control plane through jaxxstorm/tscli's
// client construction rather than a hand-rolled one. It has no allocation
// logic yet.
func newDevicesCommand(state *commandState, listDevices devicesListFunc) *cobra.Command {
	devicesCmd := &cobra.Command{
		Use:   commandDevices,
		Short: "Inspect devices on the tailnet (read-only).",
	}
	devicesCmd.AddCommand(newDevicesListCommand(state, listDevices))
	return devicesCmd
}

func newDevicesListCommand(state *commandState, listDevices devicesListFunc) *cobra.Command {
	var tag string
	command := &cobra.Command{
		Use:           commandList,
		Short:         "List devices with addresses, routes, tags, and identity fields.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := normalizeDeviceTag(tag)
			if err != nil {
				return err
			}
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
			if filter != "" {
				records = filterDevicesByTag(records, filter)
			}
			result := newOutput(
				commandDevicesList, statusOK,
				fmt.Sprintf("listed %d device(s)", len(records)), tailnet,
			)
			result.Devices = &records
			return emitOutput(cmd.OutOrStdout(), state.format, result, outputSanitizer)
		},
	}
	command.Flags().StringVar(&tag, "tag", "",
		"Show only devices carrying this tag. A value without the tag: prefix is treated as tag:<value>.")
	return command
}

func normalizeDeviceTag(tag string) (string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", nil
	}
	if !strings.HasPrefix(tag, "tag:") {
		tag = "tag:" + tag
	}
	if tag == "tag:" {
		return "", errors.New("tag name is empty")
	}
	return tag, nil
}

func filterDevicesByTag(records []deviceRecord, tag string) []deviceRecord {
	matched := make([]deviceRecord, 0)
	for _, record := range records {
		if slices.Contains(record.Tags, tag) {
			matched = append(matched, record)
		}
	}
	return matched
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
			EnabledRoutes:    device.EnabledRoutes,
			Tags:             device.Tags,
			User:             device.User,
			OS:               device.OS,
			Hostname:         device.Hostname,
			Authorized:       device.Authorized,
			Ephemeral:        device.Ephemeral,
		}
	}
	return records, nil
}
