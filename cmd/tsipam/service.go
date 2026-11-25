package main

import (
	"context"
	"fmt"

	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/registry"
)

// serviceReserveMessage and serviceReleaseMessage build user-facing text
// from the output envelope after sanitization, keeping message and structured
// fields synchronized.
func serviceReserveMessage(output *outputEnvelope) string {
	return outcomeMessage(output, "reserved", "reserve",
		fmt.Sprintf("service address %s for %q", output.Address, output.Service),
		fmt.Sprintf("service address %s already reserved for %q", output.Address, output.Service))
}

func serviceReleaseMessage(output *outputEnvelope) string {
	return outcomeMessage(output, "released", "release",
		fmt.Sprintf("service address %s", output.Address),
		fmt.Sprintf("service address %s was not reserved", output.Address))
}

// newServiceCommand groups the per-service IPv4 address reservations. Every
// leaf traverses the generated Goa HTTP client and ephemeral loopback server;
// this command performs address bookkeeping only and does not contact the
// Tailscale control plane.
func newServiceCommand(state *commandState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandService,
		Short: "Manage per-service IPv4 address reservations.",
	}
	cmd.AddCommand(newServiceReserveCommand(state))
	cmd.AddCommand(newServiceReleaseCommand(state))
	cmd.AddCommand(newServiceListCommand(state))
	return cmd
}

func newServiceReserveCommand(state *commandState) *cobra.Command {
	var metadataFlags reservationMetadataFlags
	cmd := newServiceLeafCommand(
		state, "reserve <address> <service-name>", "Reserve an IPv4 host address for a Tailscale Service.",
		[]inputSpec{addressInput(), serviceInput()}, 0, 1, &metadataFlags,
		func(ctx context.Context, client *ipamclient.Client, args []string, tailnet string) (*outputEnvelope, error) {
			owner, description, ticket, reservedAt := metadataFlags.asPointers()
			response, err := client.ReserveServiceAddress()(ctx, &ipam.ReserveServiceAddressPayload{
				DryRun: ax.DryRunFromContext(ctx), Tailnet: tailnet, Address: args[0], Service: args[1],
				Owner: owner, Description: description, Ticket: ticket, ReservedAt: reservedAt,
			})
			if err != nil {
				return nil, err
			}
			result, ok := response.(*ipam.ReserveServiceAddressResult)
			if !ok {
				return nil, fmt.Errorf("unexpected reserve_service_address result type %T", response)
			}
			output := newOutput(commandServiceReserve, changedStatus(result.Changed), "", tailnet)
			output.Changed = &result.Changed
			output.Address = result.Address
			output.Service = result.Service
			output.reservationMetadataOutput = metadataOutputFrom(
				result.Owner, result.Description, result.Ticket, result.ReservedAt,
			)
			output.messageBuilder = serviceReserveMessage
			return output, nil
		},
	)
	addMetadataFlags(cmd, &metadataFlags)
	return cmd
}

func newServiceReleaseCommand(state *commandState) *cobra.Command {
	return newServiceLeafCommand(
		state, "release <address>", "Release a reserved service address.", []inputSpec{addressInput()}, 0, -1, nil,
		func(ctx context.Context, client *ipamclient.Client, args []string, tailnet string) (*outputEnvelope, error) {
			response, err := client.ReleaseServiceAddress()(ctx, &ipam.ReleaseServiceAddressPayload{
				DryRun: ax.DryRunFromContext(ctx), Tailnet: tailnet, Address: args[0],
			})
			if err != nil {
				return nil, err
			}
			result, ok := response.(*ipam.ReleaseServiceAddressResult)
			if !ok {
				return nil, fmt.Errorf("unexpected release_service_address result type %T", response)
			}
			output := newOutput(commandServiceRelease, changedStatus(result.Changed), "", tailnet)
			output.Changed = &result.Changed
			output.Address = result.Address
			output.messageBuilder = serviceReleaseMessage
			return output, nil
		},
	)
}

//nolint:dupl // mirrors newSubnetListCommand (subnet.go) by design; unifying needs a generic beyond this file's scope
func newServiceListCommand(state *commandState) *cobra.Command {
	return newServiceLeafCommand(
		state, commandList, "List reserved service addresses.", nil, -1, -1, nil,
		func(ctx context.Context, client *ipamclient.Client, _ []string, tailnet string) (*outputEnvelope, error) {
			response, err := client.ListServiceAddresses()(ctx, &ipam.ListServiceAddressesPayload{Tailnet: tailnet})
			if err != nil {
				return nil, err
			}
			entries, ok := response.([]*ipam.ServiceAddressReservation)
			if !ok {
				return nil, fmt.Errorf("unexpected list_service_addresses result type %T", response)
			}
			outputEntries := make([]serviceAddressOutput, len(entries))
			for index, entry := range entries {
				outputEntries[index] = serviceAddressOutput{
					Address: entry.Address, Service: entry.Service,
					reservationMetadataOutput: metadataOutputFrom(
						entry.Owner, entry.Description, entry.Ticket, entry.ReservedAt,
					),
				}
			}
			output := newOutput(
				commandServiceList, statusOK,
				fmt.Sprintf("listed %d service-address reservation(s)", len(entries)), tailnet,
			)
			output.ServiceAddresses = &outputEntries
			return output, nil
		},
	)
}

type serviceLeafCall func(
	context.Context,
	*ipamclient.Client,
	[]string,
	string,
) (*outputEnvelope, error)

// newServiceLeafCommand mirrors newSiteIDLeafCommand (siteid.go) by design;
// see its doc comment for why the two share a shape rather than a generic.
func newServiceLeafCommand(
	state *commandState,
	use, short string,
	specs []inputSpec,
	addressArgIndex, serviceArgIndex int,
	metadataFlags *reservationMetadataFlags,
	call serviceLeafCall,
) *cobra.Command {
	var tailnetFlag string
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
			if addressArgIndex >= 0 && addressArgIndex < len(args) {
				registerSweepValueWithCanonical(
					outputSanitizer, kindIP, args[addressArgIndex], registry.NormalizeServiceAddress,
				)
			}
			if serviceArgIndex >= 0 && serviceArgIndex < len(args) {
				if validateErr := registerAndValidate(
					outputSanitizer, kindService, args[serviceArgIndex], registry.NormalizeServiceName,
				); validateErr != nil {
					return validateErr
				}
			}
			if metadataFlags != nil && metadataFlags.owner != "" {
				registerSweepValue(outputSanitizer, kindOwner, metadataFlags.owner)
			}
			if err = state.approve(cmd, args, registry.NormalizeServiceAddress); err != nil {
				return err
			}
			return state.runRegistryCall(cmd, outputSanitizer,
				func(ctx context.Context, client *ipamclient.Client) (*outputEnvelope, error) {
					return call(ctx, client, args, tailnet)
				})
		},
	}
	bound = bindInputs(cmd, specs...)
	addRegistryFlags(cmd, &tailnetFlag)
	return cmd
}
