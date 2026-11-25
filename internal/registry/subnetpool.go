package registry

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/rshade/tsacl/pkg/ipcalc"
	"github.com/rshade/tsacl/pkg/policycore/addrspace"
)

// SubnetPoolEntry is one resolved subnet-pool reservation, returned by
// ListSubnetPools.
type SubnetPoolEntry struct {
	ReservationMetadata

	CIDR string
	Site string
}

// SubnetPoolCollisionError reports that a requested CIDR is already reserved
// for a different site or overlaps a pool reserved for a different site.
type SubnetPoolCollisionError struct {
	CIDR         string
	ExistingCIDR string
	ExistingSite string
	WantedSite   string
}

func (e *SubnetPoolCollisionError) Error() string {
	if e.CIDR == e.ExistingCIDR {
		return fmt.Sprintf(
			"subnet CIDR %s is already reserved for %q, not %q",
			e.CIDR, e.ExistingSite, e.WantedSite,
		)
	}
	return fmt.Sprintf(
		"subnet CIDR %s overlaps %s reserved for %q, not %q",
		e.CIDR, e.ExistingCIDR, e.ExistingSite, e.WantedSite,
	)
}

// SubnetPoolExhaustedError reports that no child of Size bits is free inside
// Pool once registry reservations and live routes are excluded. The text
// matches calc next-free's exhaustion message so operators and scripts see
// one wording for one condition.
type SubnetPoolExhaustedError struct {
	Pool string
	Size int
}

func (e *SubnetPoolExhaustedError) Error() string {
	return fmt.Sprintf("no free /%d block in %s", e.Size, e.Pool)
}

// NormalizeSubnetCIDR validates cidr as a canonical IPv4 or IPv6 prefix.
// Unlike site IDs, CIDRs are not silently masked or text-normalized: an
// operator must reserve the network they wrote, so host bits, expanded IPv6,
// and other non-canonical spellings are rejected with the canonical form in
// the error. Validation errors are wrapped with invalidInput so they are
// classified as HTTP 400 caller-input failures on caller-supplied input,
// and unwrapped by storedStateError on stored-state validation.
func NormalizeSubnetCIDR(cidr string) (string, error) {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return "", invalidInput(errors.New("subnet CIDR must not be empty"))
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", invalidInput(fmt.Errorf("subnet CIDR %q is not a valid prefix: %w", cidr, err))
	}
	canonical := prefix.Masked().String()
	if cidr != canonical {
		return "", invalidInput(fmt.Errorf("subnet CIDR %q is not canonical; use %q", cidr, canonical))
	}
	return canonical, nil
}

// findTicketReplay implements allocate's idempotency key: a non-empty ticket
// that already names a reservation for the same site inside pool means the
// caller is re-running an allocation that succeeded, so that reservation is
// returned as-is. Unlike reserve's repeat path, nothing is merged or
// re-stamped -- a CI retry with a tweaked description must not churn the
// file. The scan runs in subnet list's order so the lowest matching block
// wins deterministically when several share the ticket.
func findTicketReplay(pools map[string]Entry, pool netip.Prefix, site, ticket string) (SubnetPoolEntry, bool) {
	if ticket == "" {
		return SubnetPoolEntry{}, false
	}
	for _, entry := range sortedSubnetPoolEntries(pools) {
		if entry.Site != site || entry.Ticket != ticket {
			continue
		}
		prefix, err := netip.ParsePrefix(entry.CIDR)
		if err != nil {
			continue
		}
		if unmapped, ok := ipcalc.Unmap4In6(prefix); ok && pool.Addr().Is4() {
			prefix = unmapped
		}
		if prefix.Addr().BitLen() != pool.Addr().BitLen() {
			continue
		}
		if prefix.Bits() >= pool.Bits() && pool.Contains(prefix.Addr()) {
			return entry, true
		}
	}
	return SubnetPoolEntry{}, false
}

// NormalizeAllocationPool parses pool the way calc next-free does -- 4-in-6
// unmapped, then masked -- and validates size against it. It is exported so
// the CLI can fail fast on the same rule, with the same wording, before it
// reads devices; AllocateSubnetPoolWithLogger applies it again because the
// service method is a contract of its own. Errors are invalidInput.
func NormalizeAllocationPool(pool string, size int) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(pool))
	if err != nil {
		return netip.Prefix{}, invalidInput(fmt.Errorf("pool CIDR %q is not a valid prefix: %w", pool, err))
	}
	if normalized, ok := ipcalc.Unmap4In6(prefix); ok {
		prefix = normalized
	}
	prefix = prefix.Masked()
	if size < prefix.Bits() || size > prefix.Addr().BitLen() {
		return netip.Prefix{}, invalidInput(fmt.Errorf(
			"target prefix length /%d must be between pool /%d and address length /%d",
			size, prefix.Bits(), prefix.Addr().BitLen()))
	}
	return prefix, nil
}

// AppendTaken adds prefix to a taken set for [ipcalc.NextFree], which compares
// within one address family. An IPv4-mapped prefix is the same space in both, so
// it is added in both.
func AppendTaken(taken []netip.Prefix, prefix netip.Prefix) []netip.Prefix {
	if unmapped, ok := ipcalc.Unmap4In6(prefix); ok {
		taken = append(taken, unmapped)
	}
	return append(taken, prefix)
}

// parseLiveTaken fails closed on a live route that does not parse: a taken
// set missing one advertised route could hand out space a router already
// serves. The CLI parses routes before sending them (nextFreeTaken), so
// from that caller this is a contract check; it exists for any caller that
// hands the service raw strings.
func parseLiveTaken(liveTaken []string) ([]netip.Prefix, error) {
	taken := make([]netip.Prefix, 0, len(liveTaken))
	for _, route := range liveTaken {
		prefix, err := netip.ParsePrefix(route)
		if err != nil {
			return nil, invalidInput(fmt.Errorf("live route %q is not a valid prefix: %w", route, err))
		}
		taken = AppendTaken(taken, prefix)
	}
	return taken, nil
}

// checkSubnetPoolConflict refuses cidr against reg's existing subnet pools:
// an exact-CIDR match owned by a different site is a *SubnetPoolCollisionError
// regardless of metadata, and a genuinely new CIDR is checked for an overlap
// against every other pool (see validateSubnetPoolOverlap). An existing entry
// for cidr already establishes ownership, so it is not re-checked for overlap
// against itself or anything else.
func checkSubnetPoolConflict(existingEntry Entry, exists bool, pools map[string]Entry, cidr, site string) error {
	if exists {
		if existingEntry.Site != site {
			return &SubnetPoolCollisionError{
				CIDR: cidr, ExistingCIDR: cidr, ExistingSite: existingEntry.Site, WantedSite: site,
			}
		}
		return nil
	}
	return validateSubnetPoolOverlap(pools, cidr, site)
}

// validateSubnetPoolOverlap checks cidr against every existing pool for an
// overlap owned by a different site. Iteration order doesn't affect which
// pools exist, only which colliding entry a multi-overlap error names first
// -- not worth sortedSubnetPoolEntries's ordering cost (used where display
// order matters, e.g. ListSubnetPoolsWithLogger) on this membership scan.
func validateSubnetPoolOverlap(
	pools map[string]Entry,
	cidr, site string,
) error {
	for existingCIDR, existingEntry := range pools {
		overlaps, err := addrspace.Overlaps(cidr, existingCIDR)
		if err != nil {
			return fmt.Errorf("comparing subnet CIDRs %s and %s: %w", cidr, existingCIDR, err)
		}
		if overlaps && existingEntry.Site != site {
			return &SubnetPoolCollisionError{
				CIDR: cidr, ExistingCIDR: existingCIDR, ExistingSite: existingEntry.Site, WantedSite: site,
			}
		}
	}
	return nil
}

func sortedSubnetPoolEntries(pools map[string]Entry) []SubnetPoolEntry {
	entries := make([]SubnetPoolEntry, 0, len(pools))
	for cidr, entry := range pools {
		entries = append(
			entries,
			SubnetPoolEntry{CIDR: cidr, Site: entry.Site, ReservationMetadata: entry.ReservationMetadata},
		)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, leftErr := netip.ParsePrefix(entries[i].CIDR)
		right, rightErr := netip.ParsePrefix(entries[j].CIDR)
		if leftErr == nil && rightErr == nil {
			if cmp := left.Addr().Compare(right.Addr()); cmp != 0 {
				return cmp < 0
			}
			if left.Bits() != right.Bits() {
				return left.Bits() < right.Bits()
			}
		}
		return entries[i].CIDR < entries[j].CIDR
	})
	return entries
}
