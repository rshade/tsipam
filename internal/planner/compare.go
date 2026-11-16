package planner

import (
	"net/netip"

	"github.com/rshade/tsacl/pkg/ipcalc"

	"github.com/rshade/tsipam/internal/registry"
)

func compareSiteIDs(reserved map[string]registry.Entry, live map[string]evidence) []Entry {
	entries := make([]Entry, 0, len(reserved)+len(live))
	for key, reservation := range reserved {
		item, exists := live[key]
		state := ReservedOnly
		if exists {
			state = Matched
		}
		entries = append(entries, Entry{
			Kind: KindSiteID, Key: key, State: state, Expected: reservation.Site,
			Observed: item.owners, Routes: item.routes,
		})
	}
	for key, item := range live {
		if _, exists := reserved[key]; !exists {
			entries = append(entries, Entry{
				Kind: KindSiteID, Key: key, State: LiveOnly, Observed: item.owners, Routes: item.routes,
			})
		}
	}
	sortEntries(entries)
	return entries
}

func compareSubnetPools(reserved map[string]registry.Entry, live map[netip.Prefix][]string) []Entry {
	entries := make([]Entry, 0, len(reserved)+len(live))
	covered := make(map[netip.Prefix]bool)
	for key, reservation := range reserved {
		pool := netip.MustParsePrefix(key)
		if normalized, ok := ipcalc.Unmap4In6(pool); ok {
			pool = normalized
		}
		entry := Entry{Kind: KindSubnetPool, Key: key, State: ReservedOnly, Expected: reservation.Site}
		for route, owners := range live {
			if pool.Addr().BitLen() == route.Addr().BitLen() &&
				pool.Bits() <= route.Bits() && pool.Contains(route.Addr()) {
				entry.State = Matched
				entry.Observed = append(entry.Observed, owners...)
				entry.Routes = append(entry.Routes, route.String())
				covered[route] = true
			}
		}
		entries = append(entries, entry)
	}
	for route, owners := range live {
		if !covered[route] {
			entries = append(entries, Entry{
				Kind: KindSubnetPool, Key: route.String(), State: LiveOnly, Observed: owners,
				Routes: []string{route.String()},
			})
		}
	}
	sortEntries(entries)
	return entries
}

func compareServiceAddresses(reserved map[string]registry.ServiceEntry, live map[string][]string) []Entry {
	entries := make([]Entry, 0, len(reserved)+len(live))
	for key, reservation := range reserved {
		owners := sortedUnique(live[key])
		state := ReservedOnly
		if len(owners) > 0 {
			state = Conflict
			if len(owners) == 1 && owners[0] == reservation.Service {
				state = Matched
			}
		}
		entries = append(entries, Entry{
			Kind: KindServiceAddress, Key: key, State: state, Expected: reservation.Service, Observed: owners,
		})
	}
	for key, owners := range live {
		if _, exists := reserved[key]; !exists {
			entries = append(entries, Entry{Kind: KindServiceAddress, Key: key, State: LiveOnly, Observed: owners})
		}
	}
	sortEntries(entries)
	return entries
}
