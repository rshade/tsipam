package policycheck

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"go4.org/netipx"
)

// These bound expansion, not address-space size: IPv6 /0 requires one prefix.
const (
	maxDepth    = 128
	maxPrefixes = 65536
)

type resolver struct {
	definitions

	cache   map[string][]netip.Prefix
	heights map[string]int
	active  map[string]bool
	total   int
}

func resolve(data []byte) (map[string][]netip.Prefix, error) {
	defs, err := parse(data)
	if err != nil {
		return nil, err
	}
	r := resolver{definitions: defs, cache: make(map[string][]netip.Prefix),
		heights: make(map[string]int), active: make(map[string]bool)}
	for index, name := range defs.order {
		if _, err = r.set(name, 0); err != nil {
			return nil, fmt.Errorf("policy ipsets definition %d: %w", index+1, err)
		}
	}
	return r.cache, nil
}

func (r *resolver) set(name string, depth int) ([]netip.Prefix, error) {
	if r.active[name] {
		return nil, errors.New("cyclic ipset reference")
	}
	if depth >= maxDepth {
		return nil, errors.New("ipset nesting exceeds 128 levels")
	}
	if cached, ok := r.cache[name]; ok {
		if depth+r.heights[name] > maxDepth {
			return nil, errors.New("ipset nesting exceeds 128 levels")
		}
		return cached, nil
	}
	members, ok := r.sets[name]
	if !ok {
		return nil, errors.New("missing ipset reference")
	}
	r.active[name] = true
	r.heights[name] = 1
	defer delete(r.active, name)
	operations, err := parseOperations(members)
	if err != nil {
		return nil, err
	}
	effective := make(map[netip.Prefix]struct{})
	for index, operation := range operations {
		target, targetErr := r.target(operation.target, depth)
		if targetErr != nil {
			return nil, fmt.Errorf("member %d: %w", index+1, targetErr)
		}
		if strings.HasPrefix(operation.target, "ipset:") {
			r.heights[name] = max(r.heights[name], r.heights[operation.target]+1)
		}
		if err = applyOperation(effective, target, operation.remove); err != nil {
			return nil, err
		}
	}
	prefixes := uniquePrefixes(slices.Collect(maps.Keys(effective)))
	r.total += len(prefixes)
	if r.total > maxPrefixes {
		return nil, errors.New("policy expansion exceeds 65536 prefixes")
	}
	r.cache[name] = prefixes
	return prefixes, nil
}

type operation struct {
	target string
	remove bool
}

func parseOperations(members []string) ([]operation, error) {
	result := make([]operation, len(members))
	hasRemoval, hasImplicit := false, false
	for index, member := range members {
		parts := strings.Fields(member)
		switch {
		case len(parts) == 1:
			hasImplicit = true
			result[index].target = parts[0]
		case len(parts) == 2 && (parts[0] == "add" || parts[0] == "remove"):
			result[index] = operation{target: parts[1], remove: parts[0] == "remove"}
			hasRemoval = hasRemoval || result[index].remove
		default:
			return nil, fmt.Errorf("member %d has an invalid ipset operation", index+1)
		}
	}
	if hasRemoval && hasImplicit {
		return nil, errors.New("all members need explicit add/remove operations when removals occur")
	}
	return result, nil
}

func (r *resolver) target(target string, depth int) ([]netip.Prefix, error) {
	if strings.HasPrefix(target, "ipset:") {
		return r.set(target, depth+1)
	}
	if name, isHost := strings.CutPrefix(target, "host:"); isHost {
		value, ok := r.hosts[name]
		if !ok {
			return nil, errors.New("missing host reference")
		}
		prefix, err := literalPrefix(value)
		if err != nil {
			return nil, errors.New("referenced host must contain a literal address or CIDR")
		}
		return []netip.Prefix{prefix}, nil
	}
	if prefix, err := literalPrefix(target); err == nil {
		return []netip.Prefix{prefix}, nil
	}
	if ipRange, err := netipx.ParseIPRange(target); err == nil &&
		ipRange.From().Zone() == "" && ipRange.To().Zone() == "" {
		return ipRange.Prefixes(), nil
	}
	return nil, errors.New(
		"unsupported or invalid static ipset target (autogroups and dynamic selectors are unsupported)",
	)
}

func literalPrefix(value string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked(), nil
	}
	if addr, err := netip.ParseAddr(value); err == nil && addr.Zone() == "" {
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	return netip.Prefix{}, errors.New("not a literal address or CIDR")
}

func uniquePrefixes(prefixes []netip.Prefix) []netip.Prefix {
	slices.SortFunc(prefixes, comparePrefixes)
	return slices.Compact(prefixes)
}

func comparePrefixes(a, b netip.Prefix) int {
	if order := a.Addr().Compare(b.Addr()); order != 0 {
		return order
	}
	return cmp.Compare(a.Bits(), b.Bits())
}

func sortedNames[T any](values map[string]T) []string {
	return slices.Sorted(maps.Keys(values))
}
