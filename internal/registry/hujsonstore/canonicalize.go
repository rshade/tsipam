package hujsonstore

import (
	"fmt"
	"sort"

	"github.com/rshade/tsacl/pkg/policycore/addrspace"

	"github.com/rshade/tsipam/internal/registry"
)

func canonicalizeSiteIDs(raw map[string]registry.Entry) (map[string]registry.Entry, map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	rawIDs := sortedKeys(raw)
	siteIDs := make(map[string]registry.Entry, len(raw))
	rawKeys := make(map[string]string, len(raw))
	for _, rawID := range rawIDs {
		canonicalID, normErr := registry.NormalizeSiteID(rawID)
		if normErr != nil {
			return nil, nil, fmt.Errorf("invalid site ID key %q: %w", rawID, registry.StoredStateError(normErr))
		}
		if firstRaw, ok := rawKeys[canonicalID]; ok {
			return nil, nil, fmt.Errorf("site ID %s is defined twice, as %q and %q", canonicalID, firstRaw, rawID)
		}
		entry := raw[rawID]
		if entry.Site == "" {
			return nil, nil, fmt.Errorf("site ID %s (key %q) has an empty or null site name", canonicalID, rawID)
		}
		if metaErr := registry.ValidateStoredReservationMetadata(entry.ReservationMetadata); metaErr != nil {
			return nil, nil, fmt.Errorf(
				"site ID %s (key %q): %w", canonicalID, rawID, registry.StoredStateError(metaErr),
			)
		}
		siteIDs[canonicalID] = entry
		rawKeys[canonicalID] = rawID
	}
	return siteIDs, rawKeys, nil
}

func canonicalizeSubnetPools(raw map[string]registry.Entry) (map[string]registry.Entry, map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	rawCIDRs := sortedKeys(raw)
	pools := make(map[string]registry.Entry, len(raw))
	rawKeys := make(map[string]string, len(raw))
	canonicalCIDRs := make([]string, 0, len(raw))
	for _, rawCIDR := range rawCIDRs {
		cidr, err := registry.NormalizeSubnetCIDR(rawCIDR)
		if err != nil {
			err = registry.StoredStateError(err)
			return nil, nil, fmt.Errorf("invalid subnet pool key %q: %w", rawCIDR, err)
		}
		if firstRaw, ok := rawKeys[cidr]; ok {
			return nil, nil, fmt.Errorf("subnet CIDR %s is defined twice, as %q and %q", cidr, firstRaw, rawCIDR)
		}
		entry := raw[rawCIDR]
		if entry.Site == "" {
			return nil, nil, fmt.Errorf("subnet CIDR %s (key %q) has an empty or null site name", cidr, rawCIDR)
		}
		if metaErr := registry.ValidateStoredReservationMetadata(entry.ReservationMetadata); metaErr != nil {
			return nil, nil, fmt.Errorf(
				"subnet CIDR %s (key %q): %w",
				cidr,
				rawCIDR,
				registry.StoredStateError(metaErr),
			)
		}
		pools[cidr] = entry
		rawKeys[cidr] = rawCIDR
		canonicalCIDRs = append(canonicalCIDRs, cidr)
	}
	return pools, rawKeys, subnetPoolOverlapError(pools, canonicalCIDRs)
}

func subnetPoolOverlapError(pools map[string]registry.Entry, canonicalCIDRs []string) error {
	pairs, parseErrors := addrspace.OverlappingPairs(canonicalCIDRs)
	for index := range canonicalCIDRs {
		parseErr, ok := parseErrors[index]
		if !ok {
			continue
		}
		return fmt.Errorf("validating subnet CIDR %s: %w", canonicalCIDRs[index], parseErr)
	}
	for _, pair := range pairs {
		left, right := canonicalCIDRs[pair[0]], canonicalCIDRs[pair[1]]
		if pools[left].Site == pools[right].Site {
			continue
		}
		return fmt.Errorf(
			"subnet CIDRs %s (%q) and %s (%q) overlap",
			left, pools[left].Site, right, pools[right].Site,
		)
	}
	return nil
}

func canonicalizeServiceAddresses(
	raw map[string]registry.ServiceEntry,
) (map[string]registry.ServiceEntry, map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	rawAddresses := make([]string, 0, len(raw))
	for rawAddress := range raw {
		rawAddresses = append(rawAddresses, rawAddress)
	}
	sort.Strings(rawAddresses)
	addresses := make(map[string]registry.ServiceEntry, len(raw))
	rawKeys := make(map[string]string, len(raw))
	for _, rawAddress := range rawAddresses {
		address, err := registry.NormalizeServiceAddress(rawAddress)
		if err != nil {
			err = registry.StoredStateError(err)
			return nil, nil, fmt.Errorf("invalid service address key %q: %w", rawAddress, err)
		}
		if firstRaw, ok := rawKeys[address]; ok {
			return nil, nil, fmt.Errorf(
				"service address %s is defined twice, as %q and %q",
				address, firstRaw, rawAddress,
			)
		}
		entry := raw[rawAddress]
		service, err := registry.NormalizeServiceName(entry.Service)
		if err != nil {
			err = registry.StoredStateError(err)
			return nil, nil, fmt.Errorf("service address %s (key %q): %w", address, rawAddress, err)
		}
		entry.Service = service
		if metaErr := registry.ValidateStoredReservationMetadata(entry.ReservationMetadata); metaErr != nil {
			return nil, nil, fmt.Errorf(
				"service address %s (key %q): %w", address, rawAddress, registry.StoredStateError(metaErr),
			)
		}
		addresses[address] = entry
		rawKeys[address] = rawAddress
	}
	return addresses, rawKeys, nil
}

func sortedKeys(raw map[string]registry.Entry) []string {
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
