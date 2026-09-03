package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

const serviceSelectorPrefix = "svc:"

// InvalidInputError marks a caller-supplied service reservation value as
// invalid. The service adapter uses this type to distinguish HTTP 400 input
// failures from registry I/O or malformed stored-state failures.
type InvalidInputError struct {
	Err error
}

func (e *InvalidInputError) Error() string {
	if e == nil || e.Err == nil {
		return "invalid input"
	}
	return e.Err.Error()
}

func (e *InvalidInputError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func invalidInput(err error) error {
	return &InvalidInputError{Err: err}
}

// NormalizeServiceAddress accepts a bare IPv4 address or an exact IPv4 /32
// prefix and returns its canonical bare-address spelling. Wider prefixes are
// rejected instead of being masked; native and mapped IPv6 spellings are not
// part of this IPv4-only allocation primitive.
func NormalizeServiceAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if parsed, err := netip.ParseAddr(address); err == nil {
		if parsed.Is4() && !parsed.Is4In6() {
			return parsed.String(), nil
		}
		return "", invalidServiceAddress(address, nil)
	}

	prefix, err := netip.ParsePrefix(address)
	if err != nil {
		return "", invalidServiceAddress(address, err)
	}
	parsed := prefix.Addr()
	if !parsed.Is4() || parsed.Is4In6() || prefix.Bits() != netip.IPv4Unspecified().BitLen() {
		return "", invalidServiceAddress(address, nil)
	}
	return parsed.String(), nil
}

func invalidServiceAddress(address string, cause error) error {
	base := fmt.Errorf("service address %q must be an IPv4 host or exact /32", address)
	if cause != nil {
		base = fmt.Errorf("%w: %w", base, cause)
	}
	return invalidInput(base)
}

// NormalizeServiceName accepts the bare service name or its svc: policy
// selector spelling and returns exactly one svc: prefix. It intentionally
// validates only the invariants this registry owns; live control-plane name
// grammar and existence checks belong to later plan/check work.
func NormalizeServiceName(service string) (string, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		return "", invalidInput(errors.New("service name must not be empty"))
	}
	if err := rejectControlChars("service name", service); err != nil {
		return "", invalidInput(err)
	}
	if name, ok := strings.CutPrefix(service, serviceSelectorPrefix); ok {
		service = strings.TrimSpace(name)
		if service == "" {
			return "", invalidInput(fmt.Errorf("service name must not be %q", serviceSelectorPrefix))
		}
		if strings.HasPrefix(service, serviceSelectorPrefix) {
			return "", invalidInput(fmt.Errorf(
				"service name must contain exactly one %q prefix", serviceSelectorPrefix,
			))
		}
	}
	return serviceSelectorPrefix + service, nil
}

// ServiceEntry is one resolved per-service address reservation. It uses a
// service field in its object form rather than overloading Entry.Site.
type ServiceEntry struct {
	ReservationMetadata

	Service string
}

type serviceEntryJSON struct {
	Service     string `json:"service"`
	Owner       string `json:"owner,omitempty"`
	Description string `json:"description,omitempty"`
	Ticket      string `json:"ticket,omitempty"`
	ReservedAt  string `json:"reservedAt,omitempty"`
}

// MarshalJSON keeps metadata-free entries concise and uses the truthful
// service object field when metadata is present. Re-running the service name
// through NormalizeServiceName is load-bearing here (unlike Entry.MarshalJSON,
// which does no validation): it canonicalizes a bare, unprefixed Service value
// to its svc:-prefixed spelling on write. Every production caller already
// stores a canonicalized Service (canonicalizeServiceAddresses on the load
// path, ReserveServiceAddressWithLogger on the write path), so a failure here
// would mean stored-state corruption rather than caller input -- hence
// storedStateError rather than returning the *InvalidInputError verbatim.
func (e ServiceEntry) MarshalJSON() ([]byte, error) {
	service, err := NormalizeServiceName(e.Service)
	if err != nil {
		return nil, storedStateError(err)
	}
	if e.ReservationMetadata.isZero() {
		return json.Marshal(service)
	}
	return json.Marshal(serviceEntryJSON{
		Service: service, Owner: e.Owner, Description: e.Description,
		Ticket: e.Ticket, ReservedAt: e.ReservedAt,
	})
}

// UnmarshalJSON accepts the concise string or metadata-bearing object form,
// preserving the raw service name without validation (validation happens in
// canonicalizeServiceAddresses so stored-state errors are properly distinguished
// from caller input).
func (e *ServiceEntry) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return errors.New("service registry entry must be a string or an object: unexpected end of JSON input")
	}

	switch data[0] {
	case '"':
		var service string
		if err := json.Unmarshal(data, &service); err != nil {
			return fmt.Errorf("service registry entry must be a string or an object: %w", err)
		}
		*e = ServiceEntry{Service: service}
		return nil

	case '{':
		var obj serviceEntryJSON
		if err := json.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("service registry entry must be a string or an object: %w", err)
		}
		*e = ServiceEntry{
			Service: obj.Service,
			ReservationMetadata: ReservationMetadata{
				Owner: obj.Owner, Description: obj.Description, Ticket: obj.Ticket, ReservedAt: obj.ReservedAt,
			},
		}
		return nil

	default:
		return fmt.Errorf("service registry entry must be a string or an object: encountered %q", data[0])
	}
}

func resolveServiceEntry(
	existingEntry ServiceEntry,
	exists bool,
	service string,
	metadata ReservationMetadata,
	now func() time.Time,
) (ServiceEntry, bool) {
	merged := resolveReservationMetadata(existingEntry.ReservationMetadata, exists, metadata, now)
	newEntry := ServiceEntry{Service: service, ReservationMetadata: merged}
	return newEntry, exists && newEntry == existingEntry
}
