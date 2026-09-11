package main

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rshade/tailscale-utils/policycore/sanitize"
	ipamclient "github.com/rshade/tailscale-utils/tail-ipam/gen/http/ipam/client"
	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/loopback"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

const (
	subnetReserveArgs  = 2
	subnetAllocateArgs = 2
)

// subnetReserveMessage and subnetReleaseMessage render their command's
// outcome message from o's own CIDR/Site fields, at emit time -- see
// outputEnvelope.messageBuilder's doc comment. Reading o.Site rather than
// closing over the raw API result means a builder invoked after
// sanitizeOutput sees the already-redacted placeholder.
func subnetReserveMessage(o *outputEnvelope) string {
	if o.Changed != nil && *o.Changed {
		return fmt.Sprintf("reserved subnet CIDR %s for %q", o.CIDR, o.Site)
	}
	return fmt.Sprintf("subnet CIDR %s already reserved for %q", o.CIDR, o.Site)
}

func subnetReleaseMessage(o *outputEnvelope) string {
	if o.Changed != nil && *o.Changed {
		return fmt.Sprintf("released subnet CIDR %s", o.CIDR)
	}
	return fmt.Sprintf("subnet CIDR %s was not reserved", o.CIDR)
}

func subnetAllocateMessage(o *outputEnvelope) string {
	if o.Changed != nil && *o.Changed {
		return fmt.Sprintf("allocated subnet CIDR %s for %q", o.CIDR, o.Site)
	}
	return fmt.Sprintf("subnet CIDR %s already allocated for %q", o.CIDR, o.Site)
}

// newSubnetCommand groups subnet-CIDR registry commands. Like site-id, every
// operation traverses the generated Goa HTTP client and loopback server.
func newSubnetCommand(state *commandState, listDevices devicesListFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandSubnet,
		Short: "Manage subnet CIDR pool reservations.",
	}
	cmd.AddCommand(newSubnetReserveCommand(state))
	cmd.AddCommand(newSubnetAllocateCommand(state, listDevices))
	cmd.AddCommand(newSubnetReleaseCommand(state))
	cmd.AddCommand(newSubnetListCommand(state))
	return cmd
}

// newSubnetReserveCommand structurally mirrors newSiteIDReserveCommand
// (siteid.go) by design: each reserves its own registry entry shape through
// the shared leaf-command plumbing.
//
//nolint:dupl // mirrors newSiteIDReserveCommand (siteid.go) by design; unifying needs a generic beyond this file's scope
func newSubnetReserveCommand(state *commandState) *cobra.Command {
	var metadataFlags reservationMetadataFlags
	cmd := newSubnetLeafCommand(
		state, "reserve <cidr> <site-name>", "Reserve a subnet CIDR pool for a site.",
		cobra.ExactArgs(subnetReserveArgs), 0, 1, &metadataFlags,
		func(ctx context.Context, client *ipamclient.Client, args []string, tailnet string) (*outputEnvelope, error) {
			owner, description, ticket, reservedAt := metadataFlags.asPointers()
			res, err := client.ReserveSubnetPool()(ctx, &ipam.ReserveSubnetPoolPayload{
				Tailnet: tailnet, CIDR: args[0], Site: args[1],
				Owner: owner, Description: description, Ticket: ticket, ReservedAt: reservedAt,
			})
			if err != nil {
				return nil, err
			}
			result, ok := res.(*ipam.ReserveSubnetPoolResult)
			if !ok {
				return nil, fmt.Errorf("unexpected reserve_subnet_pool result type %T", res)
			}
			status := changedStatus(result.Changed)
			output := newOutput(commandSubnetReserve, status, "", tailnet)
			output.Changed = &result.Changed
			output.CIDR = result.CIDR
			output.Site = result.Site
			output.reservationMetadataOutput = metadataOutputFrom(
				result.Owner, result.Description, result.Ticket, result.ReservedAt,
			)
			output.messageBuilder = subnetReserveMessage
			return output, nil
		},
	)
	addMetadataFlags(cmd, &metadataFlags)
	return cmd
}

// newSubnetAllocateCommand composes calc next-free's pick with subnet
// reserve's write in one registry transaction. Live routes are collected
// here, before the service call, exactly as next-free collects them: they
// are not registry state, so the lock never spans the API read -- only the
// pick and the write (see registry.AllocateSubnetPoolWithLogger).
func newSubnetAllocateCommand(state *commandState, listDevices devicesListFunc) *cobra.Command {
	var metadataFlags reservationMetadataFlags
	var size int
	cmd := newSubnetLeafCommand(
		state, "allocate <pool-cidr> <site-name>", "Reserve the next free child CIDR of a pool for a site.",
		cobra.ExactArgs(subnetAllocateArgs), 0, 1, &metadataFlags,
		func(ctx context.Context, client *ipamclient.Client, args []string, tailnet string) (*outputEnvelope, error) {
			pool, err := registry.NormalizeAllocationPool(args[0], size)
			if err != nil {
				return nil, err
			}
			if listDevices == nil {
				return nil, errNoDeviceCollector
			}
			// The leaf helper registered the raw pool argument; the exhaustion
			// message names the unmapped, masked form, so register that too.
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return nil, err
			}
			registerSweepValue(outputSanitizer, kindCIDR, pool.String())

			devices, err := listDevices(ctx, state.logger)
			if err != nil {
				return nil, err
			}
			owner, description, ticket, reservedAt := metadataFlags.asPointers()
			res, err := client.AllocateSubnetPool()(ctx, &ipam.AllocateSubnetPoolPayload{
				Tailnet: tailnet, Pool: pool.String(), Size: size, Site: args[1],
				LiveTaken: liveTakenRoutes(devices),
				Owner:     owner, Description: description, Ticket: ticket, ReservedAt: reservedAt,
			})
			if err != nil {
				return nil, err
			}
			result, ok := res.(*ipam.AllocateSubnetPoolResult)
			if !ok {
				return nil, fmt.Errorf("unexpected allocate_subnet_pool result type %T", res)
			}
			output := newOutput(commandSubnetAllocate, changedStatus(result.Changed), "", tailnet)
			output.Changed = &result.Changed
			output.CIDR = result.CIDR
			output.Site = result.Site
			output.reservationMetadataOutput = metadataOutputFrom(
				result.Owner, result.Description, result.Ticket, result.ReservedAt,
			)
			output.messageBuilder = subnetAllocateMessage
			return output, nil
		},
	)
	cmd.Flags().IntVar(&size, "size", 0, "prefix length of the child subnet to allocate")
	_ = cmd.MarkFlagRequired("size")
	addMetadataFlags(cmd, &metadataFlags)
	return cmd
}

// liveTakenRoutes applies next-free's route parse, then renders the result
// for the string-typed allocate payload.
func liveTakenRoutes(devices []deviceRecord) []string {
	taken := nextFreeTaken(nil, devices)
	routes := make([]string, len(taken))
	for index, prefix := range taken {
		routes[index] = prefix.String()
	}
	return routes
}

func newSubnetReleaseCommand(state *commandState) *cobra.Command {
	return newSubnetLeafCommand(
		state, "release <cidr>", "Release a reserved subnet CIDR pool.", cobra.ExactArgs(1), 0, -1, nil,
		func(ctx context.Context, client *ipamclient.Client, args []string, tailnet string) (*outputEnvelope, error) {
			res, err := client.ReleaseSubnetPool()(ctx, &ipam.ReleaseSubnetPoolPayload{
				Tailnet: tailnet, CIDR: args[0],
			})
			if err != nil {
				return nil, err
			}
			result, ok := res.(*ipam.ReleaseSubnetPoolResult)
			if !ok {
				return nil, fmt.Errorf("unexpected release_subnet_pool result type %T", res)
			}
			status := changedStatus(result.Changed)
			output := newOutput(commandSubnetRelease, status, "", tailnet)
			output.Changed = &result.Changed
			output.CIDR = result.CIDR
			output.messageBuilder = subnetReleaseMessage
			return output, nil
		},
	)
}

// newSubnetListCommand structurally mirrors newServiceListCommand (service.go)
// by design: each lists its own registry entry shape through the shared
// leaf-command plumbing, and unifying them would need a generic beyond this
// file's scope.
//
//nolint:dupl // mirrors newServiceListCommand (service.go) by design; unifying needs a generic beyond this file's scope
func newSubnetListCommand(state *commandState) *cobra.Command {
	return newSubnetLeafCommand(
		state, commandList, "List reserved subnet CIDR pools.", cobra.NoArgs, -1, -1, nil,
		func(ctx context.Context, client *ipamclient.Client, _ []string, tailnet string) (*outputEnvelope, error) {
			res, err := client.ListSubnetPools()(ctx, &ipam.ListSubnetPoolsPayload{Tailnet: tailnet})
			if err != nil {
				return nil, err
			}
			entries, ok := res.([]*ipam.SubnetPoolReservation)
			if !ok {
				return nil, fmt.Errorf("unexpected list_subnet_pools result type %T", res)
			}
			outputEntries := make([]subnetPoolOutput, len(entries))
			for index, entry := range entries {
				outputEntries[index] = subnetPoolOutput{
					CIDR: entry.CIDR, Site: entry.Site,
					reservationMetadataOutput: metadataOutputFrom(
						entry.Owner, entry.Description, entry.Ticket, entry.ReservedAt,
					),
				}
			}
			output := newOutput(
				commandSubnetList, statusOK,
				fmt.Sprintf("listed %d subnet-pool reservation(s)", len(entries)), tailnet,
			)
			output.SubnetPools = &outputEntries
			return output, nil
		},
	)
}

// registerCanonicalCIDRSuggestion registers, under kindCIDR, the canonical
// spelling registry.NormalizeSubnetCIDR would suggest for a non-canonical
// (but otherwise parseable) CIDR. NormalizeSubnetCIDR's own success path
// never returns this value: a non-canonical prefix is always an error whose
// text embeds the suggestion via %q ("subnet CIDR %q is not canonical; use
// %q"), so registerSweepValueWithCanonical's err == nil branch can never
// reach it -- the suggestion has to be computed independently, with the same
// TrimSpace + [netip.ParsePrefix] + Masked steps NormalizeSubnetCIDR performs
// internally. A value that fails to parse at all has no suggestion to
// register, matching NormalizeSubnetCIDR's own "not a valid prefix" branch.
func registerCanonicalCIDRSuggestion(sanitizer *sanitize.Sanitizer, value string) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return
	}
	registerSweepValue(sanitizer, kindCIDR, prefix.Masked().String())
}

type subnetLeafCall func(
	context.Context,
	*ipamclient.Client,
	[]string,
	string,
) (*outputEnvelope, error)

func newSubnetLeafCommand(
	state *commandState,
	use, short string,
	argsValidator cobra.PositionalArgs,
	cidrArgIndex, siteArgIndex int,
	metadataFlags *reservationMetadataFlags,
	call subnetLeafCall,
) *cobra.Command {
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:           use,
		Short:         short,
		Args:          argsValidator,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return err
			}
			if cidrArgIndex >= 0 && cidrArgIndex < len(args) {
				registerSweepValueWithCanonical(
					outputSanitizer, kindCIDR, args[cidrArgIndex], registry.NormalizeSubnetCIDR,
				)
				registerCanonicalCIDRSuggestion(outputSanitizer, args[cidrArgIndex])
			}
			if siteArgIndex >= 0 && siteArgIndex < len(args) {
				if validateErr := registerAndValidate(
					outputSanitizer, kindSite, args[siteArgIndex], registry.NormalizeSiteName,
				); validateErr != nil {
					return validateErr
				}
			}
			if metadataFlags != nil && metadataFlags.owner != "" {
				registerSweepValue(outputSanitizer, kindOwner, metadataFlags.owner)
			}
			return loopback.RunWithLogger(
				cmd.Context(), registryDir, state.logger,
				func(ctx context.Context, client *ipamclient.Client) error {
					output, callErr := call(ctx, client, args, tailnet)
					if callErr != nil {
						return callErr
					}
					return emitOutput(cmd.OutOrStdout(), state.format, output, outputSanitizer)
				},
			)
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}
