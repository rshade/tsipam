// Package registry provides comment-preserving, HuJSON-backed read/write
// access to the tail-ipam address-space registry — the declarative source of
// truth for reserved Tailscale address-space allocations. It starts scoped to
// 4via6 site IDs only; subnet-CIDR pools and per-service /32 allocation are
// separately scoped future work (see ROADMAP.md).
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
)

// siteIDsKey is the registry's top-level key for the site-ID map.
const siteIDsKey = "siteIDs"

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
// Load: SiteIDs maps a canonical 4via6 site ID (see NormalizeSiteID) to the
// name of the site reserved for it. rawKeys records each entry's original
// on-file key spelling (e.g. "01" for canonical "1"). hujson's patch matching
// is exact and case/format-sensitive, so an edit that must address an
// EXISTING entry (ReleaseSiteID) has to target the key spelling the file
// actually uses, while an edit that creates a NEW entry has no existing
// spelling to preserve and always uses the canonical form.
//
// siteIDsPresent records whether the file's top-level "siteIDs" member
// already exists as an object node (empty or not) -- as opposed to being
// entirely absent, or present but null. It is not derivable from SiteIDs
// being non-nil: an existing-but-empty object and a genuinely absent member
// both decode to a nil SiteIDs map, yet ReserveSiteID must edit them with
// different patch strategies (see its doc comment).
type Registry struct {
	SiteIDs        map[string]string
	rawKeys        map[string]string
	siteIDsPresent bool
}

// SiteIDEntry is one resolved site-ID reservation, returned by ListSiteIDs.
type SiteIDEntry struct {
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

// NormalizeSiteName validates a site name: non-empty after trimming.
func NormalizeSiteName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("site name must not be empty")
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
// spelling. Any other top-level key loads and round-trips untouched: the
// registry is documented to grow additional top-level keys (subnetPools,
// per-service /32 allocations) in future versions, so unknown fields are not
// rejected wholesale. Within the "siteIDs" object itself, an exact-duplicate
// entry key is rejected for the same first-wins/last-wins reason, and every
// entry key is canonicalized through NormalizeSiteID: two raw keys that
// canonicalize to the same ID (e.g. "01" and "1") name both spellings in the
// returned error, since Load cannot decide which is right. An entry whose
// value is null or "" is rejected: a reservation must have a genuine
// non-empty site name.
func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// The UTF-8 byte-order mark (U+FEFF). hujson.Parse already fails on a
	// BOM-prefixed document with an "invalid character" error, but that
	// message gives no indication of the cause; checking for it directly
	// lets Load tell the operator what is actually wrong instead of
	// silently stripping it or leaving them to guess.
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return nil, fmt.Errorf(
			"decoding %s: file starts with a UTF-8 byte-order mark; re-save it as UTF-8 without a BOM", path)
	}

	value, err := hujson.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	value.Standardize()

	object, ok := value.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("decoding %s: top-level document must be a JSON object", path)
	}

	idx, err := siteIDsMemberIndex(object)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}

	var entries map[string]string
	present := false
	if idx >= 0 {
		member := object.Members[idx].Value
		if entryObject, isObject := member.Value.(*hujson.Object); isObject {
			present = true
			if dupErr := duplicateEntryKey(entryObject); dupErr != nil {
				return nil, fmt.Errorf("decoding %s: %w", path, dupErr)
			}
		}
		if unmarshalErr := json.Unmarshal(member.Pack(), &entries); unmarshalErr != nil {
			return nil, fmt.Errorf("decoding %s: %q: %w", path, siteIDsKey, unmarshalErr)
		}
	}

	reg, err := canonicalize(entries)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	reg.siteIDsPresent = present
	return reg, nil
}

// siteIDsMemberIndex locates the document's top-level "siteIDs" member and
// returns its index in object.Members, or -1 if no member's name
// case-insensitively matches siteIDsKey (the block is entirely absent).
//
// An error means the match is ambiguous: either the single matching member's
// exact spelling differs from siteIDsKey (a near-miss such as "siteids"), or
// more than one member matches case-insensitively -- which also covers two
// members with the identical exact spelling, since hujson does not merge
// duplicate object members the way encoding/json's map decode would.
func siteIDsMemberIndex(object *hujson.Object) (int, error) {
	var spellings []string
	idx := -1
	for i := range object.Members {
		literal, isLiteral := object.Members[i].Name.Value.(hujson.Literal)
		if !isLiteral {
			continue
		}
		name := literal.String()
		if !strings.EqualFold(name, siteIDsKey) {
			continue
		}
		spellings = append(spellings, name)
		idx = i
	}
	switch len(spellings) {
	case 0:
		return -1, nil
	case 1:
		if spellings[0] != siteIDsKey {
			return -1, fmt.Errorf("top-level key %q must be spelled %q", spellings[0], siteIDsKey)
		}
		return idx, nil
	default:
		quoted := make([]string, len(spellings))
		for i, spelling := range spellings {
			quoted[i] = strconv.Quote(spelling)
		}
		return -1, fmt.Errorf("multiple top-level keys match %q: %s", siteIDsKey, strings.Join(quoted, ", "))
	}
}

// duplicateEntryKey rejects an exact-duplicate raw key within the siteIDs
// object. [json.Unmarshal] resolves a repeated key last-wins, silently
// collapsing it to one entry before canonicalize ever sees it, while
// hujson's patch matching is first-wins -- the same first-wins/last-wins
// mismatch tail-acl's checkDuplicateMembers guards against for the ACL
// policy file.
func duplicateEntryKey(object *hujson.Object) error {
	seen := make(map[string]struct{}, len(object.Members))
	for i := range object.Members {
		literal, isLiteral := object.Members[i].Name.Value.(hujson.Literal)
		if !isLiteral {
			continue
		}
		name := literal.String()
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate %s entry key %q", siteIDsKey, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// canonicalize builds the canonical view of a raw siteIDs map: SiteIDs keyed
// by NormalizeSiteID's canonical spelling, and rawKeys recording each entry's
// original on-file key.
func canonicalize(raw map[string]string) (*Registry, error) {
	if len(raw) == 0 {
		return &Registry{}, nil
	}

	rawIDs := make([]string, 0, len(raw))
	for rawID := range raw {
		rawIDs = append(rawIDs, rawID)
	}
	sort.Strings(rawIDs)

	siteIDs := make(map[string]string, len(raw))
	rawKeys := make(map[string]string, len(raw))
	for _, rawID := range rawIDs {
		canonicalID, err := NormalizeSiteID(rawID)
		if err != nil {
			return nil, fmt.Errorf("invalid site ID key %q: %w", rawID, err)
		}
		if firstRaw, ok := rawKeys[canonicalID]; ok {
			return nil, fmt.Errorf("site ID %s is defined twice, as %q and %q", canonicalID, firstRaw, rawID)
		}
		site := raw[rawID]
		if site == "" {
			return nil, fmt.Errorf("site ID %s (key %q) has an empty or null site name", canonicalID, rawID)
		}
		siteIDs[canonicalID] = site
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
func withRegistryLock(path string, fn func() error) error {
	lockPath := path + lockSuffix
	deadline := time.Now().Add(lockAcquireTimeout)
	for {
		lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if closeErr := lockFile.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return fmt.Errorf("acquiring lock %s: %w", lockPath, closeErr)
			}
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("acquiring lock %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"acquiring lock %s: timed out after %s waiting for a concurrent tail-ipam to finish",
				lockPath, lockAcquireTimeout)
		}
		time.Sleep(lockRetryInterval)
	}
	defer func() { _ = os.Remove(lockPath) }()
	return fn()
}

// loadOrCreate loads the registry at path, creating it as an empty "{}\n"
// document first if it is absent. Creation uses O_CREATE|O_EXCL so a second
// caller that also observed the file as absent cannot silently overwrite the
// first's create: it re-Loads instead, picking up whatever is now on disk.
// Callers must run this inside withRegistryLock.
func loadOrCreate(path string) (*Registry, error) {
	reg, err := Load(path)
	switch {
	case err == nil:
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
			return nil, fmt.Errorf("creating %s: %w", path, writeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("creating %s: %w", path, closeErr)
		}
		return &Registry{}, nil
	case errors.Is(createErr, os.ErrExist):
		// Another holder created it between our Load and this attempt; pick
		// up whatever is now on disk rather than treating this as an error.
		return Load(path)
	default:
		return nil, fmt.Errorf("creating %s: %w", path, createErr)
	}
}

// ReserveSiteID reserves id for site in the registry at path, creating the
// file (as "{}\n") if it does not exist yet. Reserving the same id+site pair
// again is a no-op — changed reports false, err is nil — so repeating the
// command is idempotent. Reserving an id already held by a different site
// fails with *SiteIDCollisionError rather than overwriting it. The whole
// read-decide-write span runs under an exclusive file lock (see
// withRegistryLock), so two concurrent reservations against a fresh registry
// cannot both win a whole-object create and silently drop one another.
//
// On success it returns the canonical id and site name actually written
// (e.g. "007" normalizes to "7"), not the raw arguments, so a caller does not
// need to re-derive what NormalizeSiteID/NormalizeSiteName already computed.
func ReserveSiteID(path, id, site string) (string, string, bool, error) {
	id, err := NormalizeSiteID(id)
	if err != nil {
		return "", "", false, err
	}
	site, err = NormalizeSiteName(site)
	if err != nil {
		return "", "", false, err
	}

	changed := false
	lockErr := withRegistryLock(path, func() error {
		reg, loadErr := loadOrCreate(path)
		if loadErr != nil {
			return loadErr
		}

		if existing, ok := reg.SiteIDs[id]; ok {
			if existing == site {
				return nil
			}
			return &SiteIDCollisionError{ID: id, Existing: existing, Wanted: site}
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
			op = hujsonedit.PatchAdd(map[string]string{id: site}, siteIDsKey)
		} else {
			op = hujsonedit.PatchAdd(site, siteIDsKey, id)
		}
		if patchErr := hujsonedit.WritePatch(path, nil, op); patchErr != nil {
			return fmt.Errorf("reserving site ID %s: %w", id, patchErr)
		}
		changed = true
		return nil
	})
	if lockErr != nil {
		return "", "", false, lockErr
	}
	return id, site, changed, nil
}

// ReleaseSiteID removes id from the registry at path. Releasing an id that is
// not present is a no-op — changed reports false, err is nil — matching
// tail-acl's RemoveDefinition behavior for an absent name. A missing registry
// file is an error: release has nothing to remove from and isn't the command
// that creates a registry. The read-decide-write span runs under the same
// exclusive file lock as ReserveSiteID.
//
// On success it returns the canonical id released, not the raw argument.
func ReleaseSiteID(path, id string) (string, bool, error) {
	id, err := NormalizeSiteID(id)
	if err != nil {
		return "", false, err
	}

	changed := false
	lockErr := withRegistryLock(path, func() error {
		reg, loadErr := Load(path)
		if loadErr != nil {
			return loadErr
		}
		if _, ok := reg.SiteIDs[id]; !ok {
			return nil
		}
		rawKey := reg.rawKeys[id]
		if rawKey == "" {
			rawKey = id
		}
		if patchErr := hujsonedit.WritePatch(path, nil, hujsonedit.PatchRemove(siteIDsKey, rawKey)); patchErr != nil {
			return fmt.Errorf("releasing site ID %s: %w", id, patchErr)
		}
		changed = true
		return nil
	})
	if lockErr != nil {
		return "", false, lockErr
	}
	return id, changed, nil
}

// ListSiteIDs returns the registry's site-ID entries at path, sorted
// numerically by ID. A missing file is an error, for the same reason as
// ReleaseSiteID. The sort is stable with a total-order comparator (numeric
// when both IDs parse and differ, otherwise a plain string compare) so output
// order is deterministic regardless of Go's randomized map iteration.
func ListSiteIDs(path string) ([]SiteIDEntry, error) {
	reg, err := Load(path)
	if err != nil {
		return nil, err
	}
	entries := make([]SiteIDEntry, 0, len(reg.SiteIDs))
	for id, site := range reg.SiteIDs {
		entries = append(entries, SiteIDEntry{ID: id, Site: site})
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
