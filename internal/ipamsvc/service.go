// Package ipamsvc implements the goa-generated ipam.Service interface
// (../../gen/ipam) against the registry package. It is the one place that
// translates the registry's domain types into goa's generated payload/result
// types and errors.
package ipamsvc

import (
	"context"
	"errors"
	"path/filepath"

	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

// Service implements ipam.Service against a directory of per-tailnet HuJSON
// registry files, one named "<tailnet>.ipam.hujson" per routing/storage key.
type Service struct {
	// RegistryDir is the directory holding "<tailnet>.ipam.hujson" files.
	RegistryDir string
	// Logger receives identity-free I/O diagnostics. nil is quiet.
	Logger diagnostics.Logger
}

var _ ipam.Service = (*Service)(nil)

func (s *Service) registryPath(tailnet string) string {
	return filepath.Join(s.RegistryDir, tailnet+".ipam.hujson")
}

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
func (s *Service) ListSiteIds(_ context.Context, p *ipam.ListSiteIdsPayload) ([]*ipam.SiteIDReservation, error) {
	entries, err := registry.ListSiteIDsWithLogger(s.registryPath(p.Tailnet), s.Logger)
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
func (s *Service) ReserveSiteID(_ context.Context, p *ipam.ReserveSiteIDPayload) (*ipam.ReserveSiteIDResult, error) {
	id, site, metadata, changed, err := registry.ReserveSiteIDWithLogger(
		s.registryPath(
			p.Tailnet,
		),
		p.ID,
		p.Site,
		metadataFromPayload(p.Owner, p.Description, p.Ticket, p.ReservedAt),
		s.Logger,
	)
	if err != nil {
		var collision *registry.SiteIDCollisionError
		if errors.As(err, &collision) {
			return nil, ipam.MakeCollision(collision)
		}
		return nil, err
	}
	return &ipam.ReserveSiteIDResult{
		ID: id, Site: site, Changed: changed,
		Owner: nonEmptyPtr(metadata.Owner), Description: nonEmptyPtr(metadata.Description),
		Ticket: nonEmptyPtr(metadata.Ticket), ReservedAt: nonEmptyPtr(metadata.ReservedAt),
	}, nil
}

// ReleaseSiteID implements ipam.Service.
func (s *Service) ReleaseSiteID(_ context.Context, p *ipam.ReleaseSiteIDPayload) (*ipam.ReleaseSiteIDResult, error) {
	id, changed, err := registry.ReleaseSiteIDWithLogger(s.registryPath(p.Tailnet), p.ID, s.Logger)
	if err != nil {
		return nil, err
	}
	return &ipam.ReleaseSiteIDResult{ID: id, Changed: changed}, nil
}

// ListSubnetPools implements ipam.Service.
func (s *Service) ListSubnetPools(
	_ context.Context,
	p *ipam.ListSubnetPoolsPayload,
) ([]*ipam.SubnetPoolReservation, error) {
	entries, err := registry.ListSubnetPoolsWithLogger(s.registryPath(p.Tailnet), s.Logger)
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
	_ context.Context,
	p *ipam.ReserveSubnetPoolPayload,
) (*ipam.ReserveSubnetPoolResult, error) {
	cidr, site, metadata, changed, err := registry.ReserveSubnetPoolWithLogger(
		s.registryPath(p.Tailnet), p.CIDR, p.Site,
		metadataFromPayload(p.Owner, p.Description, p.Ticket, p.ReservedAt), s.Logger,
	)
	if err != nil {
		var collision *registry.SubnetPoolCollisionError
		if errors.As(err, &collision) {
			return nil, ipam.MakeCollision(collision)
		}
		return nil, err
	}
	return &ipam.ReserveSubnetPoolResult{
		CIDR: cidr, Site: site, Changed: changed,
		Owner: nonEmptyPtr(metadata.Owner), Description: nonEmptyPtr(metadata.Description),
		Ticket: nonEmptyPtr(metadata.Ticket), ReservedAt: nonEmptyPtr(metadata.ReservedAt),
	}, nil
}

// ReleaseSubnetPool implements ipam.Service.
func (s *Service) ReleaseSubnetPool(
	_ context.Context,
	p *ipam.ReleaseSubnetPoolPayload,
) (*ipam.ReleaseSubnetPoolResult, error) {
	cidr, changed, err := registry.ReleaseSubnetPoolWithLogger(s.registryPath(p.Tailnet), p.CIDR, s.Logger)
	if err != nil {
		return nil, err
	}
	return &ipam.ReleaseSubnetPoolResult{CIDR: cidr, Changed: changed}, nil
}
