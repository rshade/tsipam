// Package registry provides comment-preserving, HuJSON-backed read/write
// access to the tail-ipam address-space registry — the declarative source of
// truth for reserved Tailscale address-space allocations. It supports 4via6
// site IDs and subnet-CIDR pools; per-service /32 allocation is separately
// scoped future work (see ROADMAP.md).
package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale/hujson"

	"github.com/rshade/tailscale-utils/policycore/hujsonedit"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
)

const (
	// siteIDsKey is the registry's top-level key for the site-ID map.
	siteIDsKey = "siteIDs"
	// subnetPoolsKey is the registry's top-level key for the subnet-pool map.
	subnetPoolsKey = "subnetPools"
)

// maxSiteID is the highest 4via6 site ID Tailscale's control plane accepts:
// the field is a 16-bit integer.
const maxSiteID = 0xffff

// Locking constants for withRegistryLock. The lockfile approach (plain
// O_CREATE|O_EXCL, not flock/fcntl) is deliberately the least portable-risk
// option: it needs no cgo or per-OS binding to work identically on every
// target this tool cross-compiles for.
const (
	lockSuffix         = ".lock"
	lockRetryInterval  = 20 * time.Millisecond
	lockAcquireTimeout = 5 * time.Second
)

// Registry is the tail-ipam address-space allocation ledger, canonicalized by
// Load. SiteIDs maps a canonical 4via6 site ID (see NormalizeSiteID) to its
// site, and SubnetPools maps a canonical CIDR (see NormalizeSubnetCIDR) to its
// site. rawKeys records each site-ID entry's original on-file key spelling
// (e.g. "01" for canonical "1"); subnetPoolRawKeys does the same for subnet
// pools. hujson's patch matching is exact and case/format-sensitive, so an
// edit that addresses an existing entry has to target the spelling the file
// actually uses.
//
// siteIDsPresent records whether the file's top-level "siteIDs" member
// already exists as an object node (empty or not) -- as opposed to being
// entirely absent, or present but null. It is not derivable from SiteIDs
// being non-nil: an existing-but-empty object and a genuinely absent member
// both decode to a nil SiteIDs map, yet ReserveSiteID must edit them with
// different patch strategies (see its doc comment). The same applies to
// subnetPoolsPresent for the "subnetPools" member.
//
// siteIDsErr and subnetPoolsErr capture any decode/validate failure scoped to
// that map only, so a caller that needs only one map is not blocked by a
// problem confined to the other (e.g., a malformed subnetPools does not
// prevent site-ID commands from working).
type Registry struct {
	SiteIDs            map[string]Entry
	SubnetPools        map[string]Entry
	rawKeys            map[string]string
	subnetPoolRawKeys  map[string]string
	siteIDsPresent     bool
	subnetPoolsPresent bool
	siteIDsErr         error
	subnetPoolsErr     error
}

// SiteIDEntry is one resolved site-ID reservation, returned by ListSiteIDs.
type SiteIDEntry struct {
	ReservationMetadata

	ID   string
	Site string
}

// SiteIDCollisionError reports that a site ID is already reserved for a
// different site than the one requested.
type SiteIDCollisionError struct {
	ID       string
	Existing string
	Wanted   string
}

func (e *SiteIDCollisionError) Error() string {
	return fmt.Sprintf("site ID %s is already reserved for %q, not %q", e.ID, e.Existing, e.Wanted)
}

// NormalizeSiteID validates and canonicalizes a 4via6 site ID: a decimal
// integer in Tailscale's 16-bit site-ID range, 0-65535. Canonicalizing
// through strconv means "01" and "1" resolve to the same registry key.
func NormalizeSiteID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("site ID must not be empty")
	}
	n, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return "", fmt.Errorf("site ID %q is not a non-negative integer: %w", id, err)
	}
	if n > maxSiteID {
		return "", fmt.Errorf("site ID %d is out of range: Tailscale 4via6 site IDs are 0-%d", n, maxSiteID)
	}
	return strconv.FormatUint(n, 10), nil
}

// NormalizeSiteName validates a site name: non-empty after trimming and
// containing no control characters (C0 control characters or DEL).
func NormalizeSiteName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("site name must not be empty")
	}
	if err := rejectControlChars("site name", name); err != nil {
		return "", err
	}
	return name, nil
}

// Load reads and decodes the registry at path. A missing file is reported as
// a distinct, checkable error (errors.Is(err, [os.ErrNotExist])) so a caller
// that should tolerate an absent registry (ReserveSiteID, which creates one)
// can tell that apart from a real read/parse failure.
//
// The top-level "siteIDs" member is resolved against the parsed HuJSON
// syntax tree, not against a decoded Go map, because the two views disagree
// on several document shapes a plain map can't distinguish: a member that is
// entirely absent, one present but null, one present as an object (which may
// itself be empty), and a document with more than one member that could
// plausibly be "siteIDs". Deciding from the decoded map alone conflates
// "absent" with "present but empty" -- see siteIDsPresent -- and silently
// tolerates a near-miss spelling or an outright duplicate that hujson's
// first-wins patch semantics and encoding/json's last-wins decode semantics
// would each resolve differently. This mirrors tail-acl's
// resolveTopLevelKey/checkDuplicateMembers (internal/aclpolicy/policy.go),
// adapted here to reject rather than tolerate a near-miss spelling: unlike
// tail-acl's Tailscale-schema fields, "siteIDs" is this tool's own invented
// key with no legacy-compat reason to accept an alternate spelling.
//
// A near-miss top-level spelling (e.g. "siteids") is rejected, as is an
// ambiguous document with more than one member that case-insensitively
// matches "siteIDs" -- including two members with the identical exact
// spelling. The same rules apply to "subnetPools". Any other top-level key
// loads and round-trips untouched: the registry is documented to grow
// additional top-level keys (for example per-service /32 allocations) in
// future versions, so unknown fields are not rejected wholesale. Within the
// "siteIDs" object itself, an exact-duplicate
// entry key is rejected for the same first-wins/last-wins reason, and every
// entry key is canonicalized through NormalizeSiteID: two raw keys that
// canonicalize to the same ID (e.g. "01" and "1") name both spellings in the
// returned error, since Load cannot decide which is right. An entry whose
// value is null or "" is rejected: a reservation must have a genuine
// non-empty site name. The same rules apply to subnet pools.
//
// Decode/validate failures are scoped to the affected map only (siteIDsErr
// or subnetPoolsErr): a caller that needs only one map can check mapError
// for that specific map and proceed even if the other map has an error.
func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, identityFreePathError(err)
	}
	// The UTF-8 byte-order mark (U+FEFF). hujson.Parse already fails on a
	// BOM-prefixed document with an "invalid character" error, but that
	// message gives no indication of the cause; checking for it directly
	// lets Load tell the operator what is actually wrong instead of
	// silently stripping it or leaving them to guess. The path must be
	// stripped from error output when sanitization is active.
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return nil, errors.New("file starts with a UTF-8 byte-order mark; re-save it as UTF-8 without a BOM")
	}

	value, err := hujson.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("decoding registry: %w", err)
	}
	value.Standardize()

	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("decoding registry: top-level document must be a JSON object")
	}

	reg := &Registry{}
	reg.loadSiteIDs(object)
	reg.loadSubnetPools(object)
	return reg, nil
}

// loadSiteIDs populates the siteIDs half of reg from object, or records a
// scoped siteIDsErr on failure. Keeping this independent of loadSubnetPools
// is what lets a subnetPools-only problem leave site-ID commands unaffected,
// and vice versa. Unlike loadSubnetPools, a canonicalize failure here leaves
// SiteIDs unset -- canonicalize has no partial-data case to hand back (see
// its own doc comment), so there is nothing to populate.
func (reg *Registry) loadSiteIDs(object *hujson.Object) {
	entries, present, err := decodeMapMember[Entry](object, siteIDsKey)
	if err != nil {
		reg.siteIDsErr = fmt.Errorf("decoding registry: %w", err)
		return
	}
	reg.siteIDsPresent = present
	canon, err := canonicalize(entries)
	if err != nil {
		reg.siteIDsErr = fmt.Errorf("decoding registry: %w", err)
		return
	}
	reg.SiteIDs = canon.SiteIDs
	reg.rawKeys = canon.rawKeys
}

// loadSubnetPools populates the subnetPools half of reg from object, or
// records a scoped subnetPoolsErr on failure. Keeping this independent of
// loadSiteIDs is what lets a siteIDs-only problem leave subnet-pool commands
// unaffected, and vice versa. Even when canonicalizeSubnetPools returns an
// error (e.g., overlapping pools), the partial pools map is still populated
// so that operations like release can repair the broken state.
func (reg *Registry) loadSubnetPools(object *hujson.Object) {
	entries, present, err := decodeMapMember[Entry](object, subnetPoolsKey)
	if err != nil {
		reg.subnetPoolsErr = fmt.Errorf("decoding registry: %w", err)
		return
	}
	pools, rawKeys, err := canonicalizeSubnetPools(entries)
	reg.SubnetPools = pools
	reg.subnetPoolRawKeys = rawKeys
	reg.subnetPoolsPresent = present
	if err != nil {
		reg.subnetPoolsErr = fmt.Errorf("decoding registry: %w", err)
	}
}

// mapAccessor bundles one top-level map's canonical entries, raw-key
// spellings, and scoped decode/validate error, so mapError and registryMap
// resolve topLevel through the single switch in mapAccessorFor instead of
// two that could silently drift apart when a third top-level map (e.g. the
// per-service /32 allocations named as future work above) is added and only
// one switch is updated for it.
type mapAccessor struct {
	entries map[string]Entry
	rawKeys map[string]string
	err     error
}

func (reg *Registry) mapAccessorFor(topLevel string) (mapAccessor, error) {
	switch topLevel {
	case siteIDsKey:
		return mapAccessor{entries: reg.SiteIDs, rawKeys: reg.rawKeys, err: reg.siteIDsErr}, nil
	case subnetPoolsKey:
		return mapAccessor{entries: reg.SubnetPools, rawKeys: reg.subnetPoolRawKeys, err: reg.subnetPoolsErr}, nil
	default:
		return mapAccessor{}, fmt.Errorf("unknown registry map %q", topLevel)
	}
}

// mapError returns the scoped decode/validate error for topLevel
// (siteIDsKey or subnetPoolsKey), so a caller that only needs one map is
// never blocked by a problem confined to the other.
func (reg *Registry) mapError(topLevel string) error {
	acc, err := reg.mapAccessorFor(topLevel)
	if err != nil {
		return err
	}
	return acc.err
}

// decodeMapMember resolves key as a top-level object member, rejects
// duplicate entry keys, and decodes its map of value type V (string for a
// pre-metadata document; Entry once the schema also allows an object form).
// present reports whether the member is an object rather than absent or
// null.
func decodeMapMember[V any](object *hujson.Object, key string) (map[string]V, bool, error) {
	idx, err := registryMemberIndex(object, key)
	if err != nil {
		return nil, false, err
	}
	if idx < 0 {
		return nil, false, nil
	}
	member := object.Members[idx].Value
	present := false
	if entryObject, isObject := member.Value.(*hujson.Object); isObject {
		present = true
		if dupErr := duplicateEntryKey(entryObject, key); dupErr != nil {
			return nil, false, dupErr
		}
	}
	var entries map[string]V
	if unmarshalErr := json.Unmarshal(member.Pack(), &entries); unmarshalErr != nil {
		return nil, false, fmt.Errorf("%q: %w", key, unmarshalErr)
	}
	return entries, present, nil
}

// registryMemberIndex locates the document's top-level key member and
// returns its index in object.Members, or -1 if no member's name
// case-insensitively matches key (the block is entirely absent).
//
// An error means the match is ambiguous: either the single matching member's
// exact spelling differs from key (a near-miss such as "siteids"), or
// more than one member matches case-insensitively -- which also covers two
// members with the identical exact spelling, since hujson does not merge
// duplicate object members the way encoding/json's map decode would.
func registryMemberIndex(object *hujson.Object, key string) (int, error) {
	var spellings []string
	idx := -1
	for i := range object.Members {
		literal, isLiteral := object.Members[i].Name.Value.(hujson.Literal)
		if !isLiteral {
			continue
		}
		name := literal.String()
		if !strings.EqualFold(name, key) {
			continue
		}
		spellings = append(spellings, name)
		idx = i
	}
	switch len(spellings) {
	case 0:
		return -1, nil
	case 1:
		if spellings[0] != key {
			return -1, fmt.Errorf("top-level key %q must be spelled %q", spellings[0], key)
		}
		return idx, nil
	default:
		quoted := make([]string, len(spellings))
		for i, spelling := range spellings {
			quoted[i] = strconv.Quote(spelling)
		}
		return -1, fmt.Errorf("multiple top-level keys match %q: %s", key, strings.Join(quoted, ", "))
	}
}

// duplicateEntryKey rejects an exact-duplicate raw key within a registry map.
// [json.Unmarshal] resolves a repeated key last-wins, silently
// collapsing it to one entry before canonicalize ever sees it, while
// hujson's patch matching is first-wins -- the same first-wins/last-wins
// mismatch tail-acl's checkDuplicateMembers guards against for the ACL
// policy file.
func duplicateEntryKey(object *hujson.Object, key string) error {
	seen := make(map[string]struct{}, len(object.Members))
	for i := range object.Members {
		literal, isLiteral := object.Members[i].Name.Value.(hujson.Literal)
		if !isLiteral {
			continue
		}
		name := literal.String()
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate %s entry key %q", key, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// canonicalize builds the canonical view of a raw siteIDs map: SiteIDs keyed
// by NormalizeSiteID's canonical spelling, and rawKeys recording each entry's
// original on-file key.
func canonicalize(raw map[string]Entry) (*Registry, error) {
	if len(raw) == 0 {
		return &Registry{}, nil
	}

	rawIDs := make([]string, 0, len(raw))
	for rawID := range raw {
		rawIDs = append(rawIDs, rawID)
	}
	sort.Strings(rawIDs)

	siteIDs := make(map[string]Entry, len(raw))
	rawKeys := make(map[string]string, len(raw))
	for _, rawID := range rawIDs {
		canonicalID, err := NormalizeSiteID(rawID)
		if err != nil {
			return nil, fmt.Errorf("invalid site ID key %q: %w", rawID, err)
		}
		if firstRaw, ok := rawKeys[canonicalID]; ok {
			return nil, fmt.Errorf("site ID %s is defined twice, as %q and %q", canonicalID, firstRaw, rawID)
		}
		entry := raw[rawID]
		if entry.Site == "" {
			return nil, fmt.Errorf("site ID %s (key %q) has an empty or null site name", canonicalID, rawID)
		}
		siteIDs[canonicalID] = entry
		rawKeys[canonicalID] = rawID
	}
	return &Registry{SiteIDs: siteIDs, rawKeys: rawKeys}, nil
}

// withRegistryLock runs fn while holding an exclusive, cross-process lock on
// path, so a caller's read-decide-write span (Load, decide, then
// WritePatch/create) is atomic with respect to other tail-ipam invocations
// against the same registry file. The lock is a plain lockfile at
// path+".lock", acquired with O_CREATE|O_EXCL and released by removing it
// when fn returns.
func withRegistryLock(path string, logger diagnostics.Logger, fn func() error) error {
	lockPath := path + lockSuffix
	deadline := time.Now().Add(lockAcquireTimeout)
	diagnostics.Debug(logger, "acquiring registry lock")
	waiting := false
	for {
		lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if closeErr := lockFile.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return fmt.Errorf("acquiring lock: %w", identityFreePathError(closeErr))
			}
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("acquiring lock: %w", identityFreePathError(err))
		}
		if !waiting {
			diagnostics.Debug(logger, "waiting for registry lock")
			waiting = true
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"acquiring lock: timed out after %s waiting for a concurrent tail-ipam to finish",
				lockAcquireTimeout)
		}
		time.Sleep(lockRetryInterval)
	}
	diagnostics.Debug(logger, "registry lock acquired", "waited", waiting)
	defer func() {
		if removeErr := os.Remove(lockPath); removeErr != nil {
			diagnostics.Warn(logger, "releasing registry lock failed", "error", identityFreePathError(removeErr))
			return
		}
		diagnostics.Debug(logger, "registry lock released")
	}()
	return fn()
}

// identityFreePathError keeps an underlying filesystem reason (for example,
// permission denied) while dropping a *PathError's registry path. Registry
// filenames contain the tailnet routing key and must not reappear in verbose
// output when TAILSCALE_SANITIZE is active.
func identityFreePathError(err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	return err
}

// RawEntries returns the best-effort, unvalidated site-ID and subnet-pool
// entries found in the registry file at path: raw key spellings mapped to
// their raw site values, exactly as decodeStringMapMember would read them
// before canonicalize/canonicalizeSubnetPools validates them. It never
// returns an error -- any read, parse, or decode failure at any stage
// yields a nil map for the affected side (or both) -- because its purpose
// is to let a caller pre-register every value that MIGHT appear verbatim
// in a Load error's text for sanitization, even when Load itself would
// fail on that very file. A returned entry is unnormalized: it is a
// sweep candidate, not a validated value.
func RawEntries(path string) (map[string]string, map[string]string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	value, err := hujson.Parse(raw)
	if err != nil {
		return nil, nil
	}
	value.Standardize()
	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, nil
	}
	siteIDs := rawMapMember(object, siteIDsKey)
	subnetPools := rawMapMember(object, subnetPoolsKey)
	return siteIDs, subnetPools
}

// rawMapMember best-effort decodes key's top-level member as a string map,
// skipping decodeStringMapMember's validation (near-miss spelling,
// ambiguous member, duplicate entry key): any such problem yields a nil
// map rather than an error, since RawEntries must never itself fail. On
// an ambiguous match (more than one case-insensitively matching member),
// it uses the last one -- good enough for a sweep-value candidate list,
// which is the only thing this feeds.
func rawMapMember(object *hujson.Object, key string) map[string]string {
	idx := -1
	for i := range object.Members {
		literal, isLiteral := object.Members[i].Name.Value.(hujson.Literal)
		if !isLiteral || !strings.EqualFold(literal.String(), key) {
			continue
		}
		idx = i
	}
	if idx < 0 {
		return nil
	}
	var entries map[string]string
	if err := json.Unmarshal(object.Members[idx].Value.Pack(), &entries); err != nil {
		return nil
	}
	return entries
}

// loadOrCreate loads the registry at path, creating it as an empty "{}\n"
// document first if it is absent. Creation uses O_CREATE|O_EXCL so a second
// caller that also observed the file as absent cannot silently overwrite the
// first's create: it re-Loads instead, picking up whatever is now on disk.
// Callers must run this inside withRegistryLock. topLevel selects which map
// (siteIDsKey or subnetPoolsKey) the caller needs; after a successful load,
// loadOrCreate checks mapError(topLevel) internally before returning.
func loadOrCreate(path, topLevel string, logger diagnostics.Logger) (*Registry, error) {
	reg, err := Load(path)
	switch {
	case err == nil:
		if mapErr := reg.mapError(topLevel); mapErr != nil {
			return nil, mapErr
		}
		diagnostics.Debug(logger, "registry loaded",
			"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools))
		return reg, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}

	f, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	switch {
	case createErr == nil:
		_, writeErr := f.WriteString("{}\n")
		closeErr := f.Close()
		if writeErr != nil {
			return nil, fmt.Errorf("creating registry: %w", identityFreePathError(writeErr))
		}
		if closeErr != nil {
			return nil, fmt.Errorf("creating registry: %w", identityFreePathError(closeErr))
		}
		diagnostics.Debug(logger, "registry created", "site_id_count", 0, "subnet_pool_count", 0)
		return &Registry{}, nil
	case errors.Is(createErr, os.ErrExist):
		// Another holder created it between our Load and this attempt; pick
		// up whatever is now on disk rather than treating this as an error.
		reloaded, loadErr := Load(path)
		if loadErr == nil {
			if mapErr := reloaded.mapError(topLevel); mapErr != nil {
				return nil, mapErr
			}
			diagnostics.Debug(logger, "registry loaded",
				"site_id_count", len(reloaded.SiteIDs), "subnet_pool_count", len(reloaded.SubnetPools))
		}
		return reloaded, loadErr
	default:
		return nil, fmt.Errorf("creating registry: %w", identityFreePathError(createErr))
	}
}

// ReserveSiteIDWithLogger reserves id for site in the registry at path, creating the
// file (as "{}\n") if it does not exist yet. Reserving the same id+site pair
// again with no metadata is a no-op — changed reports false, err is nil — so
// repeating the bare command is idempotent. Reserving an id already held by a
// different site fails with *SiteIDCollisionError rather than overwriting it,
// regardless of metadata. The whole read-decide-write span runs under an
// exclusive file lock (see withRegistryLock), so two concurrent reservations
// against a fresh registry cannot both win a whole-object create and silently
// drop one another.
//
// metadata is optional: a zero ReservationMetadata reproduces the original
// bare-string behavior exactly, including the file bytes written, so an
// existing plain-string registry re-saves unchanged. A non-zero metadata is
// merged onto whatever the id already carries — a field left blank in
// metadata keeps its stored value rather than being cleared — which lets a
// second reserve call amend just the field that changed (e.g. correct a
// ticket reference) without erasing the rest. ReservedAt is auto-stamped to
// now (UTC) the first time Owner, Description, or Ticket is set on this
// reservation and no ReservedAt (explicit or already stored) exists yet; it
// is left untouched otherwise, including on a bare repeat reserve of an
// entry that already carries metadata.
//
// On success it returns the canonical id and site name actually written
// (e.g. "007" normalizes to "7"), not the raw arguments, so a caller does not
// need to re-derive what NormalizeSiteID/NormalizeSiteName already computed,
// plus the metadata actually stored after the merge and any auto-stamp.
// logger receives identity-free I/O diagnostics; nil is quiet.
func ReserveSiteIDWithLogger(
	path, id, site string,
	metadata ReservationMetadata,
	logger diagnostics.Logger,
) (string, string, ReservationMetadata, bool, error) {
	id, err := NormalizeSiteID(id)
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
		reg, loadErr := loadOrCreate(path, siteIDsKey, logger)
		if loadErr != nil {
			return loadErr
		}

		existingEntry, exists := reg.SiteIDs[id]
		if exists && existingEntry.Site != site {
			return &SiteIDCollisionError{ID: id, Existing: existingEntry.Site, Wanted: site}
		}
		newEntry, unchanged := resolveReservationEntry(existingEntry, exists, site, metadata, time.Now)
		if unchanged {
			resultMetadata = existingEntry.ReservationMetadata
			diagnostics.Debug(logger, "site-ID reservation already present")
			return nil
		}

		var op hujsonedit.PatchOperation
		if !reg.siteIDsPresent {
			// The siteIDs member itself is absent (or present but null): one
			// add creates it with this entry, mirroring tail-acl's
			// AddDefinition for an absent map. Safe to use the whole-object
			// add unconditionally here because the lock guarantees no
			// concurrent writer can have raced this decision.
			//
			// This must NOT be reg.SiteIDs == nil: an existing-but-empty
			// siteIDs object also decodes to a nil map, and a whole-object
			// add there would silently destroy anything else attached to
			// that node -- e.g. a hand-written comment inside "siteIDs": {}
			// -- by replacing the node instead of adding a key into it. See
			// siteIDsPresent's doc comment.
			op = hujsonedit.PatchAdd(map[string]Entry{id: newEntry}, siteIDsKey)
		} else {
			op = hujsonedit.PatchAdd(newEntry, siteIDsKey, id)
		}
		if patchErr := hujsonedit.WritePatch(path, nil, op); patchErr != nil {
			return fmt.Errorf("reserving site ID %s: %w", id, patchErr)
		}
		changed = true
		resultMetadata = newEntry.ReservationMetadata
		diagnostics.Debug(logger, "site-ID reservation written")
		return nil
	})
	if lockErr != nil {
		return "", "", ReservationMetadata{}, false, lockErr
	}
	return id, site, resultMetadata, changed, nil
}

// ReleaseSiteIDWithLogger removes id from the registry at path. Releasing an id that is
// not present is a no-op — changed reports false, err is nil — matching
// tail-acl's RemoveDefinition behavior for an absent name. A missing registry
// file is an error: release has nothing to remove from and isn't the command
// that creates a registry. The read-decide-write span runs under the same
// exclusive file lock as ReserveSiteIDWithLogger.
//
// On success it returns the canonical id released, not the raw argument.
// logger receives identity-free I/O diagnostics; nil is quiet.
func ReleaseSiteIDWithLogger(
	path, id string,
	logger diagnostics.Logger,
) (string, bool, error) {
	id, err := NormalizeSiteID(id)
	if err != nil {
		return "", false, err
	}
	changed, err := releaseRegistryEntry(path, siteIDsKey, id, "site ID", "site-ID", logger)
	if err != nil {
		return "", false, err
	}
	return id, changed, nil
}

// releaseRegistryEntry owns the common locked removal mechanics for each
// map-shaped registry allocation primitive. topLevel selects the owned map;
// displayName and diagnosticName keep user-facing errors and structured event
// names specific without duplicating the read-decide-patch transaction.
// Note: releaseRegistryEntry does NOT check mapError for the target map,
// because release operations need to be able to repair broken maps by removing
// entries that caused validation errors (e.g., overlapping subnet pools).
func releaseRegistryEntry(
	path, topLevel, key, displayName, diagnosticName string,
	logger diagnostics.Logger,
) (bool, error) {
	changed := false
	err := withRegistryLock(path, logger, func() error {
		reg, loadErr := Load(path)
		if loadErr != nil {
			return loadErr
		}
		diagnostics.Debug(logger, "registry loaded",
			"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools))
		entries, rawKeys, selectErr := registryMap(reg, topLevel)
		if selectErr != nil {
			return selectErr
		}
		if _, ok := entries[key]; !ok {
			diagnostics.Debug(logger, diagnosticName+" reservation already absent")
			return nil
		}
		rawKey := rawKeys[key]
		if rawKey == "" {
			rawKey = key
		}
		if patchErr := hujsonedit.WritePatch(
			path, nil, hujsonedit.PatchRemove(topLevel, rawKey),
		); patchErr != nil {
			return fmt.Errorf("releasing %s %s: %w", displayName, key, patchErr)
		}
		changed = true
		diagnostics.Debug(logger, diagnosticName+" reservation removed")
		return nil
	})
	return changed, err
}

func registryMap(reg *Registry, topLevel string) (map[string]Entry, map[string]string, error) {
	acc, err := reg.mapAccessorFor(topLevel)
	if err != nil {
		return nil, nil, err
	}
	return acc.entries, acc.rawKeys, nil
}

// ListSiteIDsWithLogger returns the registry's site-ID entries at path, sorted
// numerically by ID. A missing file is an error, for the same reason as
// ReleaseSiteIDWithLogger. The sort is stable with a total-order comparator (numeric
// when both IDs parse and differ, otherwise a plain string compare) so output
// order is deterministic regardless of Go's randomized map iteration.
// logger receives identity-free I/O diagnostics; nil is quiet.
func ListSiteIDsWithLogger(path string, logger diagnostics.Logger) ([]SiteIDEntry, error) {
	reg, err := Load(path)
	if err != nil {
		return nil, err
	}
	if mapErr := reg.mapError(siteIDsKey); mapErr != nil {
		return nil, mapErr
	}
	diagnostics.Debug(logger, "registry loaded",
		"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools))
	entries := make([]SiteIDEntry, 0, len(reg.SiteIDs))
	for id, entry := range reg.SiteIDs {
		entries = append(entries, SiteIDEntry{ID: id, Site: entry.Site, ReservationMetadata: entry.ReservationMetadata})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, leftErr := strconv.Atoi(entries[i].ID)
		right, rightErr := strconv.Atoi(entries[j].ID)
		if leftErr == nil && rightErr == nil && left != right {
			return left < right
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, nil
}
