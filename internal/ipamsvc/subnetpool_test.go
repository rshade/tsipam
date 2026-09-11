package ipamsvc_test

import (
	"context"
	"errors"
	"strings"
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

func TestServiceAllocateSubnetPoolReservesAndReplaysByTicket(t *testing.T) {
	t.Parallel()
	svc := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()
	ticket, owner := "OPS-1", "alice"

	allocated, err := svc.AllocateSubnetPool(ctx, &ipam.AllocateSubnetPoolPayload{
		Tailnet: "acme", Pool: "10.0.0.0/16", Size: 24, Site: "hq-vpc",
		LiveTaken: []string{"10.0.0.0/24"}, Ticket: &ticket, Owner: &owner,
	})
	if err != nil {
		t.Fatalf("AllocateSubnetPool: %v", err)
	}
	if !allocated.Changed || allocated.CIDR != "10.0.1.0/24" || allocated.Site != "hq-vpc" {
		t.Errorf("AllocateSubnetPool result = %+v, want 10.0.1.0/24 for hq-vpc with Changed=true", allocated)
	}
	if allocated.Ticket == nil || *allocated.Ticket != ticket ||
		allocated.Owner == nil || *allocated.Owner != owner || allocated.ReservedAt == nil {
		t.Errorf("AllocateSubnetPool metadata = owner %v ticket %v reserved_at %v, want both and an auto-stamp",
			allocated.Owner, allocated.Ticket, allocated.ReservedAt)
	}

	replayed, err := svc.AllocateSubnetPool(ctx, &ipam.AllocateSubnetPoolPayload{
		Tailnet: "acme", Pool: "10.0.0.0/16", Size: 24, Site: "hq-vpc", Ticket: &ticket,
	})
	if err != nil {
		t.Fatalf("replay AllocateSubnetPool: %v", err)
	}
	if replayed.Changed || replayed.CIDR != allocated.CIDR {
		t.Errorf("replay result = %+v, want %s with Changed=false", replayed, allocated.CIDR)
	}
}

func TestServiceAllocateSubnetPoolTranslatesErrors(t *testing.T) {
	t.Parallel()
	svc := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()

	if _, err := svc.ReserveSubnetPool(ctx, &ipam.ReserveSubnetPoolPayload{
		Tailnet: "acme", CIDR: "10.0.0.0/25", Site: "hq-vpc",
	}); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	cases := []struct {
		name     string
		payload  *ipam.AllocateSubnetPoolPayload
		wantName string
		wantIn   string
	}{
		{
			name: "exhausted pool",
			payload: &ipam.AllocateSubnetPoolPayload{
				Tailnet: "acme", Pool: "10.0.0.0/24", Size: 25, Site: "branch",
				LiveTaken: []string{"10.0.0.128/25"},
			},
			wantName: "exhausted", wantIn: "no free /25 block in 10.0.0.0/24",
		},
		{
			name: "size outside pool",
			payload: &ipam.AllocateSubnetPoolPayload{
				Tailnet: "acme", Pool: "10.0.0.0/24", Size: 16, Site: "branch",
			},
			wantName: "invalid_input", wantIn: "between pool /24",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.AllocateSubnetPool(ctx, tc.payload)
			var svcErr *goa.ServiceError
			if !errors.As(err, &svcErr) || svcErr.Name != tc.wantName {
				t.Fatalf("AllocateSubnetPool error = %v, want a %q *goa.ServiceError", err, tc.wantName)
			}
			if !strings.Contains(svcErr.Message, tc.wantIn) {
				t.Errorf("message = %q, want it to contain %q", svcErr.Message, tc.wantIn)
			}
		})
	}
}
