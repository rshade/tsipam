package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/registry"
)

const (
	defaultRegistryDir = "."
	localTailnet       = "local"
)

// siteIDReserveMessage and siteIDReleaseMessage render their command's
// outcome message from o's own ID/Site fields, at emit time -- see
// outputEnvelope.messageBuilder's doc comment. Reading o.Site rather than
// closing over the raw API result means a builder invoked after
// sanitizeOutput sees the already-redacted placeholder.
func siteIDReserveMessage(o *outputEnvelope) string {
	if o.Changed != nil && *o.Changed {
		return fmt.Sprintf("reserved site ID %s for %q", o.ID, o.Site)
	}
	return fmt.Sprintf("site ID %s already reserved for %q", o.ID, o.Site)
}

func siteIDReleaseMessage(o *outputEnvelope) string {
	if o.Changed != nil && *o.Changed {
		return fmt.Sprintf("released site ID %s", o.ID)
	}
	return fmt.Sprintf("site ID %s was not reserved", o.ID)
}

// resolveTailnetWithEnv implements the {tailnet} routing/storage key's
// resolution order: an explicit --tailnet flag, then TAILSCALE_TAILNET, then
// the literal "local". Local mode is not a separate code path.
func resolveTailnetWithEnv(flagValue string, getenv func(string) string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := getenv("TAILSCALE_TAILNET"); env != "" {
		return env
	}
	return localTailnet
}

// newSiteIDCommand groups the 4via6 site-ID registry commands. Each
// invocation starts an ephemeral goa HTTP server bound to a fresh Unix
// socket, calls it through the generated HTTP client, and shuts it down --
// see internal/loopback. Nothing here talks to the Tailscale API; that
// live-state cross-check is separately scoped future work.
func newSiteIDCommand(state *commandState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandSiteID,
		Short: "Manage the 4via6 site-ID registry.",
	}
	cmd.AddCommand(newSiteIDReserveCommand(state))
	cmd.AddCommand(newSiteIDReleaseCommand(state))
	cmd.AddCommand(newSiteIDListCommand(state))
	return cmd
}

func addRegistryFlags(cmd *cobra.Command, registryDir, tailnet *string) {
	cmd.Flags().StringVar(registryDir, "registry-dir", defaultRegistryDir,
		"directory holding <tailnet>.ipam.hujson registry files")
	cmd.Flags().StringVar(tailnet, "tailnet", "",
		"routing/storage key for the registry (default: $TAILSCALE_TAILNET, or \"local\")")
}

// newSiteIDReserveCommand structurally mirrors newSubnetReserveCommand
// (subnet.go) by design: each reserves its own registry entry shape through
// the shared leaf-command plumbing.
//
//nolint:dupl // mirrors newSubnetReserveCommand (subnet.go) by design; unifying needs a generic beyond this file's scope
func newSiteIDReserveCommand(state *commandState) *cobra.Command {
	var metadataFlags reservationMetadataFlags
	cmd := newSiteIDLeafCommand(
		state, "reserve <id> <site-name>", "Reserve a 4via6 site ID for a site.",
		[]inputSpec{idInput(), siteInput()}, 0, 1, &metadataFlags,
		func(ctx context.Context, client *ipamclient.Client, args []string, tailnet string) (*outputEnvelope, error) {
			owner, description, ticket, reservedAt := metadataFlags.asPointers()
			res, err := client.ReserveSiteID()(ctx, &ipam.ReserveSiteIDPayload{
				Tailnet: tailnet, ID: args[0], Site: args[1],
				Owner: owner, Description: description, Ticket: ticket, ReservedAt: reservedAt,
			})
			if err != nil {
				return nil, err
			}
			result, ok := res.(*ipam.ReserveSiteIDResult)
			if !ok {
				return nil, fmt.Errorf("unexpected reserve_site_id result type %T", res)
			}
			status := changedStatus(result.Changed)
			output := newOutput(commandSiteIDReserve, status, "", tailnet)
			output.Changed = &result.Changed
			output.ID = result.ID
			output.Site = result.Site
			output.reservationMetadataOutput = metadataOutputFrom(
				result.Owner, result.Description, result.Ticket, result.ReservedAt,
			)
			output.messageBuilder = siteIDReserveMessage
			return output, nil
		},
	)
	addMetadataFlags(cmd, &metadataFlags)
	return cmd
}

func newSiteIDReleaseCommand(state *commandState) *cobra.Command {
	return newSiteIDLeafCommand(
		state, "release <id>", "Release a reserved 4via6 site ID.", []inputSpec{idInput()}, 0, -1, nil,
		func(ctx context.Context, client *ipamclient.Client, args []string, tailnet string) (*outputEnvelope, error) {
			res, err := client.ReleaseSiteID()(
				ctx, &ipam.ReleaseSiteIDPayload{Tailnet: tailnet, ID: args[0]},
			)
			if err != nil {
				return nil, err
			}
			result, ok := res.(*ipam.ReleaseSiteIDResult)
			if !ok {
				return nil, fmt.Errorf("unexpected release_site_id result type %T", res)
			}
			status := changedStatus(result.Changed)
			output := newOutput(commandSiteIDRelease, status, "", tailnet)
			output.Changed = &result.Changed
			output.ID = result.ID
			output.messageBuilder = siteIDReleaseMessage
			return output, nil
		},
	)
}

// newSiteIDListCommand structurally mirrors newSubnetListCommand (subnet.go)
// and newServiceListCommand (service.go) by design: each lists its own
// registry entry shape through the shared leaf-command plumbing, and
// unifying them would need a generic beyond this file's scope.
//
//nolint:dupl // mirrors newSubnetListCommand (subnet.go) and newServiceListCommand (service.go) by design; unifying needs a generic beyond this file's scope
func newSiteIDListCommand(state *commandState) *cobra.Command {
	return newSiteIDLeafCommand(
		state, commandList, "List reserved 4via6 site IDs.", nil, -1, -1, nil,
		func(ctx context.Context, client *ipamclient.Client, _ []string, tailnet string) (*outputEnvelope, error) {
			res, err := client.ListSiteIds()(ctx, &ipam.ListSiteIdsPayload{Tailnet: tailnet})
			if err != nil {
				return nil, err
			}
			entries, ok := res.([]*ipam.SiteIDReservation)
			if !ok {
				return nil, fmt.Errorf("unexpected list_site_ids result type %T", res)
			}
			outputEntries := make([]siteIDOutput, len(entries))
			for index, entry := range entries {
				outputEntries[index] = siteIDOutput{
					ID: entry.ID, Site: entry.Site,
					reservationMetadataOutput: metadataOutputFrom(
						entry.Owner, entry.Description, entry.Ticket, entry.ReservedAt,
					),
				}
			}
			output := newOutput(
				commandSiteIDList, statusOK,
				fmt.Sprintf("listed %d site-ID reservation(s)", len(entries)), tailnet,
			)
			output.SiteIDs = &outputEntries
			return output, nil
		},
	)
}

type siteIDLeafCall func(
	context.Context,
	*ipamclient.Client,
	[]string,
	string,
) (*outputEnvelope, error)

// newSiteIDLeafCommand is the site-id analogue of subnet.go's
// newSubnetLeafCommand and service.go's newServiceLeafCommand: one shared
// RunE registers the ID/site-name positional args for the error sanitizer's
// free-text sweep before invoking call, so reserve, release, and list -- any
// site-id leaf that takes an ID or site-name argument -- agree on what gets
// swept instead of each command registering (or forgetting to register) its
// own args independently.
//
// It mirrors newServiceLeafCommand (service.go) by design: site-id has no
// third per-leaf canonicalization step (unlike newSubnetLeafCommand's
// CIDR-suggestion call), so its RunE body follows the same two-arg-index
// sweep-then-call shape as service's, aside from each function's own
// free-form-label UTF-8 pre-check landing at a different position in that
// shape. Unifying the three needs a generic beyond this file's scope.
func newSiteIDLeafCommand(
	state *commandState,
	use, short string,
	specs []inputSpec,
	idArgIndex, siteArgIndex int,
	metadataFlags *reservationMetadataFlags,
	call siteIDLeafCall,
) *cobra.Command {
	var registryDir, tailnetFlag string
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
			// The site-name and ID args must be registered before call can
			// fail: NormalizeSiteName/NormalizeSiteID validation errors echo
			// the trimmed spelling (and, for a control character or a
			// non-numeric ID, its %q-escaped rendering -- see
			// registerQuotedForm) before the loopback client is ever
			// invoked.
			if siteArgIndex >= 0 && siteArgIndex < len(args) {
				if validateErr := registerAndValidate(
					outputSanitizer, kindSite, args[siteArgIndex], registry.NormalizeSiteName,
				); validateErr != nil {
					return validateErr
				}
			}
			if idArgIndex >= 0 && idArgIndex < len(args) {
				registerSweepValueWithCanonical(
					outputSanitizer, kindSiteID, args[idArgIndex], registry.NormalizeSiteID,
				)
			}
			if metadataFlags != nil && metadataFlags.owner != "" {
				registerSweepValue(outputSanitizer, kindOwner, metadataFlags.owner)
			}
			return state.registryTarget(registryDir).run(
				cmd.Context(),
				func(ctx context.Context, client *ipamclient.Client) error {
					output, callErr := call(ctx, client, args, tailnet)
					if callErr != nil {
						return callErr
					}
					return emitOutput(cmd, output, outputSanitizer)
				},
			)
		},
	}
	bound = bindInputs(cmd, specs...)
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}
