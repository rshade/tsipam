package devices

import (
	"context"
	"errors"
	"fmt"

	tsapi "tailscale.com/client/tailscale/v2"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
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
	deviceCtx, cancelDevices := context.WithTimeout(ctx, APITimeout)
	list, err := client.Devices().List(deviceCtx, tsapi.WithFields(tsapi.IncludeFieldsAll))
	cancelDevices()
	if err != nil {
		return Snapshot{}, snapshotCollectionError("devices", err)
	}
	if list == nil {
		return Snapshot{}, errors.New("collecting live devices: response omitted devices list")
	}
	serviceCtx, cancelServices := context.WithTimeout(ctx, APITimeout)
	services, err := client.Services().List(serviceCtx)
	cancelServices()
	if err != nil {
		return Snapshot{}, snapshotCollectionError("services", err)
	}
	if services == nil {
		return Snapshot{}, errors.New("collecting live services: response omitted services list")
	}
	result := Snapshot{Devices: make([]Device, len(list)), Services: make([]Service, len(services))}
	for index, device := range list {
		result.Devices[index] = Device{
			Name: device.Name, Addresses: nonNilStrings(device.Addresses),
			AdvertisedRoutes: nonNilStrings(device.AdvertisedRoutes),
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
func snapshotCollectionError(source string, err error) error {
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
