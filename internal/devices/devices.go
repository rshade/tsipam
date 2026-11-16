// Package devices collects the tailnet's device list from the Tailscale API
// through the official v2 client, isolating credential handling (and its
// process-global viper coupling) from the CLI's presentation layer.
package devices

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jaxxstorm/tscli/pkg/tscli"
	"github.com/spf13/viper"
	tsapi "tailscale.com/client/tailscale/v2"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// APITimeout bounds every Tailscale API request.
const APITimeout = 30 * time.Second

// Device is a normalized device record: nil address, route, and tag lists from
// the API are represented as empty slices so callers never have to special-case
// a nil slice separately from an empty one.
type Device struct {
	Name             string
	Addresses        []string
	AdvertisedRoutes []string
	EnabledRoutes    []string
	Tags             []string
	User             string
	OS               string
	Hostname         string
	Authorized       bool
	Ephemeral        bool
}

// clientMu serializes access to viper's process-global config: tscli.New reads
// its configuration from viper rather than taking an explicit Config, and this
// repo's tests run in parallel. The mutex scopes the race to "one goroutine
// sets viper, then reads it back into a client" -- the *tailscale.Client
// returned already carries everything it read, so it needs no further
// synchronization once built.
//
//nolint:gochecknoglobals // guards access to tscli's process-global viper config, not application state
var clientMu sync.Mutex

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// List collects every device on the tailnet the configured credentials can see.
func List(ctx context.Context, getenv func(string) string, logger diagnostics.Logger) ([]Device, error) {
	diagnostics.Debug(logger, "constructing Tailscale API client")
	client, err := newClient(getenv)
	if err != nil {
		return nil, err
	}
	// tscli's client builds its own http.Client with no Timeout set, so
	// the APITimeout bound that an explicit http.Client would have is
	// reapplied here through the context.
	listCtx, cancel := context.WithTimeout(ctx, APITimeout)
	defer cancel()
	diagnostics.Debug(logger, "collecting devices")
	list, err := client.Devices().List(listCtx, tsapi.WithFields(tsapi.IncludeFieldsAll))
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	devices := make([]Device, len(list))
	for index, device := range list {
		devices[index] = Device{
			Name:             device.Name,
			Addresses:        nonNilStrings(device.Addresses),
			AdvertisedRoutes: nonNilStrings(device.AdvertisedRoutes),
			EnabledRoutes:    nonNilStrings(device.EnabledRoutes),
			Tags:             nonNilStrings(device.Tags),
			User:             device.User,
			OS:               device.OS,
			Hostname:         device.Hostname,
			Authorized:       device.Authorized,
			Ephemeral:        device.IsEphemeral,
		}
	}
	diagnostics.Debug(logger, "device collection complete", "count", len(devices))
	return devices, nil
}

// newClient builds the official v2 API client via tscli's constructor
// (github.com/jaxxstorm/tscli/pkg/tscli.New), which reads its configuration
// from viper. Populating those keys here -- rather than adopting viper as
// tsipam's own config system -- keeps the dependency narrowly scoped to
// "how we talk to the API," matching the exception recorded in CONTEXT.md.
func newClient(getenv func(string) string) (*tsapi.Client, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	tailnet := getenv("TAILSCALE_TAILNET")
	if tailnet == "" {
		return nil, errors.New("TAILSCALE_TAILNET is required")
	}
	apiKey := getenv("TAILSCALE_API_KEY")
	oauthClientID := getenv("TAILSCALE_OAUTH_CLIENT_ID")
	oauthClientSecret := getenv("TAILSCALE_OAUTH_CLIENT_SECRET")
	if apiKey == "" && (oauthClientID == "" || oauthClientSecret == "") {
		return nil, errors.New(
			"TAILSCALE_API_KEY, or both TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET, are required")
	}

	clientMu.Lock()
	defer clientMu.Unlock()

	viper.Set("tailnet", tailnet)
	viper.Set("api-key", apiKey)
	viper.Set("oauth-client-id", oauthClientID)
	viper.Set("oauth-client-secret", oauthClientSecret)
	defer func() {
		viper.Set("tailnet", "")
		viper.Set("api-key", "")
		viper.Set("oauth-client-id", "")
		viper.Set("oauth-client-secret", "")
	}()

	client, err := tscli.New()
	if err != nil {
		return nil, fmt.Errorf("constructing Tailscale API client via tscli: %w", err)
	}
	// tscli's internal userAgentTransport unconditionally overwrites User-Agent
	// headers and ignores the public UserAgent field; the User-Agent leak is a
	// known unfixable limitation pending a fix upstream.
	return client, nil
}
