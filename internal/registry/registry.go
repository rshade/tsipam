// Package registry provides comment-preserving, HuJSON-backed read/write
// access to the tail-ipam address-space registry — the declarative source of
// truth for reserved Tailscale allocations. It supports 4via6 site IDs,
// subnet-CIDR pools, per-service IPv4 address reservations, and per-device UDP
// listen-port reservations scoped to a site/firewall boundary.
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
	// serviceAddressesKey is the registry's top-level key for per-service IPv4
	// address reservations.
	serviceAddressesKey = "serviceAddresses"
	// udpPortsKey is the registry's top-level key for per-site, per-device UDP
	// listen-port reservations.
	udpPortsKey = "udpPorts"
)

// storedStateError strips the InvalidInputError marker from an error if present,
// returning the wrapped inner error. This is used for stored-state validation
// errors (as opposed to caller-supplied input errors) so they are not
// misclassified as HTTP 400 bad-input failures.
func storedStateError(err error) error {
	if invalidInput, ok := errors.AsType[*InvalidInputError](err); ok {
		return invalidInput.Unwrap()
	}
	return err
}

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
// site; SubnetPools maps a canonical CIDR (see NormalizeSubnetCIDR) to its
// site; ServiceAddresses maps a canonical IPv4 address (see
// NormalizeServiceAddress) to a per-service entry; and UDPPorts maps a
// canonical site name and canonical port (see NormalizePort) to a device
// name. rawKeys records each
// site-ID entry's original on-file key spelling (e.g. "01" for canonical "1");
// subnetPoolRawKeys and serviceAddressRawKeys do the same for subnet pools and
// service addresses. hujson's patch matching is exact and case/format-sensitive,
// so an edit that addresses an existing entry has to target the spelling the
// file actually uses.
//
// siteIDsPresent records whether the file's top-level "siteIDs" member
// already exists as an object node (empty or not) -- as opposed to being
// entirely absent, or present but null. It is not derivable from SiteIDs
// being non-nil: an existing-but-empty object and a genuinely absent member
// both decode to a nil SiteIDs map, yet ReserveSiteID must edit them with
// different patch strategies (see its doc comment). The same applies to
// subnetPoolsPresent for the "subnetPools" member and serviceAddressesPresent
// for the "serviceAddresses" member. UDP ports additionally track whether
// each site's child object is present, because that second level can also be
// absent, null, empty, or populated.
//
// The four *Err fields capture decode/validate failures scoped to one map, so
// a caller that needs only one map is not blocked by a problem confined to
// another (e.g., a malformed subnetPools does not prevent UDP-port commands
// from working).
type Registry struct {
	SiteIDs                 map[string]Entry
	SubnetPools             map[string]Entry
	ServiceAddresses        map[string]ServiceEntry
	UDPPorts                map[string]map[string]string
	rawKeys                 map[string]string
	subnetPoolRawKeys       map[string]string
	serviceAddressRawKeys   map[string]string
	udpPortSiteRawKeys      map[string]string
	udpPortRawKeys          map[string]map[string]string
	siteIDsPresent          bool
	subnetPoolsPresent      bool
	serviceAddressesPresent bool
	udpPortsPresent         bool
	udpPortSitePresent      map[string]bool
	siteIDsErr              error
	subnetPoolsErr          error
	serviceAddressesErr     error
	udpPortsErr             error
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
// Validation errors are wrapped with invalidInput so they are classified as
// HTTP 400 caller-input failures on caller-supplied input, and unwrapped by
// storedStateError on stored-state validation.
func NormalizeSiteID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", invalidInput(errors.New("site ID must not be empty"))
	}
	n, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return "", invalidInput(fmt.Errorf("site ID %q is not a non-negative integer: %w", id, err))
	}
	if n > maxSiteID {
		return "", invalidInput(fmt.Errorf(
			"site ID %d is out of range: Tailscale 4via6 site IDs are 0-%d", n, maxSiteID,
		))
	}
	return strconv.FormatUint(n, 10), nil
}

// NormalizeSiteName validates a site name: non-empty after trimming and
// containing no control characters (C0 control characters or DEL) and valid UTF-8.
// Validation errors are wrapped with invalidInput so they are classified as
// HTTP 400 caller-input failures on caller-supplied input, and unwrapped by
// storedStateError on stored-state validation.
func NormalizeSiteName(name string) (string, error) {
	return normalizeLabel("site name", name)
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
// spelling. The same rules apply to "subnetPools", "serviceAddresses", and
// "udpPorts".
// Any other top-level key loads and round-trips untouched: the registry is
// documented to grow additional top-level keys (for example per-service
// address ranges or next-free allocation pools) in future versions, so
// unknown fields are not rejected wholesale. Within the "siteIDs" object
// itself, an exact-duplicate entry key is rejected for the same
// first-wins/last-wins reason, and every entry key is canonicalized through
// NormalizeSiteID: two raw keys that canonicalize to the same ID (e.g. "01"
// and "1") name both spellings in the returned error, since Load cannot
// decide which is right. An entry whose value is null or "" is rejected: a
// reservation must have a genuine non-empty site name. The same rules apply
// to subnet pools and service addresses. UDP ports repeat duplicate and
// canonical-key validation at both their site and port nesting levels.
//
// Decode/validate failures are scoped to the affected map only: a caller that
// needs one map can check mapError for that specific map and proceed even if
// another map has errors.
func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, identityFreePathError(err)
	}
	return loadBytes(raw)
}

// loadBytes decodes an in-memory registry document, applying every rule Load
// documents; Load is [os.ReadFile] followed by loadBytes.
//
// It is separate so a candidate document -- one a caller has patched in
// memory but has not written and may yet decide to discard -- can be judged
// against exactly the rules the next Load will apply, without the candidate
// ever reaching the filesystem. See repairMalformedMap, which is the reason
// this split exists.
func loadBytes(raw []byte) (*Registry, error) {
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
	reg.loadServiceAddresses(object)
	reg.loadUDPPorts(object)
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

// loadServiceAddresses populates the serviceAddresses third of reg or records
// a scoped error without affecting the other registry maps.
func (reg *Registry) loadServiceAddresses(object *hujson.Object) {
	entries, present, err := decodeMapMember[ServiceEntry](object, serviceAddressesKey)
	if err != nil {
		reg.serviceAddressesErr = fmt.Errorf("decoding registry: %w", err)
		return
	}
	addresses, rawKeys, err := canonicalizeServiceAddresses(entries)
	reg.ServiceAddresses = addresses
	reg.serviceAddressRawKeys = rawKeys
	reg.serviceAddressesPresent = present
	if err != nil {
		reg.serviceAddressesErr = fmt.Errorf("decoding registry: %w", err)
	}
}

// mapAccessor bundles the type-agnostic questions shared by all top-level
// maps: canonical key membership, raw-key spelling, on-file presence of the
// containing block, and scoped validation error. Concrete reserve/list
// operations continue to use their typed maps.
// hasView reports whether the canonical map is non-nil (e.g. reg.SiteIDs != nil);
// it's false when a map decode/validate failure prevented building a view.
// present carries the map's *Present flag (see Registry's doc comment), so
// reservePatchOp reads it off the registry rather than taking it as an
// argument a fourth map could pass wrongly or forget entirely.
type mapAccessor struct {
	hasKey  func(string) bool
	rawKeys map[string]string
	err     error
	hasView bool
	present bool
}

func (reg *Registry) mapAccessorFor(topLevel string) (mapAccessor, error) {
	switch topLevel {
	case siteIDsKey:
		return mapAccessor{
			hasKey:  func(key string) bool { _, ok := reg.SiteIDs[key]; return ok },
			rawKeys: reg.rawKeys, err: reg.siteIDsErr,
			hasView: reg.SiteIDs != nil,
			present: reg.siteIDsPresent,
		}, nil
	case subnetPoolsKey:
		return mapAccessor{
			hasKey:  func(key string) bool { _, ok := reg.SubnetPools[key]; return ok },
			rawKeys: reg.subnetPoolRawKeys, err: reg.subnetPoolsErr,
			hasView: reg.SubnetPools != nil,
			present: reg.subnetPoolsPresent,
		}, nil
	case serviceAddressesKey:
		return mapAccessor{
			hasKey:  func(key string) bool { _, ok := reg.ServiceAddresses[key]; return ok },
			rawKeys: reg.serviceAddressRawKeys, err: reg.serviceAddressesErr,
			hasView: reg.ServiceAddresses != nil,
			present: reg.serviceAddressesPresent,
		}, nil
	case udpPortsKey:
		// UDP ports is a two-level nested map (site -> port -> device), so no
		// single canonical key addresses a reservation; only the error state is
		// consumed (via mapError).
		return mapAccessor{err: reg.udpPortsErr}, nil
	default:
		return mapAccessor{}, fmt.Errorf("unknown registry map %q", topLevel)
	}
}

// mapError returns the scoped decode/validate error for topLevel, so a caller
// that only needs one map is never blocked by a problem confined to another.
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
			// NormalizeSiteID wraps InvalidInputError; for stored-state
			// validation, strip the marker so this error is not misclassified
			// as caller input.
			err = storedStateError(err)
			return nil, fmt.Errorf("invalid site ID key %q: %w", rawID, err)
		}
		if firstRaw, ok := rawKeys[canonicalID]; ok {
			return nil, fmt.Errorf("site ID %s is defined twice, as %q and %q", canonicalID, firstRaw, rawID)
		}
		entry := raw[rawID]
		if entry.Site == "" {
			return nil, fmt.Errorf("site ID %s (key %q) has an empty or null site name", canonicalID, rawID)
		}
		if metaErr := validateStoredReservationMetadata(entry.ReservationMetadata); metaErr != nil {
			return nil, fmt.Errorf("site ID %s (key %q): %w", canonicalID, rawID, storedStateError(metaErr))
		}
		siteIDs[canonicalID] = entry
		rawKeys[canonicalID] = rawID
	}
	return &Registry{SiteIDs: siteIDs, rawKeys: rawKeys}, nil
}

// canonicalizeServiceAddresses builds the canonical service-address view.
// Exact /32 raw keys are accepted as aliases for their bare address, while
// two raw spellings resolving to one address are rejected as ambiguous.
func canonicalizeServiceAddresses(
	raw map[string]ServiceEntry,
) (map[string]ServiceEntry, map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}

	rawAddresses := make([]string, 0, len(raw))
	for rawAddress := range raw {
		rawAddresses = append(rawAddresses, rawAddress)
	}
	sort.Strings(rawAddresses)

	addresses := make(map[string]ServiceEntry, len(raw))
	rawKeys := make(map[string]string, len(raw))
	for _, rawAddress := range rawAddresses {
		address, err := NormalizeServiceAddress(rawAddress)
		if err != nil {
			// NormalizeServiceAddress wraps InvalidInputError; for stored-state
			// validation, strip the marker so this error is not misclassified
			// as caller input.
			err = storedStateError(err)
			return nil, nil, fmt.Errorf("invalid service address key %q: %w", rawAddress, err)
		}
		if firstRaw, ok := rawKeys[address]; ok {
			return nil, nil, fmt.Errorf(
				"service address %s is defined twice, as %q and %q",
				address,
				firstRaw,
				rawAddress,
			)
		}
		entry := raw[rawAddress]
		service, err := NormalizeServiceName(entry.Service)
		if err != nil {
			// NormalizeServiceName wraps InvalidInputError; for stored-state
			// validation, strip the marker so this error is not misclassified
			// as caller input, and name the key for context.
			err = storedStateError(err)
			return nil, nil, fmt.Errorf("service address %s (key %q): %w", address, rawAddress, err)
		}
		entry.Service = service
		if metaErr := validateStoredReservationMetadata(entry.ReservationMetadata); metaErr != nil {
			return nil, nil, fmt.Errorf(
				"service address %s (key %q): %w", address, rawAddress, storedStateError(metaErr),
			)
		}
		addresses[address] = entry
		rawKeys[address] = rawAddress
	}
	return addresses, rawKeys, nil
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
	if pathError, ok := errors.AsType[*os.PathError](err); ok {
		return pathError.Err
	}
	return err
}

// RawEntries returns best-effort, unvalidated identity values from all three
// registry maps. It reads both concise string entries and metadata-bearing
// object entries so a caller can pre-register every value that might surface
// in a Load error. Returned values are sweep candidates, not validated state.
// It never returns an error — any read, parse, or decode failure at any stage
// yields a nil map for the affected side. This preserves sweep candidates even
// when Load itself would fail on that very file: values must be registrable
// even in the presence of validation errors.
func RawEntries(path string) (map[string]string, map[string]string, map[string]string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil
	}
	value, err := hujson.Parse(raw)
	if err != nil {
		return nil, nil, nil
	}
	value.Standardize()
	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, nil, nil
	}
	siteIDs := rawReservationMapMember(object, siteIDsKey, "site")
	subnetPools := rawReservationMapMember(object, subnetPoolsKey, "site")
	serviceAddresses := rawReservationMapMember(object, serviceAddressesKey, "service")
	return siteIDs, subnetPools, serviceAddresses
}

// rawReservationMapMember best-effort extracts either a string value or the
// named subject field from an object value. On an ambiguous top-level match it
// uses the last member; this is sufficient for a sanitizer candidate list.
//
// Every entry key is returned unconditionally -- with whatever subject
// string was extractable, or "" when the entry is an object missing
// subjectField or carrying a non-string value for it -- regardless of
// whether the value validated; it is each caller's choice whether that key
// is itself an identity value worth registering as a sweep candidate.
func rawReservationMapMember(object *hujson.Object, key, subjectField string) map[string]string {
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
	entryObject, ok := object.Members[idx].Value.Value.(*hujson.Object)
	if !ok {
		return nil
	}
	entries := make(map[string]string, len(entryObject.Members))
	for i := range entryObject.Members {
		literal, isLiteral := entryObject.Members[i].Name.Value.(hujson.Literal)
		if !isLiteral {
			continue
		}
		entryKey := literal.String()
		packed := entryObject.Members[i].Value.Pack()
		var subject string
		if err := json.Unmarshal(packed, &subject); err == nil {
			entries[entryKey] = subject
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(packed, &fields); err == nil {
			if rawSubject, present := fields[subjectField]; present {
				_ = json.Unmarshal(rawSubject, &subject)
			}
		}
		entries[entryKey] = subject
	}
	return entries
}

// loadOrCreate loads the registry at path, creating it as an empty "{}\n"
// document first if it is absent. Creation uses O_CREATE|O_EXCL so a second
// caller that also observed the file as absent cannot silently overwrite the
// first's create: it re-Loads instead, picking up whatever is now on disk.
// Callers must run this inside withRegistryLock. topLevel selects which map
// (siteIDsKey, subnetPoolsKey, serviceAddressesKey, or udpPortsKey) the caller needs; after
// a successful load, loadOrCreate checks mapError(topLevel) internally before
// returning.
func loadOrCreate(path, topLevel string, logger diagnostics.Logger) (*Registry, error) {
	reg, err := Load(path)
	switch {
	case err == nil:
		if mapErr := reg.mapError(topLevel); mapErr != nil {
			return nil, mapErr
		}
		diagnostics.Debug(logger, "registry loaded",
			"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools),
			"service_address_count", len(reg.ServiceAddresses), "udp_port_count", reg.udpPortCount())
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
		diagnostics.Debug(logger, "registry created",
			"site_id_count", 0, "subnet_pool_count", 0,
			"service_address_count", 0, "udp_port_count", 0)
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
				"site_id_count", len(reloaded.SiteIDs), "subnet_pool_count", len(reloaded.SubnetPools),
				"service_address_count", len(reloaded.ServiceAddresses),
				"udp_port_count", reloaded.udpPortCount())
		}
		return reloaded, loadErr
	default:
		return nil, fmt.Errorf("creating registry: %w", identityFreePathError(createErr))
	}
}

// reservePatchOp decides the hujsonedit.PatchOperation a reserve call should
// apply to install newValue under key in topLevel. Whether the containing
// block already exists as an object node (see the *Present fields' doc
// comment on Registry) decides the strategy: when it does not, a single
// whole-object add both creates the block and installs the entry, safe to
// use unconditionally because the caller holds the registry lock across this
// decision. Presence is the *Present flag and must NOT be re-derived from
// the canonical map being nil: an existing-but-empty "siteIDs" object also
// decodes to a nil map, and a whole-object add there would silently destroy
// anything else attached to that node -- e.g. a hand-written comment inside
// "siteIDs": {} -- by replacing the node instead of adding a key into it.
//
// When the block does exist, the add must target whichever raw
// on-file spelling the registry already recorded for key -- hujson's patch
// matching is exact and format-sensitive (see Registry's doc comment), so
// amending an entry whose raw spelling differs from its canonical form (e.g.
// "01" for site ID "1") by targeting the canonical spelling instead lands as
// a second, duplicate member rather than replacing the first. Shared by
// ReserveSiteIDWithLogger, ReserveServiceAddressWithLogger, and
// ReserveSubnetPoolWithLogger so this decision cannot drift between the
// three maps that need it: NormalizeSubnetCIDR trims surrounding whitespace
// before comparing against the canonical form, so a whitespace-padded
// subnetPools raw key loads successfully with rawKey != canonicalKey just
// like a non-canonical siteIDs or serviceAddresses key does.
//
// Every input except newValue is read off reg through mapAccessorFor rather
// than passed in. That is deliberate and structural: a hand-passed presence
// flag and raw-key map are what let ReserveSubnetPoolWithLogger once write
// its own patch branch, asserting in prose that it was equivalent when it
// was not. Naming topLevel now selects the presence flag, the raw-key
// spellings, and the membership test together, so a fourth map cannot pair
// one map's key with another's state, and an unknown topLevel is a returned
// error rather than a silently empty accessor.
func reservePatchOp[V any](
	reg *Registry, topLevel, key string, newValue V,
) (hujsonedit.PatchOperation, error) {
	acc, err := reg.mapAccessorFor(topLevel)
	if err != nil {
		return hujsonedit.PatchOperation{}, err
	}
	if !acc.present {
		return hujsonedit.PatchAdd(map[string]V{key: newValue}, topLevel), nil
	}
	rawKey := key
	if acc.hasKey(key) && acc.rawKeys[key] != "" {
		rawKey = acc.rawKeys[key]
	}
	return hujsonedit.PatchAdd(newValue, topLevel, rawKey), nil
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
//
// It mirrors ReserveServiceAddressWithLogger's locked read-decide-write
// transaction by necessity, not copy-paste: both reservation primitives share
// the same normalize/lock/collide/resolve/patch shape, and reservePatchOp
// already factors out the one piece that was genuinely identical logic.
//
//nolint:dupl // mirrors ReserveServiceAddressWithLogger's normalize/lock/collide/resolve/patch shape; unifying needs a generic that costs more than it saves
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

		op, opErr := reservePatchOp(reg, siteIDsKey, id, newEntry)
		if opErr != nil {
			return opErr
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
//
// releaseRegistryEntry does NOT check mapError for the target map when a
// canonical view exists, because release operations need to be able to repair
// broken maps by removing entries that caused validation errors (e.g.,
// overlapping subnet pools). When a map has a validation error and no usable
// view at all, release still attempts a best-effort repair via
// repairMalformedMap: key has already been normalized by the caller, so it
// finds raw on-file keys that normalize to it, tries removing each exact raw
// spelling, and keeps a removal only when the resulting document demonstrably
// fixes topLevel's view -- see repairMalformedMap and
// repairCandidateFixesMap for the predicate and why it takes two conditions
// rather than one. When the repair does not qualify, the file is left
// byte-for-byte as it was (it is never written; the candidate is judged in
// memory) and release fails closed with an error rather than reporting a
// confident but untrustworthy "not reserved" result.
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
			"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools),
			"service_address_count", len(reg.ServiceAddresses), "udp_port_count", reg.udpPortCount())
		acc, selectErr := reg.mapAccessorFor(topLevel)
		if selectErr != nil {
			return selectErr
		}
		if !acc.hasKey(key) {
			repaired, absentErr := releaseAbsentKey(path, topLevel, key, displayName, diagnosticName, acc, logger)
			if absentErr != nil {
				return absentErr
			}
			changed = repaired
			return nil
		}
		rawKey := acc.rawKeys[key]
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

// releaseAbsentKey handles releaseRegistryEntry's case where key is absent
// from topLevel's canonical view: either a genuine no-op (the key was never
// reserved, or topLevel decoded with no error and simply lacks it), or --
// when topLevel's view could not be built at all -- a best-effort repair via
// repairMalformedMap. It reports whether the release changed anything, in the
// same two-value shape releaseRegistryEntry's closure returns, so the caller
// can pass its result straight through.
func releaseAbsentKey(
	path, topLevel, key, displayName, diagnosticName string,
	acc mapAccessor,
	logger diagnostics.Logger,
) (bool, error) {
	if acc.err == nil || acc.hasView {
		diagnostics.Debug(logger, diagnosticName+" reservation already absent")
		return false, nil
	}
	repaired, repairErr := repairMalformedMap(path, topLevel, key)
	if repairErr != nil {
		return false, repairErr
	}
	if repaired {
		diagnostics.Debug(logger, diagnosticName+" reservation removed by raw-key repair")
		return true, nil
	}
	return false, fmt.Errorf("cannot determine whether %s %s is reserved: %w", displayName, key, acc.err)
}

// repairMalformedMap attempts, under the caller's registry lock (see
// withRegistryLock -- releaseRegistryEntry's caller holds it across this
// entire span), to remove an on-file spelling that normalizes to key from
// topLevel when that map's canonical view could not be built at all.
//
// The candidate is produced and judged entirely in memory: the file is read
// once, patched with hujsonedit.PatchBytes, and reloaded through loadBytes.
// It is written back only once that reload BOTH shows topLevel's view now
// validates AND shows key absent from the rebuilt view -- see
// repairCandidateFixesMap for why one without the other proves nothing.
//
// Deciding before writing rather than after is what makes the refusal path
// trivially safe. A repair that writes first has to undo itself, and that
// undo is a second write that can fail, that opens a window in which the
// registry holds a rejected repair, and that must not re-serialize the
// document (this registry is comment-preserving HuJSON, so the only sound
// restore is the original bytes). None of that exists here: a rejected
// candidate is simply dropped, and the file is untouched because it was
// never touched. Reports whether the repair was kept, and an error only if
// the single write of an already-validated candidate fails.
func repairMalformedMap(path, topLevel, key string) (bool, error) {
	original, readErr := os.ReadFile(path)
	if readErr != nil {
		//nolint:nilerr // nothing was read, so no repair is even possible; the caller's own "cannot determine" refusal already reports this
		return false, nil
	}
	for _, rawKey := range repairRawKeys(original, topLevel, key) {
		candidate, patchErr := hujsonedit.PatchBytes(original, nil, hujsonedit.PatchRemove(topLevel, rawKey))
		if patchErr != nil || !repairCandidateFixesMap(candidate, topLevel, key) {
			continue
		}
		if writeErr := hujsonedit.WriteRaw(path, candidate); writeErr != nil {
			return false, fmt.Errorf(
				"repairing %s: removing %q would fix it, but writing the repaired registry failed: %w",
				topLevel, rawKey, identityFreePathError(writeErr),
			)
		}
		return true, nil
	}
	return false, nil
}

// topLevelObjectMember returns the object value of document's topLevel
// member. ok is false when the document does not parse, is not an object, or
// topLevel is absent or not object-valued. Repair paths navigate the raw
// document this way because a decode error may have left the canonical view
// only partly built.
func topLevelObjectMember(document []byte, topLevel string) (*hujson.Object, bool) {
	value, err := hujson.Parse(document)
	if err != nil {
		return nil, false
	}
	value.Standardize()
	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, false
	}
	idx, err := registryMemberIndex(object, topLevel)
	if err != nil || idx < 0 {
		return nil, false
	}
	entries, ok := object.Members[idx].Value.Value.(*hujson.Object)
	if !ok {
		return nil, false
	}
	return entries, true
}

// repairRawKeys returns every exact on-file member spelling in topLevel that
// normalizes to key. The map's canonical view is unavailable on this path, so
// its usual canonical-to-raw index cannot be trusted; walking the HuJSON syntax
// tree recovers the spelling PatchRemove must target without decoding the bad
// entry value. Candidates remain in document order so exact duplicate members
// are tested conservatively one at a time by repairCandidateFixesMap.
func repairRawKeys(document []byte, topLevel, key string) []string {
	entries, ok := topLevelObjectMember(document, topLevel)
	if !ok {
		return nil
	}

	rawKeys := make([]string, 0, 1)
	for i := range entries.Members {
		literal, isLiteral := entries.Members[i].Name.Value.(hujson.Literal)
		if !isLiteral {
			continue
		}
		rawKey := literal.String()
		canonicalKey, normalizeErr := normalizeRegistryKey(topLevel, rawKey)
		if normalizeErr == nil && canonicalKey == key {
			rawKeys = append(rawKeys, rawKey)
		}
	}
	return rawKeys
}

func normalizeRegistryKey(topLevel, key string) (string, error) {
	switch topLevel {
	case siteIDsKey:
		return NormalizeSiteID(key)
	case subnetPoolsKey:
		return NormalizeSubnetCIDR(key)
	case serviceAddressesKey:
		return NormalizeServiceAddress(key)
	case udpPortsKey:
		return "", errors.New("nested map has no single canonical key")
	default:
		return "", fmt.Errorf("unknown registry map %q", topLevel)
	}
}

// repairCandidateFixesMap reports whether candidate -- the in-memory result
// of removing key from topLevel -- is worth writing: it must rebuild
// topLevel's view without error AND leave key absent from that view.
//
// Neither condition alone is sufficient. A literal-key match proves nothing
// about which entry actually caused the failure, so a candidate that still
// fails to validate means the removed entry was a healthy sibling and the
// real culprit is elsewhere. And a clean reload proves nothing about whether
// key is still reserved: a second on-file spelling -- an exact duplicate, a
// bare/CIDR alias, or a non-canonical numeric spelling like "01" -- can
// canonicalize to the same key and survive the removal of its twin, since
// hujson resolves duplicate object members first-wins.
func repairCandidateFixesMap(candidate []byte, topLevel, key string) bool {
	reloaded, loadErr := loadBytes(candidate)
	if loadErr != nil || reloaded.mapError(topLevel) != nil {
		return false
	}
	acc, accErr := reloaded.mapAccessorFor(topLevel)
	return accErr == nil && !acc.hasKey(key)
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
		"site_id_count", len(reg.SiteIDs), "subnet_pool_count", len(reg.SubnetPools),
		"service_address_count", len(reg.ServiceAddresses), "udp_port_count", reg.udpPortCount())
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
