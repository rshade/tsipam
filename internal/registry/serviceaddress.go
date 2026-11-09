package registry

import (
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/rshade/tsacl/pkg/policycore/hujsonedit"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
)

// ServiceAddressEntry is one resolved service-address reservation returned by
// ListServiceAddressesWithLogger.
type ServiceAddressEntry struct {
	ReservationMetadata

	Address string
	Service string
}

// ServiceAddressCollisionError reports an address already owned by a
// different canonical service identity.
type ServiceAddressCollisionError struct {
	Address  string
	Existing string
	Wanted   string
}

func (e *ServiceAddressCollisionError) Error() string {
	return fmt.Sprintf(
		"service address %s is already reserved for %q, not %q",
		e.Address,
		e.Existing,
		e.Wanted,
	)
}

// ReserveServiceAddressWithLogger reserves an explicit IPv4 host for a
// service, creating the registry file if necessary. Address/service aliases
// normalize before collision and idempotency decisions. Metadata follows the
// same merge and auto-stamp rules as the existing reservation primitives.
//
// It mirrors ReserveSiteIDWithLogger's locked read-decide-write transaction
// by necessity, not copy-paste: both reservation primitives share the same
// normalize/lock/collide/resolve/patch shape, and reservePatchOp already
// factors out the one piece that was genuinely identical logic.
//
//nolint:dupl // mirrors ReserveSiteIDWithLogger's normalize/lock/collide/resolve/patch shape; unifying needs a generic that costs more than it saves
func ReserveServiceAddressWithLogger(
	path, address, service string,
	metadata ReservationMetadata,
	logger diagnostics.Logger,
) (string, string, ReservationMetadata, bool, error) {
	address, err := NormalizeServiceAddress(address)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	service, err = NormalizeServiceName(service)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	metadata, err = normalizeMetadata(metadata)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}

	changed := false
	var resultMetadata ReservationMetadata
	lockErr := withRegistryLock(path, logger, func() error {
		reg, loadErr := loadOrCreate(path, serviceAddressesKey, logger)
		if loadErr != nil {
			return loadErr
		}

		existingEntry, exists := reg.ServiceAddresses[address]
		if exists && existingEntry.Service != service {
			return &ServiceAddressCollisionError{
				Address: address, Existing: existingEntry.Service, Wanted: service,
			}
		}
		newEntry, unchanged := resolveServiceEntry(existingEntry, exists, service, metadata, time.Now)
		if unchanged {
			resultMetadata = existingEntry.ReservationMetadata
			diagnostics.Debug(logger, "service-address reservation already present")
			return nil
		}

		op, opErr := reservePatchOp(reg, serviceAddressesKey, address, newEntry)
		if opErr != nil {
			return opErr
		}
		if patchErr := hujsonedit.WritePatch(path, nil, op); patchErr != nil {
			return fmt.Errorf("reserving service address %s: %w", address, patchErr)
		}
		changed = true
		resultMetadata = newEntry.ReservationMetadata
		diagnostics.Debug(logger, "service-address reservation written")
		return nil
	})
	if lockErr != nil {
		return "", "", ReservationMetadata{}, false, lockErr
	}
	return address, service, resultMetadata, changed, nil
}

// ReleaseServiceAddressWithLogger removes an address reservation. An absent
// entry in an existing registry is an idempotent no-op; a missing registry
// remains an error, matching the other release commands.
func ReleaseServiceAddressWithLogger(
	path, address string,
	logger diagnostics.Logger,
) (string, bool, error) {
	address, err := NormalizeServiceAddress(address)
	if err != nil {
		return "", false, err
	}
	changed, err := releaseRegistryEntry(
		path,
		serviceAddressesKey,
		address,
		"service address",
		"service-address",
		logger,
	)
	if err != nil {
		return "", false, err
	}
	return address, changed, nil
}

// ListServiceAddressesWithLogger returns reservations sorted by numeric IPv4
// address with a deterministic string fallback for malformed partial state.
func ListServiceAddressesWithLogger(
	path string,
	logger diagnostics.Logger,
) ([]ServiceAddressEntry, error) {
	reg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if mapErr := reg.mapError(serviceAddressesKey); mapErr != nil {
		return nil, mapErr
	}
	diagnostics.Debug(logger, "registry loaded",
		"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools),
		"service_address_count", len(reg.ServiceAddresses))
	entries := make([]ServiceAddressEntry, 0, len(reg.ServiceAddresses))
	for address, entry := range reg.ServiceAddresses {
		entries = append(entries, ServiceAddressEntry{
			Address: address, Service: entry.Service, ReservationMetadata: entry.ReservationMetadata,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, leftErr := netip.ParseAddr(entries[i].Address)
		right, rightErr := netip.ParseAddr(entries[j].Address)
		if leftErr == nil && rightErr == nil {
			return left.Compare(right) < 0
		}
		return entries[i].Address < entries[j].Address
	})
	return entries, nil
}
