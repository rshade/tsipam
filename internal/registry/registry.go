// Package registry holds the allocation rules and the backend-agnostic Store
// contract for the tsipam address-space registry. It supports 4via6 site IDs,
// subnet-CIDR pools, per-service IPv4 address reservations, and per-device UDP
// listen-port reservations scoped to a site/firewall boundary. File bytes live
// in hujsonstore; Ledger applies the rules through a Store.
package registry

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// storedStateError strips the InvalidInputError marker from an error if present,
// returning the wrapped inner error. This is used for stored-state validation
// errors (as opposed to caller-supplied input errors) so they are not
// misclassified as HTTP 400 bad-input failures.
func storedStateError(err error) error {
	return StoredStateError(err)
}

// maxSiteID is the highest 4via6 site ID Tailscale's control plane accepts:
// the field is a 16-bit integer.
const maxSiteID = 0xffff

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
