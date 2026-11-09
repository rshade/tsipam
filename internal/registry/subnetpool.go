package registry

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/rshade/tsacl/pkg/ipcalc"
	"github.com/rshade/tsacl/pkg/policycore/addrspace"
	"github.com/rshade/tsacl/pkg/policycore/hujsonedit"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
)

// SubnetPoolEntry is one resolved subnet-pool reservation, returned by
// ListSubnetPoolsWithLogger.
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

// canonicalizeSubnetPools builds the canonical subnet-pool view and validates
// that no two pools belonging to different sites overlap. The overlap check
// deliberately uses tsacl policycore/addrspace's shared primitive so registry,
// policy, and audit consumers cannot drift on CIDR semantics.
func canonicalizeSubnetPools(raw map[string]Entry) (map[string]Entry, map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}

	rawCIDRs := make([]string, 0, len(raw))
	for rawCIDR := range raw {
		rawCIDRs = append(rawCIDRs, rawCIDR)
	}
	sort.Strings(rawCIDRs)

	pools := make(map[string]Entry, len(raw))
	rawKeys := make(map[string]string, len(raw))
	canonicalCIDRs := make([]string, 0, len(raw))
	for _, rawCIDR := range rawCIDRs {
		cidr, err := NormalizeSubnetCIDR(rawCIDR)
		if err != nil {
			// NormalizeSubnetCIDR wraps InvalidInputError; for stored-state
			// validation, strip the marker so this error is not misclassified
			// as caller input.
			err = storedStateError(err)
			return nil, nil, fmt.Errorf("invalid subnet pool key %q: %w", rawCIDR, err)
		}
		if firstRaw, ok := rawKeys[cidr]; ok {
			return nil, nil, fmt.Errorf("subnet CIDR %s is defined twice, as %q and %q", cidr, firstRaw, rawCIDR)
		}
		entry := raw[rawCIDR]
		if entry.Site == "" {
			return nil, nil, fmt.Errorf("subnet CIDR %s (key %q) has an empty or null site name", cidr, rawCIDR)
		}
		if metaErr := validateStoredReservationMetadata(entry.ReservationMetadata); metaErr != nil {
			return nil, nil, fmt.Errorf("subnet CIDR %s (key %q): %w", cidr, rawCIDR, storedStateError(metaErr))
		}
		pools[cidr] = entry
		rawKeys[cidr] = rawCIDR
		canonicalCIDRs = append(canonicalCIDRs, cidr)
	}

	pairs, parseErrors := addrspace.OverlappingPairs(canonicalCIDRs)
	var validationErr error
	for index := range canonicalCIDRs {
		if parseErr, ok := parseErrors[index]; ok {
			validationErr = fmt.Errorf("validating subnet CIDR %s: %w", canonicalCIDRs[index], parseErr)
			break
		}
	}
	if validationErr == nil {
		for _, pair := range pairs {
			left, right := canonicalCIDRs[pair[0]], canonicalCIDRs[pair[1]]
			if pools[left].Site != pools[right].Site {
				validationErr = fmt.Errorf(
					"subnet CIDRs %s (%q) and %s (%q) overlap",
					left, pools[left].Site, right, pools[right].Site,
				)
				break
			}
		}
	}
	// pools/rawKeys are returned even when validationErr is set, unlike the
	// per-entry-loop errors above (which return nil, nil): every raw entry
	// has already canonicalized successfully by this point, so the map is
	// complete and safe to hand back -- it's what lets ReleaseSubnetPoolWithLogger
	// repair an overlap by removing the offending entry (see
	// TestReleaseSubnetPoolCanRepairOverlapError). The per-entry-loop errors
	// have no such complete map to return: the loop aborted before every raw
	// key was canonicalized.
	return pools, rawKeys, validationErr
}

// ReserveSubnetPoolWithLogger reserves cidr for site in the registry at path,
// creating the file if needed. Repeating an exact CIDR+site reservation with
// no metadata is an idempotent no-op. A CIDR that overlaps any pool owned by a
// different site is refused with *SubnetPoolCollisionError, regardless of
// metadata; overlaps within one site are legal.
//
// metadata follows the same merge and auto-stamp rules as
// ReserveSiteIDWithLogger — see its doc comment.
func ReserveSubnetPoolWithLogger(
	path, cidr, site string,
	metadata ReservationMetadata,
	logger diagnostics.Logger,
) (string, string, ReservationMetadata, bool, error) {
	cidr, err := NormalizeSubnetCIDR(cidr)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	site, err = NormalizeSiteName(site)
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
		reg, loadErr := loadOrCreate(path, subnetPoolsKey, logger)
		if loadErr != nil {
			return loadErr
		}

		existingEntry, exists := reg.SubnetPools[cidr]
		if conflictErr := checkSubnetPoolConflict(
			existingEntry,
			exists,
			reg.SubnetPools,
			cidr,
			site,
		); conflictErr != nil {
			return conflictErr
		}
		newEntry, unchanged := resolveReservationEntry(existingEntry, exists, site, metadata, time.Now)
		if unchanged {
			resultMetadata = existingEntry.ReservationMetadata
			diagnostics.Debug(logger, "subnet-pool reservation already present")
			return nil
		}

		op, opErr := reservePatchOp(reg, subnetPoolsKey, cidr, newEntry)
		if opErr != nil {
			return opErr
		}
		if patchErr := hujsonedit.WritePatch(path, nil, op); patchErr != nil {
			return fmt.Errorf("reserving subnet CIDR %s: %w", cidr, patchErr)
		}
		changed = true
		resultMetadata = newEntry.ReservationMetadata
		diagnostics.Debug(logger, "subnet-pool reservation written")
		return nil
	})
	if lockErr != nil {
		return "", "", ReservationMetadata{}, false, lockErr
	}
	return cidr, site, resultMetadata, changed, nil
}

// AllocateSubnetPoolWithLogger reserves the first free size-bit child of
// pool for site in the registry at path, creating the file if needed. The
// pick runs inside the registry lock, after the file is loaded, so two
// concurrent allocations from one pool cannot receive the same block: each
// sees the other's write. Every subnet-pool reservation for any site and
// every prefix in liveTaken (routes advertised by live devices, collected by
// the caller because they are not registry state) counts as taken, exactly
// as calc next-free counts them. pool is unmapped from 4-in-6 and masked
// rather than held to NormalizeSubnetCIDR's canonical spelling: it is a
// search space, not a stored key, and the allocated child is canonical by
// construction.
//
// metadata follows the same merge and auto-stamp rules as
// ReserveSubnetPoolWithLogger. An exhausted pool is reported as
// *SubnetPoolExhaustedError with no reservation written.
func AllocateSubnetPoolWithLogger(
	path, pool string,
	size int,
	site string,
	liveTaken []string,
	metadata ReservationMetadata,
	logger diagnostics.Logger,
) (string, string, ReservationMetadata, bool, error) {
	poolPrefix, err := NormalizeAllocationPool(pool, size)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	taken, err := parseLiveTaken(liveTaken)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	site, err = NormalizeSiteName(site)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}
	metadata, err = normalizeMetadata(metadata)
	if err != nil {
		return "", "", ReservationMetadata{}, false, err
	}

	var cidr string
	var resultMetadata ReservationMetadata
	changed := false
	lockErr := withRegistryLock(path, logger, func() error {
		reg, loadErr := loadOrCreate(path, subnetPoolsKey, logger)
		if loadErr != nil {
			return loadErr
		}
		if replay, ok := findTicketReplay(reg.SubnetPools, poolPrefix, site, metadata.Ticket); ok {
			cidr = replay.CIDR
			resultMetadata = replay.ReservationMetadata
			diagnostics.Debug(logger, "subnet-pool allocation replayed by ticket")
			return nil
		}
		for reserved := range reg.SubnetPools {
			prefix, parseErr := netip.ParsePrefix(reserved)
			if parseErr != nil {
				return fmt.Errorf("stored subnet CIDR %s: %w", reserved, parseErr)
			}
			taken = append(taken, prefix)
		}
		block, ok := ipcalc.NextFree(poolPrefix, size, taken)
		if !ok {
			return &SubnetPoolExhaustedError{Pool: poolPrefix.String(), Size: size}
		}
		cidr = block.String()

		newEntry, _ := resolveReservationEntry(Entry{}, false, site, metadata, time.Now)
		op, opErr := reservePatchOp(reg, subnetPoolsKey, cidr, newEntry)
		if opErr != nil {
			return opErr
		}
		if patchErr := hujsonedit.WritePatch(path, nil, op); patchErr != nil {
			return fmt.Errorf("allocating subnet CIDR %s: %w", cidr, patchErr)
		}
		changed = true
		resultMetadata = newEntry.ReservationMetadata
		diagnostics.Debug(logger, "subnet-pool allocation written")
		return nil
	})
	if lockErr != nil {
		return "", "", ReservationMetadata{}, false, lockErr
	}
	return cidr, site, resultMetadata, changed, nil
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
		if err != nil || prefix.Addr().BitLen() != pool.Addr().BitLen() {
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
		taken = append(taken, prefix)
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

// ReleaseSubnetPoolWithLogger removes cidr from the registry at path. An
// absent reservation is an idempotent no-op; a missing registry file is an
// error, matching the site-ID release behavior.
func ReleaseSubnetPoolWithLogger(
	path, cidr string,
	logger diagnostics.Logger,
) (string, bool, error) {
	cidr, err := NormalizeSubnetCIDR(cidr)
	if err != nil {
		return "", false, err
	}
	changed, err := releaseRegistryEntry(
		path, subnetPoolsKey, cidr, "subnet CIDR", "subnet-pool", logger,
	)
	if err != nil {
		return "", false, err
	}
	return cidr, changed, nil
}

// ListSubnetPoolsWithLogger returns subnet reservations sorted by network
// address, then prefix length and CIDR text for a deterministic total order.
func ListSubnetPoolsWithLogger(path string, logger diagnostics.Logger) ([]SubnetPoolEntry, error) {
	reg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if mapErr := reg.mapError(subnetPoolsKey); mapErr != nil {
		return nil, mapErr
	}
	diagnostics.Debug(logger, "registry loaded",
		"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools),
		"service_address_count", len(reg.ServiceAddresses))
	return sortedSubnetPoolEntries(reg.SubnetPools), nil
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
