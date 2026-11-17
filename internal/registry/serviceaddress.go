package registry

import (
	"fmt"
)

// ServiceAddressEntry is one resolved service-address reservation returned by
// ListServiceAddresses.
type ServiceAddressEntry struct {
	ReservationMetadata

	Address string
	Service string
}

// ServiceAddressCollisionError reports an address already owned by a
// different canonical service identity.
type ServiceAddressCollisionError struct {
	Address  string
	Existing string
	Wanted   string
}

func (e *ServiceAddressCollisionError) Error() string {
	return fmt.Sprintf(
		"service address %s is already reserved for %q, not %q",
		e.Address,
		e.Existing,
		e.Wanted,
	)
}
