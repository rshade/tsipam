package policycheck

import (
	"errors"
	"maps"
	"net/netip"
	"slices"

	"go4.org/netipx"

	"github.com/rshade/tailscale-utils/policycore/ipcalc"
)

const ipv4MappingBits = 96

// subtract preserves each addition's boundaries. Merging adjacent additions
// would invent broader authored CIDRs and change single-pool containment.
func subtract(prefixes, removals []netip.Prefix) ([]netip.Prefix, error) {
	var removalBuilder netipx.IPSetBuilder
	for _, prefix := range removals {
		removalBuilder.AddPrefix(prefix)
		unmapped, mapped := ipcalc.Unmap4In6(prefix)
		if mapped {
			removalBuilder.AddPrefix(unmapped)
		} else if prefix.Addr().Is4() {
			// Include the IPv6 representation when subtracting from IPv6 sets.
			addr := netip.AddrFrom16(prefix.Addr().As16())
			removalBuilder.AddPrefix(netip.PrefixFrom(addr, prefix.Bits()+ipv4MappingBits))
		}
	}
	remove, err := removalBuilder.IPSet()
	if err != nil {
		return nil, errors.New("could not construct ipset removal")
	}
	result := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		var builder netipx.IPSetBuilder
		builder.AddPrefix(prefix)
		builder.RemoveSet(remove)
		set, setErr := builder.IPSet()
		if setErr != nil {
			return nil, errors.New("could not subtract ipset removal")
		}
		result = append(result, set.Prefixes()...)
		if len(result) > maxPrefixes {
			return nil, errors.New("ipset subtraction exceeds 65536 prefixes")
		}
	}
	return result, nil
}

func applyOperation(effective map[netip.Prefix]struct{}, target []netip.Prefix, remove bool) error {
	if remove {
		remainder, err := subtract(slices.Collect(maps.Keys(effective)), target)
		if err != nil {
			return err
		}
		clear(effective)
		target = remainder
	}
	for _, prefix := range target {
		effective[prefix] = struct{}{}
	}
	if len(effective) > maxPrefixes {
		return errors.New("ipset expansion exceeds 65536 prefixes")
	}
	return nil
}
