package ipamsvc_test

import (
	"context"
	"errors"
	"testing"

	goa "goa.design/goa/v3/pkg"

	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/ipamsvc"
)

func TestServiceReserveListRelease(t *testing.T) {
	t.Parallel()
	svc := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()

	reserved, err := svc.ReserveSiteID(ctx, &ipam.ReserveSiteIDPayload{Tailnet: "acme", ID: "1", Site: "hq-vpc"})
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if !reserved.Changed || reserved.ID != "1" || reserved.Site != "hq-vpc" {
		t.Errorf("ReserveSiteID result = %+v, want {ID:1 Site:hq-vpc Changed:true}", reserved)
	}

	list, err := svc.ListSiteIds(ctx, &ipam.ListSiteIdsPayload{Tailnet: "acme"})
	if err != nil {
		t.Fatalf("ListSiteIds: %v", err)
	}
	if len(list) != 1 || list[0].ID != "1" || list[0].Site != "hq-vpc" {
		t.Errorf("ListSiteIds = %+v, want one entry {ID:1 Site:hq-vpc}", list)
	}

	released, err := svc.ReleaseSiteID(ctx, &ipam.ReleaseSiteIDPayload{Tailnet: "acme", ID: "1"})
	if err != nil {
		t.Fatalf("ReleaseSiteID: %v", err)
	}
	if !released.Changed || released.ID != "1" {
		t.Errorf("ReleaseSiteID result = %+v, want {ID:1 Changed:true}", released)
	}

	list, err = svc.ListSiteIds(ctx, &ipam.ListSiteIdsPayload{Tailnet: "acme"})
	if err != nil {
		t.Fatalf("ListSiteIds after release: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListSiteIds after release = %+v, want empty", list)
	}
}

func TestServiceReserveSiteIDPassesMetadataThrough(t *testing.T) {
	t.Parallel()
	svc := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()
	owner, ticket := "alice", "OPS-1234"

	reserved, err := svc.ReserveSiteID(ctx, &ipam.ReserveSiteIDPayload{
		Tailnet: "acme", ID: "1", Site: "hq-vpc", Owner: &owner, Ticket: &ticket,
	})
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if reserved.Owner == nil || *reserved.Owner != owner {
		t.Errorf("reserved.Owner = %v, want %q", reserved.Owner, owner)
	}
	if reserved.Ticket == nil || *reserved.Ticket != ticket {
		t.Errorf("reserved.Ticket = %v, want %q", reserved.Ticket, ticket)
	}
	if reserved.ReservedAt == nil || *reserved.ReservedAt == "" {
		t.Error("reserved.ReservedAt is nil or empty, want it auto-stamped")
	}
	if reserved.Description != nil {
		t.Errorf("reserved.Description = %v, want nil (never set)", reserved.Description)
	}

	list, err := svc.ListSiteIds(ctx, &ipam.ListSiteIdsPayload{Tailnet: "acme"})
	if err != nil {
		t.Fatalf("ListSiteIds: %v", err)
	}
	if len(list) != 1 || list[0].Owner == nil || *list[0].Owner != owner {
		t.Errorf("ListSiteIds = %+v, want one entry with Owner=%q", list, owner)
	}
}

func TestServiceReserveCollisionReturnsGoaServiceError(t *testing.T) {
	t.Parallel()
	svc := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()

	if _, err := svc.ReserveSiteID(
		ctx,
		&ipam.ReserveSiteIDPayload{Tailnet: "acme", ID: "1", Site: "hq-vpc"},
	); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	_, err := svc.ReserveSiteID(ctx, &ipam.ReserveSiteIDPayload{Tailnet: "acme", ID: "1", Site: "other-site"})
	var svcErr *goa.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Name != "collision" {
		t.Fatalf("ReserveSiteID collision error = %v, want a %q *goa.ServiceError", err, "collision")
	}
}

func TestServiceIsolatesRegistriesByTailnet(t *testing.T) {
	t.Parallel()
	svc := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()

	if _, err := svc.ReserveSiteID(
		ctx,
		&ipam.ReserveSiteIDPayload{Tailnet: "acme", ID: "1", Site: "acme-site"},
	); err != nil {
		t.Fatalf("reserving in acme: %v", err)
	}
	if _, err := svc.ReserveSiteID(
		ctx,
		&ipam.ReserveSiteIDPayload{Tailnet: "local", ID: "1", Site: "local-site"},
	); err != nil {
		t.Fatalf("reserving the same id in local: %v", err)
	}

	acmeList, err := svc.ListSiteIds(ctx, &ipam.ListSiteIdsPayload{Tailnet: "acme"})
	if err != nil {
		t.Fatalf("listing acme: %v", err)
	}
	localList, err := svc.ListSiteIds(ctx, &ipam.ListSiteIdsPayload{Tailnet: "local"})
	if err != nil {
		t.Fatalf("listing local: %v", err)
	}
	if len(acmeList) != 1 || acmeList[0].Site != "acme-site" {
		t.Errorf("acme registry = %+v, want one entry for acme-site", acmeList)
	}
	if len(localList) != 1 || localList[0].Site != "local-site" {
		t.Errorf("local registry = %+v, want one entry for local-site", localList)
	}
}
