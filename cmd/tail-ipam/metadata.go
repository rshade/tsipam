package main

import "github.com/spf13/cobra"

// reservationMetadataFlags holds the four optional reservation-metadata
// flags shared by `site-id reserve` and `subnet reserve`. A blank field means
// "not provided" -- the same absent sentinel registry.ReservationMetadata
// itself uses -- so these values pass straight through to the goa payload's
// optional attributes without any cobra Changed() tracking.
type reservationMetadataFlags struct {
	owner       string
	description string
	ticket      string
	reservedAt  string
}

// addMetadataFlags registers the four reservation-metadata flags on cmd.
func addMetadataFlags(cmd *cobra.Command, flags *reservationMetadataFlags) {
	cmd.Flags().StringVar(&flags.owner, "owner", "", "operator or team that owns this reservation")
	cmd.Flags().StringVar(&flags.description, "description", "",
		"free-text description of why this address space is reserved")
	cmd.Flags().StringVar(&flags.ticket, "ticket", "", "tracking ticket or issue reference for this reservation")
	cmd.Flags().StringVar(&flags.reservedAt, "reserved-at", "",
		"RFC3339 timestamp this reservation was made (default: auto-stamped when other metadata is set)")
}

// asPointers converts f to the four *string optional attributes a goa
// reserve payload expects: nil for a blank (unprovided) field.
func (f reservationMetadataFlags) asPointers() (*string, *string, *string, *string) {
	return nonEmptyStringPtr(f.owner), nonEmptyStringPtr(f.description),
		nonEmptyStringPtr(f.ticket), nonEmptyStringPtr(f.reservedAt)
}

// nonEmptyStringPtr returns nil for an empty string, and a pointer to value
// otherwise. It mirrors ipamsvc's own nonEmptyPtr; kept separate rather than
// exported and shared, since each is a private, single-use boundary
// conversion in its own package.
func nonEmptyStringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// stringOrEmpty is nonEmptyStringPtr's inverse: a nil optional attribute
// reads back as "".
func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// metadataOutputFrom builds a reservationMetadataOutput from a reserve
// result's four optional attributes.
func metadataOutputFrom(owner, description, ticket, reservedAt *string) reservationMetadataOutput {
	return reservationMetadataOutput{
		Owner:       stringOrEmpty(owner),
		Description: stringOrEmpty(description),
		Ticket:      stringOrEmpty(ticket),
		ReservedAt:  stringOrEmpty(reservedAt),
	}
}
