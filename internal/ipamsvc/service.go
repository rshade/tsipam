// Package ipamsvc implements the goa-generated ipam.Service interface
// (../../gen/ipam) against the registry package. It is the one place that
// translates the registry's domain types into goa's generated payload/result
// types and errors.
package ipamsvc

import (
	"context"
	"errors"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/registry"
)

// Service implements ipam.Service against a registry ledger. The HuJSON
// backend stores one "<tailnet>.ipam.hujson" file per routing key.
type Service struct {
	// Ledger is the registry the service reads and writes.
	Ledger *registry.Ledger
	// Logger receives identity-free I/O diagnostics. nil is quiet.
	Logger diagnostics.Logger
	// CollectLive reads a complete live allocation snapshot for the requested tailnet.
	// nil disables plan while retaining the registry-only methods.
	CollectLive func(context.Context, string, diagnostics.Logger) (devices.Snapshot, error)
}

var _ ipam.Service = (*Service)(nil)

// nonEmptyPtr returns nil for an empty string, and a pointer to value
// otherwise -- goa's optional attributes are *string, but registry.Entry's
// metadata fields use "" as their absent sentinel (see ReservationMetadata's
// doc comment), so every result crossing that boundary goes through here.
func nonEmptyPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// stringOrEmpty is nonEmptyPtr's inverse: a nil optional attribute reads back
// as "", registry's own absent sentinel.
func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// metadataFromPayload builds a registry.ReservationMetadata from a reserve
// payload's four optional attributes.
func metadataFromPayload(owner, description, ticket, reservedAt *string) registry.ReservationMetadata {
	return registry.ReservationMetadata{
		Owner:       stringOrEmpty(owner),
		Description: stringOrEmpty(description),
		Ticket:      stringOrEmpty(ticket),
		ReservedAt:  stringOrEmpty(reservedAt),
	}
}

// ListSiteIds implements ipam.Service.
//
//nolint:revive // must match the generated ipam.Service interface's exact method name (ListSiteIds, not ListSiteIDs).
func (s *Service) ListSiteIds(ctx context.Context, p *ipam.ListSiteIdsPayload) ([]*ipam.SiteIDReservation, error) {
	entries, err := s.Ledger.ListSiteIDs(ctx, p.Tailnet)
	if err != nil {
		return nil, err
	}
	out := make([]*ipam.SiteIDReservation, len(entries))
	for i, entry := range entries {
		out[i] = &ipam.SiteIDReservation{
			ID: entry.ID, Site: entry.Site,
			Owner: nonEmptyPtr(entry.Owner), Description: nonEmptyPtr(entry.Description),
			Ticket: nonEmptyPtr(entry.Ticket), ReservedAt: nonEmptyPtr(entry.ReservedAt),
		}
	}
	return out, nil
}

// ReserveSiteID implements ipam.Service.
func (s *Service) ReserveSiteID(ctx context.Context, p *ipam.ReserveSiteIDPayload) (*ipam.ReserveSiteIDResult, error) {
	id, site, metadata, changed, err := s.Ledger.ReserveSiteID(
		ctx, p.Tailnet, p.ID, p.Site,
		metadataFromPayload(p.Owner, p.Description, p.Ticket, p.ReservedAt),
	)
	if err != nil {
		return nil, translateSiteIDError(err)
	}
	return &ipam.ReserveSiteIDResult{
		ID: id, Site: site, Changed: changed,
		Owner: nonEmptyPtr(metadata.Owner), Description: nonEmptyPtr(metadata.Description),
		Ticket: nonEmptyPtr(metadata.Ticket), ReservedAt: nonEmptyPtr(metadata.ReservedAt),
	}, nil
}

// ReleaseSiteID implements ipam.Service.
func (s *Service) ReleaseSiteID(ctx context.Context, p *ipam.ReleaseSiteIDPayload) (*ipam.ReleaseSiteIDResult, error) {
	id, changed, err := s.Ledger.ReleaseSiteID(ctx, p.Tailnet, p.ID)
	if err != nil {
		return nil, translateSiteIDError(err)
	}
	return &ipam.ReleaseSiteIDResult{ID: id, Changed: changed}, nil
}

// ListSubnetPools implements ipam.Service.
func (s *Service) ListSubnetPools(
	ctx context.Context,
	p *ipam.ListSubnetPoolsPayload,
) ([]*ipam.SubnetPoolReservation, error) {
	entries, err := s.Ledger.ListSubnetPools(ctx, p.Tailnet)
	if err != nil {
		return nil, err
	}
	out := make([]*ipam.SubnetPoolReservation, len(entries))
	for i, entry := range entries {
		out[i] = &ipam.SubnetPoolReservation{
			CIDR: entry.CIDR, Site: entry.Site,
			Owner: nonEmptyPtr(entry.Owner), Description: nonEmptyPtr(entry.Description),
			Ticket: nonEmptyPtr(entry.Ticket), ReservedAt: nonEmptyPtr(entry.ReservedAt),
		}
	}
	return out, nil
}

// ReserveSubnetPool implements ipam.Service.
func (s *Service) ReserveSubnetPool(
	ctx context.Context,
	p *ipam.ReserveSubnetPoolPayload,
) (*ipam.ReserveSubnetPoolResult, error) {
	cidr, site, metadata, changed, err := s.Ledger.ReserveSubnetPool(
		ctx, p.Tailnet, p.CIDR, p.Site,
		metadataFromPayload(p.Owner, p.Description, p.Ticket, p.ReservedAt),
	)
	if err != nil {
		return nil, translateSubnetPoolError(err)
	}
	return &ipam.ReserveSubnetPoolResult{
		CIDR: cidr, Site: site, Changed: changed,
		Owner: nonEmptyPtr(metadata.Owner), Description: nonEmptyPtr(metadata.Description),
		Ticket: nonEmptyPtr(metadata.Ticket), ReservedAt: nonEmptyPtr(metadata.ReservedAt),
	}, nil
}

// AllocateSubnetPool implements ipam.Service.
func (s *Service) AllocateSubnetPool(
	ctx context.Context,
	p *ipam.AllocateSubnetPoolPayload,
) (*ipam.AllocateSubnetPoolResult, error) {
	cidr, site, metadata, changed, err := s.Ledger.AllocateSubnetPool(
		ctx, p.Tailnet, p.Pool, p.Size, p.Site, p.LiveTaken,
		metadataFromPayload(p.Owner, p.Description, p.Ticket, p.ReservedAt),
	)
	if err != nil {
		return nil, translateSubnetPoolError(err)
	}
	return &ipam.AllocateSubnetPoolResult{
		CIDR: cidr, Site: site, Changed: changed,
		Owner: nonEmptyPtr(metadata.Owner), Description: nonEmptyPtr(metadata.Description),
		Ticket: nonEmptyPtr(metadata.Ticket), ReservedAt: nonEmptyPtr(metadata.ReservedAt),
	}, nil
}

// ReleaseSubnetPool implements ipam.Service.
func (s *Service) ReleaseSubnetPool(
	ctx context.Context,
	p *ipam.ReleaseSubnetPoolPayload,
) (*ipam.ReleaseSubnetPoolResult, error) {
	cidr, changed, err := s.Ledger.ReleaseSubnetPool(ctx, p.Tailnet, p.CIDR)
	if err != nil {
		return nil, translateSubnetPoolError(err)
	}
	return &ipam.ReleaseSubnetPoolResult{CIDR: cidr, Changed: changed}, nil
}

// ListServiceAddresses implements ipam.Service.
func (s *Service) ListServiceAddresses(
	ctx context.Context,
	p *ipam.ListServiceAddressesPayload,
) ([]*ipam.ServiceAddressReservation, error) {
	entries, err := s.Ledger.ListServiceAddresses(ctx, p.Tailnet)
	if err != nil {
		return nil, err
	}
	out := make([]*ipam.ServiceAddressReservation, len(entries))
	for i, entry := range entries {
		out[i] = &ipam.ServiceAddressReservation{
			Address: entry.Address, Service: entry.Service,
			Owner: nonEmptyPtr(entry.Owner), Description: nonEmptyPtr(entry.Description),
			Ticket: nonEmptyPtr(entry.Ticket), ReservedAt: nonEmptyPtr(entry.ReservedAt),
		}
	}
	return out, nil
}

// ReserveServiceAddress implements ipam.Service.
func (s *Service) ReserveServiceAddress(
	ctx context.Context,
	p *ipam.ReserveServiceAddressPayload,
) (*ipam.ReserveServiceAddressResult, error) {
	address, service, metadata, changed, err := s.Ledger.ReserveServiceAddress(
		ctx, p.Tailnet, p.Address, p.Service,
		metadataFromPayload(p.Owner, p.Description, p.Ticket, p.ReservedAt),
	)
	if err != nil {
		return nil, translateServiceAddressError(err)
	}
	return &ipam.ReserveServiceAddressResult{
		Address: address, Service: service, Changed: changed,
		Owner: nonEmptyPtr(metadata.Owner), Description: nonEmptyPtr(metadata.Description),
		Ticket: nonEmptyPtr(metadata.Ticket), ReservedAt: nonEmptyPtr(metadata.ReservedAt),
	}, nil
}

// ReleaseServiceAddress implements ipam.Service.
func (s *Service) ReleaseServiceAddress(
	ctx context.Context,
	p *ipam.ReleaseServiceAddressPayload,
) (*ipam.ReleaseServiceAddressResult, error) {
	address, changed, err := s.Ledger.ReleaseServiceAddress(ctx, p.Tailnet, p.Address)
	if err != nil {
		return nil, translateServiceAddressError(err)
	}
	return &ipam.ReleaseServiceAddressResult{Address: address, Changed: changed}, nil
}

// ListUDPPorts implements ipam.Service.
func (s *Service) ListUDPPorts(
	ctx context.Context,
	p *ipam.ListUDPPortsPayload,
) ([]*ipam.UDPPortReservation, error) {
	site := ""
	if p.Site != nil {
		var err error
		site, err = registry.NormalizeSiteName(*p.Site)
		if err != nil {
			return nil, translateUDPPortError(err)
		}
	}
	entries, err := s.Ledger.ListUDPPorts(ctx, p.Tailnet, site)
	if err != nil {
		return nil, translateUDPPortError(err)
	}
	out := make([]*ipam.UDPPortReservation, len(entries))
	for i, entry := range entries {
		out[i] = &ipam.UDPPortReservation{Site: entry.Site, Port: entry.Port, Device: entry.Device}
	}
	return out, nil
}

// ReserveUDPPort implements ipam.Service.
func (s *Service) ReserveUDPPort(
	ctx context.Context,
	p *ipam.ReserveUDPPortPayload,
) (*ipam.ReserveUDPPortResult, error) {
	site, port, device, changed, err := s.Ledger.ReserveUDPPort(ctx, p.Tailnet, p.Site, p.Port, p.Device)
	if err != nil {
		return nil, translateUDPPortError(err)
	}
	return &ipam.ReserveUDPPortResult{
		Site: site, Port: port, Device: device, Changed: changed,
	}, nil
}

// ReleaseUDPPort implements ipam.Service.
func (s *Service) ReleaseUDPPort(
	ctx context.Context,
	p *ipam.ReleaseUDPPortPayload,
) (*ipam.ReleaseUDPPortResult, error) {
	site, port, changed, err := s.Ledger.ReleaseUDPPort(ctx, p.Tailnet, p.Site, p.Port)
	if err != nil {
		return nil, translateUDPPortError(err)
	}
	return &ipam.ReleaseUDPPortResult{Site: site, Port: port, Changed: changed}, nil
}

func translateUDPPortError(err error) error {
	if collision, ok := errors.AsType[*registry.UDPPortCollisionError](err); ok {
		return ipam.MakeCollision(collision)
	}
	if _, ok := errors.AsType[*registry.InvalidInputError](err); ok {
		return ipam.MakeInvalidInput(err)
	}
	return err
}

func translateServiceAddressError(err error) error {
	if collision, ok := errors.AsType[*registry.ServiceAddressCollisionError](err); ok {
		return ipam.MakeCollision(collision)
	}
	if _, ok := errors.AsType[*registry.InvalidInputError](err); ok {
		return ipam.MakeInvalidInput(err)
	}
	return err
}

func translateSiteIDError(err error) error {
	if collision, ok := errors.AsType[*registry.SiteIDCollisionError](err); ok {
		return ipam.MakeCollision(collision)
	}
	if _, ok := errors.AsType[*registry.InvalidInputError](err); ok {
		return ipam.MakeInvalidInput(err)
	}
	return err
}

func translateSubnetPoolError(err error) error {
	if collision, ok := errors.AsType[*registry.SubnetPoolCollisionError](err); ok {
		return ipam.MakeCollision(collision)
	}
	if exhausted, ok := errors.AsType[*registry.SubnetPoolExhaustedError](err); ok {
		return ipam.MakeExhausted(exhausted)
	}
	if _, ok := errors.AsType[*registry.InvalidInputError](err); ok {
		return ipam.MakeInvalidInput(err)
	}
	return err
}
