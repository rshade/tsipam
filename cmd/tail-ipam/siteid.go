package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	ipamclient "github.com/rshade/tailscale-utils/tail-ipam/gen/http/ipam/client"
	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/loopback"
)

const (
	defaultRegistryDir = "."
	siteIDReserveArgs  = 2
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

func newSiteIDReserveCommand(state *commandState) *cobra.Command {
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:           "reserve <id> <site-name>",
		Short:         "Reserve a 4via6 site ID for a site.",
		Args:          cobra.ExactArgs(siteIDReserveArgs),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return err
			}
			return loopback.RunWithLogger(
				cmd.Context(), registryDir, state.logger,
				func(ctx context.Context, client *ipamclient.Client) error {
					res, callErr := client.ReserveSiteID()(ctx, &ipam.ReserveSiteIDPayload{
						Tailnet: tailnet, ID: args[0], Site: args[1],
					})
					if callErr != nil {
						return callErr
					}
					result, ok := res.(*ipam.ReserveSiteIDResult)
					if !ok {
						return fmt.Errorf("unexpected reserve_site_id result type %T", res)
					}
					status := changedStatus(result.Changed)
					output := newOutput(commandSiteIDReserve, status, "", tailnet)
					output.Changed = &result.Changed
					output.ID = result.ID
					output.Site = result.Site
					output.messageBuilder = siteIDReserveMessage
					return emitOutput(cmd.OutOrStdout(), state.format, output, outputSanitizer)
				},
			)
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}

func newSiteIDReleaseCommand(state *commandState) *cobra.Command {
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:           "release <id>",
		Short:         "Release a reserved 4via6 site ID.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return err
			}
			return loopback.RunWithLogger(
				cmd.Context(), registryDir, state.logger,
				func(ctx context.Context, client *ipamclient.Client) error {
					res, callErr := client.ReleaseSiteID()(
						ctx, &ipam.ReleaseSiteIDPayload{Tailnet: tailnet, ID: args[0]},
					)
					if callErr != nil {
						return callErr
					}
					result, ok := res.(*ipam.ReleaseSiteIDResult)
					if !ok {
						return fmt.Errorf("unexpected release_site_id result type %T", res)
					}
					status := changedStatus(result.Changed)
					output := newOutput(commandSiteIDRelease, status, "", tailnet)
					output.Changed = &result.Changed
					output.ID = result.ID
					output.messageBuilder = siteIDReleaseMessage
					return emitOutput(cmd.OutOrStdout(), state.format, output, outputSanitizer)
				},
			)
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}

func newSiteIDListCommand(state *commandState) *cobra.Command {
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:           commandList,
		Short:         "List reserved 4via6 site IDs.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
			outputSanitizer, err := state.outputSanitizer(tailnet)
			if err != nil {
				return err
			}
			return loopback.RunWithLogger(
				cmd.Context(), registryDir, state.logger,
				func(ctx context.Context, client *ipamclient.Client) error {
					res, callErr := client.ListSiteIds()(ctx, &ipam.ListSiteIdsPayload{Tailnet: tailnet})
					if callErr != nil {
						return callErr
					}
					entries, ok := res.([]*ipam.SiteIDReservation)
					if !ok {
						return fmt.Errorf("unexpected list_site_ids result type %T", res)
					}
					outputEntries := make([]siteIDOutput, len(entries))
					for index, entry := range entries {
						outputEntries[index] = siteIDOutput{ID: entry.ID, Site: entry.Site}
					}
					output := newOutput(
						commandSiteIDList, statusOK,
						fmt.Sprintf("listed %d site-ID reservation(s)", len(entries)), tailnet,
					)
					output.SiteIDs = &outputEntries
					return emitOutput(cmd.OutOrStdout(), state.format, output, outputSanitizer)
				},
			)
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}
