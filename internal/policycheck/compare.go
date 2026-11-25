package policycheck

import (
	"fmt"
	"net/netip"
	"slices"

	"github.com/rshade/tsacl/pkg/ipcalc"
	"github.com/rshade/tsacl/pkg/policycore/addrspace"

	"github.com/rshade/tsipam/internal/registry"
)

// Comparison states are the values the API allows in its state attributes.
const (
	Matched                 = "matched"
	AuthoredButUnreserved   = "authored_but_unreserved"
	ReservedButUnreferenced = "reserved_but_unreferenced"
)

// Authored is one effective CIDR in a named ipset.
type Authored struct {
	IPSet string   `json:"ipset"`
	CIDR  string   `json:"cidr"`
	State string   `json:"state"`
	Pools []string `json:"pools"`
}

// Reserved is one subnet pool and the sets referencing any of its addresses.
type Reserved struct {
	CIDR   string   `json:"cidr"`
	Site   string   `json:"site"`
	State  string   `json:"state"`
	IPSets []string `json:"ipsets"`
}

// Report contains both directions, including matches, in deterministic order.
type Report struct {
	DriftCount int        `json:"drift_count"`
	Authored   []Authored `json:"authored"`
	Reserved   []Reserved `json:"reserved"`
}

// Compare resolves all ipset definitions and classifies them against validated
// reservations. An authored CIDR requires one containing pool; a pool reference
// requires any overlap. Inputs are not modified.
func Compare(policy []byte, pools []registry.SubnetPoolEntry) (Report, error) {
	sets, err := resolve(policy)
	if err != nil {
		return Report{}, err
	}
	pools = slices.Clone(pools)
	prefixes := make(map[string]netip.Prefix, len(pools))
	for index, pool := range pools {
		prefix, parseErr := netip.ParsePrefix(pool.CIDR)
		if parseErr != nil {
			return Report{}, fmt.Errorf("registry subnet pool %d has an invalid CIDR", index+1)
		}
		prefixes[pool.CIDR] = prefix.Masked()
	}
	slices.SortFunc(pools, func(a, b registry.SubnetPoolEntry) int {
		return comparePrefixes(prefixes[a.CIDR], prefixes[b.CIDR])
	})
	report := Report{Authored: make([]Authored, 0), Reserved: make([]Reserved, len(pools))}
	for index, pool := range pools {
		report.Reserved[index] = Reserved{
			CIDR: pool.CIDR, Site: pool.Site, State: ReservedButUnreferenced, IPSets: make([]string, 0),
		}
	}
	for _, name := range sortedNames(sets) {
		for _, prefix := range sets[name] {
			entry, matchErr := matchAuthored(name, prefix, prefixes, report.Reserved)
			if matchErr != nil {
				return Report{}, matchErr
			}
			if entry.State != Matched {
				report.DriftCount++
			}
			report.Authored = append(report.Authored, entry)
		}
	}
	for index := range report.Reserved {
		entry := &report.Reserved[index]
		entry.IPSets = slices.Compact(entry.IPSets) // Names were visited in sorted order.
		if entry.State != Matched {
			report.DriftCount++
		}
	}
	return report, nil
}

// matchAuthored collects containing pools and records references in reserved.
func matchAuthored(
	name string, prefix netip.Prefix, prefixes map[string]netip.Prefix, reserved []Reserved,
) (Authored, error) {
	entry := Authored{IPSet: name, CIDR: prefix.String(), State: AuthoredButUnreserved, Pools: make([]string, 0)}
	for index := range reserved {
		pool := &reserved[index]
		// Both values were parsed above; Overlaps also handles mapped IPv4.
		overlap, err := addrspace.Overlaps(entry.CIDR, pool.CIDR)
		if err != nil {
			return Authored{}, err
		}
		if overlap {
			pool.IPSets = append(pool.IPSets, name)
			pool.State = Matched
		}
		if contains(prefixes[pool.CIDR], prefix) {
			entry.Pools = append(entry.Pools, pool.CIDR)
			entry.State = Matched
		}
	}
	slices.Sort(entry.Pools)
	return entry, nil
}

func contains(pool, authored netip.Prefix) bool {
	if pool.Bits() <= authored.Bits() && pool.Contains(authored.Addr()) {
		return true
	}
	if unmapped, ok := ipcalc.Unmap4In6(pool); ok {
		pool = unmapped
	}
	if unmapped, ok := ipcalc.Unmap4In6(authored); ok {
		authored = unmapped
	}
	return pool.Bits() <= authored.Bits() && pool.Contains(authored.Addr())
}
