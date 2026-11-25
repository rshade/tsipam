package devices

import (
	"context"
	"errors"
	"fmt"

	tsapi "tailscale.com/client/tailscale/v2"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// Service contains the identity and assigned addresses needed for comparison.
type Service struct {
	Name      string
	Addresses []string
}

// Snapshot is the complete live input for registry comparisons.
type Snapshot struct {
	Devices  []Device
	Services []Service
}

// CollectSnapshot reads both allocation sources using one authenticated client.
// An error from either source discards the partial inventory.
func CollectSnapshot(
	ctx context.Context, getenv func(string) string, logger diagnostics.Logger,
) (Snapshot, error) {
	client, err := newClient(getenv)
	if err != nil {
		return Snapshot{}, err
	}
	diagnostics.Debug(logger, "collecting live allocation inventory")
	list, err := read(ctx, "list_devices", describeSnapshotError("devices"),
		func(ctx context.Context) ([]tsapi.Device, error) {
			return client.Devices().List(ctx, tsapi.WithFields(tsapi.IncludeFieldsAll))
		})
	if err != nil {
		return Snapshot{}, err
	}
	if list == nil {
		return Snapshot{}, &CollectionError{Err: errors.New("collecting live devices: response omitted devices list")}
	}
	services, err := read(ctx, "list_services", describeSnapshotError("services"),
		func(ctx context.Context) ([]tsapi.Service, error) {
			return client.Services().List(ctx)
		})
	if err != nil {
		return Snapshot{}, err
	}
	if services == nil {
		return Snapshot{}, &CollectionError{Err: errors.New("collecting live services: response omitted services list")}
	}
	result := Snapshot{Devices: make([]Device, len(list)), Services: make([]Service, len(services))}
	for index, device := range list {
		result.Devices[index] = Device{
			Name: device.Name, Addresses: nonNilStrings(device.Addresses),
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
	for index, service := range services {
		result.Services[index] = Service{Name: service.Name, Addresses: nonNilStrings(service.Addrs)}
	}
	diagnostics.Debug(logger, "live allocation collection complete", "devices", len(list), "services", len(services))
	return result, nil
}

// API bodies can contain identities unknown to the registry sanitizer. Keep
// operation/status diagnostics while never copying those bodies into errors.
func describeSnapshotError(source string) func(error) error {
	return func(err error) error {
		if apiErr, ok := errors.AsType[tsapi.APIError](err); ok {
			return fmt.Errorf("collecting live %s: HTTP %d", source, apiErr.Status)
		}
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("collecting live %s: %w", source, context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("collecting live %s: %w", source, context.DeadlineExceeded)
		}
		return fmt.Errorf("collecting live %s: request or response decoding failed", source)
	}
}
