// Package planner compares a validated registry snapshot with live allocation
// evidence. It performs no I/O and never infers physical-site ownership.
package planner

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"

	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/registry"
)

const (
	KindSiteID         = "site_id"
	KindSubnetPool     = "subnet_pool"
	KindServiceAddress = "service_address"
	KindUDPPort        = "udp_port"
	Matched            = "matched"
	ReservedOnly       = "reserved_but_unadvertised"
	LiveOnly           = "unreserved_but_live"
	Conflict           = "conflict"
)

// Entry describes one declared or undeclared allocation, with live evidence.
type Entry struct {
	Kind     string
	State    string
	Key      string
	Expected string
	Observed []string
	Routes   []string
}

// Report contains the deterministic comparison result and explicit exclusions.
type Report struct {
	Entries    []Entry
	DriftCount int
	Excluded   []string
}

// Compare requires allocations loaded by registry.Ledger.LoadForPlanning.
// Malformed live data is an error, never an absent allocation or a partial
// clean result.
func Compare(reg registry.Allocations, live devices.Snapshot) (Report, error) {
	routes, sites, err := routeInventory(live.Devices)
	if err != nil {
		return Report{}, err
	}
	services, err := serviceInventory(live.Services)
	if err != nil {
		return Report{}, err
	}
	entries := compareSiteIDs(reg.SiteIDs, sites)
	entries = append(entries, compareSubnetPools(reg.SubnetPools, routes)...)
	entries = append(entries, compareServiceAddresses(reg.ServiceAddresses, services)...)
	result := Report{Entries: entries, Excluded: []string{KindUDPPort}}
	for index := range result.Entries {
		entry := &result.Entries[index]
		entry.Observed = sortedUnique(entry.Observed)
		entry.Routes = sortedUnique(entry.Routes)
		if entry.State != Matched {
			result.DriftCount++
		}
	}
	return result, nil
}

func sortedUnique(values []string) []string {
	result := append([]string{}, values...)
	slices.Sort(result)
	return slices.Compact(result)
}

func sortEntries(entries []Entry) {
	slices.SortFunc(entries, func(left, right Entry) int {
		if left.Kind == KindSiteID {
			leftID, _ := strconv.Atoi(left.Key)
			rightID, _ := strconv.Atoi(right.Key)
			return cmp.Compare(leftID, rightID)
		}
		if left.Kind == KindSubnetPool {
			leftPrefix := netip.MustParsePrefix(left.Key)
			rightPrefix := netip.MustParsePrefix(right.Key)
			if order := leftPrefix.Addr().Compare(rightPrefix.Addr()); order != 0 {
				return order
			}
			return cmp.Compare(leftPrefix.Bits(), rightPrefix.Bits())
		}
		return netip.MustParseAddr(left.Key).Compare(netip.MustParseAddr(right.Key))
	})
}
