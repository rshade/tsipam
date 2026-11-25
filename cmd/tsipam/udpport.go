package main

import (
	"context"
	"fmt"

	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/registry"
)

// udpPortReserveMessage and udpPortReleaseMessage build user-facing text from
// the output envelope after sanitization, keeping message and structured fields
// synchronized.
func udpPortReserveMessage(output *outputEnvelope) string {
	return outcomeMessage(output, "reserved", "reserve",
		fmt.Sprintf("UDP port %s in site %q for %q", output.Port, output.Site, output.Device),
		fmt.Sprintf("UDP port %s in site %q already reserved for %q", output.Port, output.Site, output.Device))
}

func udpPortReleaseMessage(output *outputEnvelope) string {
	return outcomeMessage(output, "released", "release",
		fmt.Sprintf("UDP port %s from site %q", output.Port, output.Site),
		fmt.Sprintf("UDP port %s in site %q was not reserved", output.Port, output.Site))
}

// newUDPPortCommand groups explicit per-device UDP listen-port reservations.
// Port uniqueness is scoped to the site flag, which represents the shared
// firewall boundary.
func newUDPPortCommand(state *commandState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandPort,
		Short: "Manage per-device UDP listen-port reservations.",
	}
	cmd.AddCommand(newUDPPortReserveCommand(state))
	cmd.AddCommand(newUDPPortReleaseCommand(state))
	cmd.AddCommand(newUDPPortListCommand(state))
	return cmd
}

func newUDPPortReserveCommand(state *commandState) *cobra.Command {
	return newUDPPortLeafCommand(
		state,
		"reserve <port> <device>",
		"Reserve a UDP listen port for a device within a site.",
		[]inputSpec{portInput(), deviceInput()},
		true,
		1,
		func(
			ctx context.Context,
			client *ipamclient.Client,
			args []string,
			tailnet, site string,
			_ bool,
		) (*outputEnvelope, error) {
			res, err := client.ReserveUDPPort()(ctx, &ipam.ReserveUDPPortPayload{
				DryRun: ax.DryRunFromContext(ctx), Tailnet: tailnet,
				Site:   site,
				Port:   args[0],
				Device: args[1],
			})
			if err != nil {
				return nil, err
			}
			result, ok := res.(*ipam.ReserveUDPPortResult)
			if !ok {
				return nil, fmt.Errorf("unexpected reserve_udp_port result type %T", res)
			}
			output := newOutput(commandPortReserve, changedStatus(result.Changed), "", tailnet)
			output.Changed = &result.Changed
			output.Site = result.Site
			output.Port = result.Port
			output.Device = result.Device
			output.messageBuilder = udpPortReserveMessage
			return output, nil
		},
	)
}

func newUDPPortReleaseCommand(state *commandState) *cobra.Command {
	return newUDPPortLeafCommand(
		state,
		"release <port>",
		"Release a UDP listen-port reservation from a site.",
		[]inputSpec{portInput()},
		true,
		-1,
		func(
			ctx context.Context,
			client *ipamclient.Client,
			args []string,
			tailnet, site string,
			_ bool,
		) (*outputEnvelope, error) {
			res, err := client.ReleaseUDPPort()(ctx, &ipam.ReleaseUDPPortPayload{
				DryRun: ax.DryRunFromContext(ctx), Tailnet: tailnet,
				Site: site,
				Port: args[0],
			})
			if err != nil {
				return nil, err
			}
			result, ok := res.(*ipam.ReleaseUDPPortResult)
			if !ok {
				return nil, fmt.Errorf("unexpected release_udp_port result type %T", res)
			}
			output := newOutput(commandPortRelease, changedStatus(result.Changed), "", tailnet)
			output.Changed = &result.Changed
			output.Site = result.Site
			output.Port = result.Port
			output.messageBuilder = udpPortReleaseMessage
			return output, nil
		},
	)
}

func newUDPPortListCommand(state *commandState) *cobra.Command {
	return newUDPPortLeafCommand(
		state,
		commandList,
		"List UDP listen-port reservations, optionally filtered by site.",
		nil,
		false,
		-1,
		func(
			ctx context.Context,
			client *ipamclient.Client,
			_ []string,
			tailnet, site string,
			siteSet bool,
		) (*outputEnvelope, error) {
			var siteFilter *string
			if siteSet {
				siteFilter = &site
			}
			res, err := client.ListUDPPorts()(ctx, &ipam.ListUDPPortsPayload{
				Tailnet: tailnet,
				Site:    siteFilter,
			})
			if err != nil {
				return nil, err
			}
			entries, ok := res.([]*ipam.UDPPortReservation)
			if !ok {
				return nil, fmt.Errorf("unexpected list_udp_ports result type %T", res)
			}
			outputEntries := make([]udpPortOutput, len(entries))
			for index, entry := range entries {
				outputEntries[index] = udpPortOutput{
					Site: entry.Site, Port: entry.Port, Device: entry.Device,
				}
			}
			output := newOutput(
				commandPortList,
				statusOK,
				fmt.Sprintf("listed %d UDP port reservation(s)", len(entries)),
				tailnet,
			)
			output.UDPPorts = &outputEntries
			return output, nil
		},
	)
}

type udpPortLeafCall func(
	context.Context,
	*ipamclient.Client,
	[]string,
	string,
	string,
	bool,
) (*outputEnvelope, error)

// registerInvalidPort registers the port argument for the error sweep when it is
// not a port. Port numbers stay visible, but anything else the caller put there
// may be an identity, and the validation error echoes it.
func registerInvalidPort(sanitizer *sanitize.Sanitizer, args []string) {
	if len(args) == 0 {
		return
	}
	if _, err := registry.NormalizePort(args[0]); err != nil {
		registerSweepValueWithCanonical(sanitizer, kindPort, args[0], registry.NormalizePort)
	}
}

// newUDPPortLeafCommand centralizes registry/tailnet flags, optional versus
// required site handling, identity registration, and loopback traversal for
// all three UDP-port operations.
func newUDPPortLeafCommand(
	state *commandState,
	use, short string,
	specs []inputSpec,
	siteRequired bool,
	deviceArgIndex int,
	call udpPortLeafCall,
) *cobra.Command {
	var registryDir, tailnetFlag, site string
	var bound inputs
	cmd := &cobra.Command{
		Use:           use,
		Short:         short,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			args, err := bound.resolve(cmd, args)
			if err != nil {
				return err
			}
			tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return err
			}
			registerInvalidPort(outputSanitizer, args)
			siteSet := cmd.Flags().Changed("site")
			if siteSet {
				if validateErr := registerAndValidate(
					outputSanitizer, kindSite, site, registry.NormalizeSiteName,
				); validateErr != nil {
					return validateErr
				}
			}
			if deviceArgIndex >= 0 && deviceArgIndex < len(args) {
				if validateErr := registerAndValidate(
					outputSanitizer, kindHostname, args[deviceArgIndex], registry.NormalizeDeviceName,
				); validateErr != nil {
					return validateErr
				}
			}
			if err = state.approve(cmd, args, registry.NormalizePort); err != nil {
				return err
			}
			return state.runRegistryCall(cmd, registryDir, outputSanitizer,
				func(ctx context.Context, client *ipamclient.Client) (*outputEnvelope, error) {
					return call(ctx, client, args, tailnet, site, siteSet)
				})
		},
	}
	bound = bindInputs(cmd, specs...)
	cmd.Flags().StringVar(&site, "site", "", "site/firewall-boundary label")
	if siteRequired {
		_ = cmd.MarkFlagRequired("site")
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}
