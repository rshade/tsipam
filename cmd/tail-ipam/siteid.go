package main

import (
	"context"
	"fmt"
	"io"
	"os"

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

// resolveTailnet is the {tailnet} routing/storage key's resolution order: an
// explicit --tailnet flag (a customer-configured alias, which may or may not
// match their real Tailscale tailnet name), then TAILSCALE_TAILNET, then the
// literal "local". Local mode is not a separate code path — it is simply
// where this same resolution falls through when nothing else is configured.
func resolveTailnet(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("TAILSCALE_TAILNET"); env != "" {
		return env
	}
	return localTailnet
}

// newSiteIDCommand groups the 4via6 site-ID registry commands. Each
// invocation starts an ephemeral goa HTTP server bound to a fresh Unix
// socket, calls it through the generated HTTP client, and shuts it down —
// see internal/loopback. Nothing here talks to the Tailscale API; that
// live-state cross-check is separately scoped future work.
func newSiteIDCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site-id",
		Short: "Manage the 4via6 site-ID registry.",
	}
	cmd.AddCommand(newSiteIDReserveCommand())
	cmd.AddCommand(newSiteIDReleaseCommand())
	cmd.AddCommand(newSiteIDListCommand())
	return cmd
}

func addRegistryFlags(cmd *cobra.Command, registryDir, tailnet *string) {
	cmd.Flags().StringVar(registryDir, "registry-dir", defaultRegistryDir,
		"directory holding <tailnet>.ipam.hujson registry files")
	cmd.Flags().StringVar(tailnet, "tailnet", "",
		"routing/storage key for the registry (default: $TAILSCALE_TAILNET, or \"local\")")
}

func newSiteIDReserveCommand() *cobra.Command {
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:           "reserve <id> <site-name>",
		Short:         "Reserve a 4via6 site ID for a site.",
		Args:          cobra.ExactArgs(siteIDReserveArgs),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			tailnet := resolveTailnet(tailnetFlag)
			return loopback.Run(cmd.Context(), registryDir, func(ctx context.Context, client *ipamclient.Client) error {
				res, err := client.ReserveSiteID()(ctx, &ipam.ReserveSiteIDPayload{
					Tailnet: tailnet, ID: args[0], Site: args[1],
				})
				if err != nil {
					return err
				}
				result, ok := res.(*ipam.ReserveSiteIDResult)
				if !ok {
					return fmt.Errorf("unexpected reserve_site_id result type %T", res)
				}
				return printReserveResult(out, result)
			})
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}

func printReserveResult(out io.Writer, res *ipam.ReserveSiteIDResult) error {
	var err error
	if res.Changed {
		_, err = fmt.Fprintf(out, "reserved site ID %s for %q\n", res.ID, res.Site)
	} else {
		_, err = fmt.Fprintf(out, "site ID %s already reserved for %q\n", res.ID, res.Site)
	}
	return err
}

func newSiteIDReleaseCommand() *cobra.Command {
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:           "release <id>",
		Short:         "Release a reserved 4via6 site ID.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			tailnet := resolveTailnet(tailnetFlag)
			return loopback.Run(cmd.Context(), registryDir, func(ctx context.Context, client *ipamclient.Client) error {
				res, err := client.ReleaseSiteID()(ctx, &ipam.ReleaseSiteIDPayload{Tailnet: tailnet, ID: args[0]})
				if err != nil {
					return err
				}
				result, ok := res.(*ipam.ReleaseSiteIDResult)
				if !ok {
					return fmt.Errorf("unexpected release_site_id result type %T", res)
				}
				if result.Changed {
					_, err = fmt.Fprintf(out, "released site ID %s\n", result.ID)
				} else {
					_, err = fmt.Fprintf(out, "site ID %s was not reserved\n", result.ID)
				}
				return err
			})
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}

func newSiteIDListCommand() *cobra.Command {
	var registryDir, tailnetFlag string
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List reserved 4via6 site IDs.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			tailnet := resolveTailnet(tailnetFlag)
			return loopback.Run(cmd.Context(), registryDir, func(ctx context.Context, client *ipamclient.Client) error {
				res, err := client.ListSiteIds()(ctx, &ipam.ListSiteIdsPayload{Tailnet: tailnet})
				if err != nil {
					return err
				}
				entries, ok := res.([]*ipam.SiteIDReservation)
				if !ok {
					return fmt.Errorf("unexpected list_site_ids result type %T", res)
				}
				for _, entry := range entries {
					if _, writeErr := fmt.Fprintf(out, "%s\t%s\n", entry.ID, entry.Site); writeErr != nil {
						return writeErr
					}
				}
				return nil
			})
		},
	}
	addRegistryFlags(cmd, &registryDir, &tailnetFlag)
	return cmd
}
