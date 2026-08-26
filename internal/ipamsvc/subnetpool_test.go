package ipamsvc_test

import (
	"context"
	"errors"
	"testing"

	goa "goa.design/goa/v3/pkg"

	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/ipamsvc"
)

func TestServiceSubnetPoolReserveListRelease(t *testing.T) {
	t.Parallel()
	svc := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()

	reserved, err := svc.ReserveSubnetPool(ctx, &ipam.ReserveSubnetPoolPayload{
		Tailnet: "acme", CIDR: "10.0.0.0/24", Site: "hq-vpc",
	})
	if err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}
	if !reserved.Changed || reserved.CIDR != "10.0.0.0/24" || reserved.Site != "hq-vpc" {
		t.Errorf("ReserveSubnetPool result = %+v, want CIDR/site and Changed=true", reserved)
	}

	list, err := svc.ListSubnetPools(ctx, &ipam.ListSubnetPoolsPayload{Tailnet: "acme"})
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(list) != 1 || list[0].CIDR != "10.0.0.0/24" || list[0].Site != "hq-vpc" {
		t.Errorf("ListSubnetPools = %+v, want one hq-vpc entry", list)
	}

	released, err := svc.ReleaseSubnetPool(ctx, &ipam.ReleaseSubnetPoolPayload{
		Tailnet: "acme", CIDR: "10.0.0.0/24",
	})
	if err != nil {
		t.Fatalf("ReleaseSubnetPool: %v", err)
	}
	if !released.Changed || released.CIDR != "10.0.0.0/24" {
		t.Errorf("ReleaseSubnetPool result = %+v, want CIDR and Changed=true", released)
	}
}

func TestServiceSubnetPoolCollisionReturnsGoaServiceError(t *testing.T) {
	t.Parallel()
	svc := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()

	if _, err := svc.ReserveSubnetPool(ctx, &ipam.ReserveSubnetPoolPayload{
		Tailnet: "acme", CIDR: "10.0.0.0/16", Site: "hq-vpc",
	}); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	_, err := svc.ReserveSubnetPool(ctx, &ipam.ReserveSubnetPoolPayload{
		Tailnet: "acme", CIDR: "10.0.1.0/24", Site: "branch-office",
	})
	var svcErr *goa.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Name != "collision" {
		t.Fatalf("ReserveSubnetPool collision error = %v, want a %q *goa.ServiceError", err, "collision")
	}
}
