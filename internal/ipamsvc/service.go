// Package ipamsvc implements the goa-generated ipam.Service interface
// (../../gen/ipam) against the registry package. It is the one place that
// translates the registry's domain types into goa's generated payload/result
// types and errors.
package ipamsvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/devices"
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
	// CollectLive reads a complete live allocation snapshot for the requested tailnet.
	// nil disables plan while retaining the registry-only methods.
	CollectLive func(context.Context, string, diagnostics.Logger) (devices.Snapshot, error)
}

var _ ipam.Service = (*Service)(nil)

func (s *Service) registryPath(tailnet string) string {
	return filepath.Join(s.RegistryDir, tailnet+".ipam.hujson")
}

// registryError restores the lookup context registry.Load withholds on
// purpose (registry.identityFreePathError drops the file path because the
// filename embeds the tailnet key): the alias and directory are reported
// separately, so the CLI's sanitizer can sweep the alias while the directory,
// which is the operator's own --registry-dir value, stays readable. A missing
// directory and a missing file are told apart because they call for
// different fixes.
func (s *Service) registryError(tailnet string, err error) error {
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, statErr := os.Stat(s.RegistryDir); errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("registry directory %q does not exist: %w", s.RegistryDir, err)
	}
	return fmt.Errorf("no registry for tailnet %q in %q (a reserve command creates it): %w",
		tailnet, s.RegistryDir, err)
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
		return nil, s.registryError(p.Tailnet, err)
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
		return nil, translateSiteIDError(s.registryError(p.Tailnet, err))
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
		return nil, translateSiteIDError(s.registryError(p.Tailnet, err))
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
		return nil, s.registryError(p.Tailnet, err)
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
		return nil, translateSubnetPoolError(s.registryError(p.Tailnet, err))
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
		return nil, translateSubnetPoolError(s.registryError(p.Tailnet, err))
	}
	return &ipam.ReleaseSubnetPoolResult{CIDR: cidr, Changed: changed}, nil
}

// ListServiceAddresses implements ipam.Service.
func (s *Service) ListServiceAddresses(
	_ context.Context,
	p *ipam.ListServiceAddressesPayload,
) ([]*ipam.ServiceAddressReservation, error) {
	entries, err := registry.ListServiceAddressesWithLogger(s.registryPath(p.Tailnet), s.Logger)
	if err != nil {
		return nil, s.registryError(p.Tailnet, err)
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
	_ context.Context,
	p *ipam.ReserveServiceAddressPayload,
) (*ipam.ReserveServiceAddressResult, error) {
	address, service, metadata, changed, err := registry.ReserveServiceAddressWithLogger(
		s.registryPath(p.Tailnet), p.Address, p.Service,
		metadataFromPayload(p.Owner, p.Description, p.Ticket, p.ReservedAt), s.Logger,
	)
	if err != nil {
		return nil, translateServiceAddressError(s.registryError(p.Tailnet, err))
	}
	return &ipam.ReserveServiceAddressResult{
		Address: address, Service: service, Changed: changed,
		Owner: nonEmptyPtr(metadata.Owner), Description: nonEmptyPtr(metadata.Description),
		Ticket: nonEmptyPtr(metadata.Ticket), ReservedAt: nonEmptyPtr(metadata.ReservedAt),
	}, nil
}

// ReleaseServiceAddress implements ipam.Service.
func (s *Service) ReleaseServiceAddress(
	_ context.Context,
	p *ipam.ReleaseServiceAddressPayload,
) (*ipam.ReleaseServiceAddressResult, error) {
	address, changed, err := registry.ReleaseServiceAddressWithLogger(
		s.registryPath(p.Tailnet), p.Address, s.Logger,
	)
	if err != nil {
		return nil, translateServiceAddressError(s.registryError(p.Tailnet, err))
	}
	return &ipam.ReleaseServiceAddressResult{Address: address, Changed: changed}, nil
}

// ListUDPPorts implements ipam.Service.
func (s *Service) ListUDPPorts(
	_ context.Context,
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
	entries, err := registry.ListUDPPortsWithLogger(
		s.registryPath(p.Tailnet), site, s.Logger,
	)
	if err != nil {
		return nil, translateUDPPortError(s.registryError(p.Tailnet, err))
	}
	out := make([]*ipam.UDPPortReservation, len(entries))
	for i, entry := range entries {
		out[i] = &ipam.UDPPortReservation{Site: entry.Site, Port: entry.Port, Device: entry.Device}
	}
	return out, nil
}

// ReserveUDPPort implements ipam.Service.
func (s *Service) ReserveUDPPort(
	_ context.Context,
	p *ipam.ReserveUDPPortPayload,
) (*ipam.ReserveUDPPortResult, error) {
	site, port, device, changed, err := registry.ReserveUDPPortWithLogger(
		s.registryPath(p.Tailnet), p.Site, p.Port, p.Device, s.Logger,
	)
	if err != nil {
		return nil, translateUDPPortError(s.registryError(p.Tailnet, err))
	}
	return &ipam.ReserveUDPPortResult{
		Site: site, Port: port, Device: device, Changed: changed,
	}, nil
}

// ReleaseUDPPort implements ipam.Service.
func (s *Service) ReleaseUDPPort(
	_ context.Context,
	p *ipam.ReleaseUDPPortPayload,
) (*ipam.ReleaseUDPPortResult, error) {
	site, port, changed, err := registry.ReleaseUDPPortWithLogger(
		s.registryPath(p.Tailnet), p.Site, p.Port, s.Logger,
	)
	if err != nil {
		return nil, translateUDPPortError(s.registryError(p.Tailnet, err))
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
	if _, ok := errors.AsType[*registry.InvalidInputError](err); ok {
		return ipam.MakeInvalidInput(err)
	}
	return err
}
