package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jaxxstorm/tscli/pkg/tscli"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	tsapi "tailscale.com/client/tailscale/v2"
)

// apiTimeout bounds every Tailscale API request.
const apiTimeout = 30 * time.Second

// newTailscaleClientMu serializes access to viper's process-global config:
// tscli.New reads its configuration from viper rather than taking an explicit
// Config, and this repo's tests run in parallel. The mutex scopes the race to
// "one goroutine sets viper, then reads it back into a client" — the
// *tailscale.Client returned already carries everything it read, so it needs no
// further synchronization once built.
//
//nolint:gochecknoglobals // guards access to tscli's process-global viper config, not application state
var newTailscaleClientMu sync.Mutex

// newDevicesCommand is the first real (non-scaffold) command: a read-only
// proof that tail-ipam can reach the control plane through jaxxstorm/tscli's
// client construction rather than a hand-rolled one. It has no allocation
// logic yet — see ROADMAP.md ("Future Vision" -> tail-ipam) for that design.
func newDevicesCommand() *cobra.Command {
	devices := &cobra.Command{
		Use:   "devices",
		Short: "Inspect devices on the tailnet (read-only).",
	}
	devices.AddCommand(newDevicesListCommand())
	return devices
}

func newDevicesListCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List devices with their addresses and advertised routes.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDevicesList(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

func runDevicesList(ctx context.Context, out interface{ Write([]byte) (int, error) }) error {
	client, err := newTailscaleClient()
	if err != nil {
		return err
	}
	// tscli's client builds its own http.Client with no Timeout set, so
	// the apiTimeout bound that an explicit http.Client would have is
	// reapplied here through the context.
	listCtx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	list, err := client.Devices().List(listCtx, tsapi.WithFields(tsapi.IncludeFieldsAll))
	if err != nil {
		return fmt.Errorf("listing devices: %w", err)
	}
	for _, d := range list {
		_, writeErr := fmt.Fprintf(out, "%s\t%v\troutes=%v\n",
			d.Name, d.Addresses, d.AdvertisedRoutes)
		if writeErr != nil {
			return writeErr
		}
	}
	return nil
}

// newTailscaleClient builds the official v2 API client via tscli's
// constructor (github.com/jaxxstorm/tscli/pkg/tscli.New), which reads its
// configuration from viper. Populating those keys here — rather than adopting
// viper as tail-ipam's own config system — keeps the dependency narrowly
// scoped to "how we talk to the API," matching the exception recorded in
// CONTEXT.md.
func newTailscaleClient() (*tsapi.Client, error) {
	tailnet := os.Getenv("TAILSCALE_TAILNET")
	if tailnet == "" {
		return nil, errors.New("TAILSCALE_TAILNET is required")
	}
	apiKey := os.Getenv("TAILSCALE_API_KEY")
	oauthClientID := os.Getenv("TAILSCALE_OAUTH_CLIENT_ID")
	oauthClientSecret := os.Getenv("TAILSCALE_OAUTH_CLIENT_SECRET")
	if apiKey == "" && (oauthClientID == "" || oauthClientSecret == "") {
		return nil, errors.New(
			"TAILSCALE_API_KEY, or both TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET, are required")
	}

	newTailscaleClientMu.Lock()
	defer newTailscaleClientMu.Unlock()

	viper.Set("tailnet", tailnet)
	viper.Set("api-key", apiKey)
	viper.Set("oauth-client-id", oauthClientID)
	viper.Set("oauth-client-secret", oauthClientSecret)

	client, err := tscli.New()
	viper.Set("oauth-client-secret", "")
	viper.Set("api-key", "")
	if err != nil {
		return nil, fmt.Errorf("constructing Tailscale API client via tscli: %w", err)
	}
	// tscli's internal userAgentTransport unconditionally overwrites User-Agent
	// headers and ignores the public UserAgent field; the User-Agent leak is a
	// known unfixable limitation pending a fix upstream.
	return client, nil
}
