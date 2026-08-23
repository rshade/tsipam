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
		out[i] = &ipam.SiteIDReservation{ID: entry.ID, Site: entry.Site}
	}
	return out, nil
}

// ReserveSiteID implements ipam.Service.
func (s *Service) ReserveSiteID(_ context.Context, p *ipam.ReserveSiteIDPayload) (*ipam.ReserveSiteIDResult, error) {
	id, site, changed, err := registry.ReserveSiteIDWithLogger(
		s.registryPath(p.Tailnet), p.ID, p.Site, s.Logger,
	)
	if err != nil {
		var collision *registry.SiteIDCollisionError
		if errors.As(err, &collision) {
			return nil, ipam.MakeCollision(collision)
		}
		return nil, err
	}
	return &ipam.ReserveSiteIDResult{ID: id, Site: site, Changed: changed}, nil
}

// ReleaseSiteID implements ipam.Service.
func (s *Service) ReleaseSiteID(_ context.Context, p *ipam.ReleaseSiteIDPayload) (*ipam.ReleaseSiteIDResult, error) {
	id, changed, err := registry.ReleaseSiteIDWithLogger(s.registryPath(p.Tailnet), p.ID, s.Logger)
	if err != nil {
		return nil, err
	}
	return &ipam.ReleaseSiteIDResult{ID: id, Changed: changed}, nil
}
