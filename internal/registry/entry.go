package registry

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ReservationMetadata is the optional ownership/tracking information a
// registry entry may carry alongside its required site name: who reserved
// it, why, and when. Every field is optional; the zero value means "no
// metadata," which is what keeps a plain site-name-only entry round-tripping
// as a bare JSON string (see Entry.MarshalJSON).
type ReservationMetadata struct {
	Owner       string
	Description string
	Ticket      string
	// ReservedAt is an RFC 3339 timestamp, normalized to UTC. Empty means
	// unset.
	ReservedAt string
}

// isZero reports whether m carries no metadata at all.
func (m ReservationMetadata) isZero() bool {
	return m == ReservationMetadata{}
}

// Entry is one resolved registry reservation: the site name every entry
// carries, plus whatever ReservationMetadata was attached to it. It is the
// map value type for both Registry.SiteIDs and Registry.SubnetPools.
type Entry struct {
	ReservationMetadata

	Site string
}

// entryJSON is Entry's on-file object shape: the same fields, individually
// optional (via omitempty) so a metadata-free entry marshals as it always
// has -- see Entry.MarshalJSON, which chooses between this and a bare string.
type entryJSON struct {
	Site        string `json:"site"`
	Owner       string `json:"owner,omitempty"`
	Description string `json:"description,omitempty"`
	Ticket      string `json:"ticket,omitempty"`
	ReservedAt  string `json:"reservedAt,omitempty"`
}

// MarshalJSON renders e as a bare JSON string when it carries no metadata --
// preserving the registry's original, default entry shape byte-for-byte --
// and as an object otherwise.
func (e Entry) MarshalJSON() ([]byte, error) {
	if e.ReservationMetadata.isZero() {
		return json.Marshal(e.Site)
	}
	return json.Marshal(entryJSON{
		Site:        e.Site,
		Owner:       e.Owner,
		Description: e.Description,
		Ticket:      e.Ticket,
		ReservedAt:  e.ReservedAt,
	})
}

// UnmarshalJSON accepts either a bare JSON string (the default shape, and
// what a "null" or absent value also decodes to, as an empty site name) or an
// object carrying "site" plus optional metadata fields. Any other JSON shape
// (a number, array, or bool) is rejected.
func (e *Entry) UnmarshalJSON(data []byte) error {
	var site string
	if err := json.Unmarshal(data, &site); err == nil {
		*e = Entry{Site: site}
		return nil
	}
	var obj entryJSON
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("registry entry must be a string or an object: %w", err)
	}
	*e = Entry{
		Site:        obj.Site,
		Owner:       obj.Owner,
		Description: obj.Description,
		Ticket:      obj.Ticket,
		ReservedAt:  obj.ReservedAt,
	}
	return nil
}

// rejectControlChars reports an error naming kind if value contains a C0
// control character or DEL -- shared by NormalizeSiteName and the metadata
// text fields below so a reservation's site name, owner, description, and
// ticket are all held to the same no-embedded-control-character rule (it
// prevents, for example, a terminal escape sequence smuggled through a
// registry value from corrupting human-readable output).
// normalizeLabel applies the validation every free-form registry label
// shares: trimmed, non-empty, valid UTF-8, and free of control characters.
// kind names the field in the resulting message. The UTF-8 and empty errors
// deliberately do not echo the value -- an invalid-UTF-8 label must never
// reach an error string, where a sanitizer sweep could not match it.
func normalizeLabel(kind, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", invalidInput(fmt.Errorf("%s must not be empty", kind))
	}
	if !utf8.ValidString(raw) {
		return "", invalidInput(fmt.Errorf("%s must be valid UTF-8", kind))
	}
	if err := rejectControlChars(kind, raw); err != nil {
		return "", invalidInput(err)
	}
	return raw, nil
}

func rejectControlChars(kind, value string) error {
	if i := strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }); i >= 0 {
		return fmt.Errorf("%s %q must not contain control characters", kind, value)
	}
	return nil
}

// normalizeMetadata trims and validates each of m's optional text fields and
// normalizes ReservedAt to a canonical UTC RFC 3339 timestamp. A field left
// blank (or blank after trimming) normalizes to "" -- these fields are
// optional, so unlike NormalizeSiteName a blank value is not an error, it is
// simply absent. Validation errors are wrapped with invalidInput so they are
// classified as HTTP 400 caller-input failures on caller-supplied metadata,
// and unwrapped by storedStateError on stored-state validation.
func normalizeMetadata(m ReservationMetadata) (ReservationMetadata, error) {
	owner, err := normalizeMetadataText("owner", m.Owner)
	if err != nil {
		return ReservationMetadata{}, invalidInput(err)
	}
	description, err := normalizeMetadataText("description", m.Description)
	if err != nil {
		return ReservationMetadata{}, invalidInput(err)
	}
	ticket, err := normalizeMetadataText("ticket", m.Ticket)
	if err != nil {
		return ReservationMetadata{}, invalidInput(err)
	}
	reservedAt, err := normalizeReservedAt(m.ReservedAt)
	if err != nil {
		return ReservationMetadata{}, invalidInput(err)
	}
	return ReservationMetadata{Owner: owner, Description: description, Ticket: ticket, ReservedAt: reservedAt}, nil
}

func normalizeMetadataText(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if err := rejectControlChars(field, value); err != nil {
		return "", err
	}
	return value, nil
}

// normalizeReservedAt validates value as an RFC 3339 timestamp and
// canonicalizes it to UTC. An empty value is left as-is: ReservedAt is
// optional and, when omitted, is auto-stamped by the reserve functions
// rather than defaulted here.
func normalizeReservedAt(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("reserved-at %q is not a valid RFC 3339 timestamp: %w", value, err)
	}
	return t.UTC().Format(time.RFC3339), nil
}

// validateStoredReservationMetadata validates metadata decoded from the
// registry. It is the load-path counterpart to normalizeMetadata: caller input
// is normalized before a reserve, but a hand-edited value reaches
// Entry/ServiceEntry through UnmarshalJSON unchanged. Stored values are not
// trimmed or rewritten here; validation rejects control characters before they
// can reach human output and rejects a malformed ReservedAt before it can make
// the generated HTTP client reject an otherwise successful list or reserve
// response.
func validateStoredReservationMetadata(m ReservationMetadata) error {
	if err := rejectControlChars("owner", m.Owner); err != nil {
		return err
	}
	if err := rejectControlChars("description", m.Description); err != nil {
		return err
	}
	if err := rejectControlChars("ticket", m.Ticket); err != nil {
		return err
	}
	if m.ReservedAt != "" {
		if _, err := time.Parse(time.RFC3339, m.ReservedAt); err != nil {
			return fmt.Errorf("reserved-at %q is not a valid RFC 3339 timestamp: %w", m.ReservedAt, err)
		}
	}
	return nil
}

// mergeMetadataInto overlays requested onto existing: a field requested
// leaves blank keeps existing's value, and a field requested sets replaces
// it. It never stamps ReservedAt -- see stampReservedAt -- so a caller that
// wants to detect "nothing actually changed" can compare its result against
// existing before stamping.
func mergeMetadataInto(existing, requested ReservationMetadata) ReservationMetadata {
	merged := existing
	if requested.Owner != "" {
		merged.Owner = requested.Owner
	}
	if requested.Description != "" {
		merged.Description = requested.Description
	}
	if requested.Ticket != "" {
		merged.Ticket = requested.Ticket
	}
	if requested.ReservedAt != "" {
		merged.ReservedAt = requested.ReservedAt
	}
	return merged
}

// stampReservedAt sets merged.ReservedAt to now when requested is actively
// introducing owner/description/ticket content and merged does not already
// carry a timestamp (from either an explicit --reserved-at or a prior
// reservation). It deliberately does not fire on a bare requested (no
// metadata flags at all) or on a requested that only ever touches
// ReservedAt itself -- see ReserveSiteIDWithLogger's doc comment for why: an
// idempotent repeat of a metadata-free reserve must never start writing a
// timestamp into an entry that already has other metadata but, for whatever
// reason (typically a hand-edited registry file), no ReservedAt of its own.
func stampReservedAt(
	requested ReservationMetadata,
	merged ReservationMetadata,
	now func() time.Time,
) ReservationMetadata {
	introducing := requested.Owner != "" || requested.Description != "" || requested.Ticket != ""
	if introducing && merged.ReservedAt == "" {
		merged.ReservedAt = now().UTC().Format(time.RFC3339)
	}
	return merged
}

// resolveReservationMetadata computes the metadata a reserve call should end
// up storing. It is shared by site/subnet Entry and ServiceEntry resolution so
// the merge/stamp rules cannot drift between registry maps with different
// subject-field names.
func resolveReservationMetadata(
	existing ReservationMetadata,
	exists bool,
	requested ReservationMetadata,
	now func() time.Time,
) ReservationMetadata {
	merged := requested
	if exists {
		merged = mergeMetadataInto(existing, requested)
	}
	return stampReservedAt(requested, merged, now)
}

// resolveReservationEntry computes the Entry a reserve call should end up
// storing for site, given whatever entry (if any) already exists under this
// key and the requested metadata. Shared by ReserveSiteIDWithLogger and
// ReserveSubnetPoolWithLogger so the merge/stamp/no-op decision cannot drift
// between the two maps. unchanged is true only when an existing entry's
// resolved value is identical to what is already stored -- a brand-new key
// is always a change, never "unchanged".
func resolveReservationEntry(
	existingEntry Entry, exists bool, site string, metadata ReservationMetadata, now func() time.Time,
) (Entry, bool) {
	merged := resolveReservationMetadata(existingEntry.ReservationMetadata, exists, metadata, now)
	newEntry := Entry{Site: site, ReservationMetadata: merged}
	return newEntry, exists && newEntry == existingEntry
}
