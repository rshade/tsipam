package ipamsvc_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goa "goa.design/goa/v3/pkg"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/ipamsvc"
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

func TestServiceAddressReserveListRelease(t *testing.T) {
	t.Parallel()
	service := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()
	owner, description, ticket, reservedAt := "network-team", "internal dashboard", "NET-237", "2026-08-31T12:00:00Z"

	reserved, err := service.ReserveServiceAddress(ctx, &ipam.ReserveServiceAddressPayload{
		Tailnet: "acme", Address: "100.100.100.8/32", Service: "dashboard",
		Owner: &owner, Description: &description, Ticket: &ticket, ReservedAt: &reservedAt,
	})
	if err != nil {
		t.Fatalf("ReserveServiceAddress: %v", err)
	}
	if reserved.Address != "100.100.100.8" || reserved.Service != "svc:dashboard" || !reserved.Changed {
		t.Errorf("ReserveServiceAddress result = %+v, want canonical address and service with Changed=true", reserved)
	}
	if reserved.Owner == nil || *reserved.Owner != owner ||
		reserved.Description == nil || *reserved.Description != description ||
		reserved.Ticket == nil || *reserved.Ticket != ticket ||
		reserved.ReservedAt == nil || *reserved.ReservedAt != reservedAt {
		t.Errorf("ReserveServiceAddress metadata = %+v, want payload metadata", reserved)
	}

	list, err := service.ListServiceAddresses(ctx, &ipam.ListServiceAddressesPayload{Tailnet: "acme"})
	if err != nil {
		t.Fatalf("ListServiceAddresses: %v", err)
	}
	if len(list) != 1 || list[0].Address != "100.100.100.8" || list[0].Service != "svc:dashboard" {
		t.Fatalf("ListServiceAddresses = %+v, want one canonical reservation", list)
	}
	if list[0].Owner == nil || *list[0].Owner != owner {
		t.Errorf("ListServiceAddresses metadata = %+v, want owner %q", list[0], owner)
	}

	released, err := service.ReleaseServiceAddress(ctx, &ipam.ReleaseServiceAddressPayload{
		Tailnet: "acme", Address: "100.100.100.8/32",
	})
	if err != nil {
		t.Fatalf("ReleaseServiceAddress: %v", err)
	}
	if released.Address != "100.100.100.8" || !released.Changed {
		t.Errorf("ReleaseServiceAddress result = %+v, want canonical address with Changed=true", released)
	}
}

func TestServiceAddressCollisionReturnsNamedError(t *testing.T) {
	t.Parallel()
	service := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()

	if _, err := service.ReserveServiceAddress(ctx, &ipam.ReserveServiceAddressPayload{
		Tailnet: "acme", Address: "100.100.100.8", Service: "svc:dashboard",
	}); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	_, err := service.ReserveServiceAddress(ctx, &ipam.ReserveServiceAddressPayload{
		Tailnet: "acme", Address: "100.100.100.8", Service: "svc:other",
	})
	assertServiceErrorName(t, err, "collision")
}

func TestServiceAddressValidationReturnsNamedError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(*ipamsvc.Service) error
	}{
		{
			name: "reserve address",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveServiceAddress(context.Background(), &ipam.ReserveServiceAddressPayload{
					Tailnet: "acme", Address: "100.100.100.0/24", Service: "svc:dashboard",
				})
				return err
			},
		},
		{
			name: "reserve service",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveServiceAddress(context.Background(), &ipam.ReserveServiceAddressPayload{
					Tailnet: "acme", Address: "100.100.100.8", Service: "svc:",
				})
				return err
			},
		},
		{
			name: "release address",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReleaseServiceAddress(context.Background(), &ipam.ReleaseServiceAddressPayload{
					Tailnet: "acme", Address: "not-an-address",
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertServiceErrorName(t, test.run(&ipamsvc.Service{RegistryDir: t.TempDir()}), "invalid_input")
		})
	}
}

func TestServiceAddressRegistryFailureIsNotMisclassified(t *testing.T) {
	t.Parallel()
	registryDir := filepath.Join(t.TempDir(), "registry-file")
	if err := os.WriteFile(registryDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	service := &ipamsvc.Service{RegistryDir: registryDir}

	_, err := service.ListServiceAddresses(
		context.Background(),
		&ipam.ListServiceAddressesPayload{Tailnet: "acme"},
	)
	if err == nil {
		t.Fatal("ListServiceAddresses error = nil, want registry I/O failure")
	}
	if _, ok := errors.AsType[*goa.ServiceError](err); ok {
		t.Fatalf("ListServiceAddresses error = %v, want unclassified registry failure", err)
	}
}

func TestServiceAddressMalformedStoredStateIsNotInvalidInput(t *testing.T) {
	t.Parallel()
	registryDir := t.TempDir()
	registryFile := filepath.Join(registryDir, "acme.ipam.hujson")
	if err := os.WriteFile(registryFile, []byte(`{
		"serviceAddresses": {
			"999.1.1.1": "svc:bad",
			"100.100.100.8": "svc:good"
		}
	}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	service := &ipamsvc.Service{RegistryDir: registryDir}
	ctx := context.Background()

	// ReserveServiceAddress routes through translateServiceAddressError, which pre-fix
	// would misclassify stored-state InvalidInputError markers as HTTP 400 bad-input.
	_, err := service.ReserveServiceAddress(ctx, &ipam.ReserveServiceAddressPayload{
		Tailnet: "acme", Address: "10.0.0.1", Service: "svc:test",
	})
	if err == nil {
		t.Fatal("ReserveServiceAddress error = nil, want malformed map rejection")
	}
	var serviceError *goa.ServiceError
	if errors.As(err, &serviceError) && serviceError.Name == "invalid_input" {
		t.Fatalf("ReserveServiceAddress error misclassified as %q, want generic error", serviceError.Name)
	}

	// ReleaseServiceAddress also routes through translateServiceAddressError.
	_, err = service.ReleaseServiceAddress(ctx, &ipam.ReleaseServiceAddressPayload{
		Tailnet: "acme", Address: "100.100.100.8",
	})
	if err == nil {
		t.Fatal("ReleaseServiceAddress error = nil, want malformed map rejection")
	}
	if errors.As(err, &serviceError) && serviceError.Name == "invalid_input" {
		t.Fatalf("ReleaseServiceAddress error misclassified as %q, want generic error", serviceError.Name)
	}
}

func TestServiceAddressReserveAgainstMalformedStoredStatePreservesContext(t *testing.T) {
	t.Parallel()
	registryDir := t.TempDir()
	registryFile := filepath.Join(registryDir, "acme.ipam.hujson")
	// Empty service name value triggers the stored-state validation path that pre-fix
	// would have been misclassified as invalid_input due to InvalidInputError wrapping.
	if err := os.WriteFile(registryFile, []byte(`{
		"serviceAddresses": {
			"10.0.0.5": ""
		}
	}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	service := &ipamsvc.Service{RegistryDir: registryDir}

	_, err := service.ReserveServiceAddress(context.Background(), &ipam.ReserveServiceAddressPayload{
		Tailnet: "acme", Address: "100.100.100.8", Service: "svc:new",
	})
	if err == nil {
		t.Fatal("ReserveServiceAddress error = nil, want malformed map rejection")
	}
	// Error must not be misclassified as invalid_input (the fix being tested).
	var serviceError *goa.ServiceError
	if errors.As(err, &serviceError) && serviceError.Name == "invalid_input" {
		t.Fatalf("error misclassified as %q, want generic error", serviceError.Name)
	}
	// Error must preserve decoding context and name the offending key.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "decoding registry") {
		t.Errorf("error = %q, want 'decoding registry' context", errMsg)
	}
	if !strings.Contains(errMsg, `key "10.0.0.5"`) {
		t.Errorf("error = %q, want 'key \"10.0.0.5\"' context", errMsg)
	}
}

func TestSiteIDValidationReturnsNamedError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(*ipamsvc.Service) error
	}{
		{
			name: "reserve invalid ID",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveSiteID(context.Background(), &ipam.ReserveSiteIDPayload{
					Tailnet: "acme", ID: "not-a-number", Site: "hq",
				})
				return err
			},
		},
		{
			name: "reserve invalid site name",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveSiteID(context.Background(), &ipam.ReserveSiteIDPayload{
					Tailnet: "acme", ID: "1", Site: "",
				})
				return err
			},
		},
		{
			name: "release invalid ID",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReleaseSiteID(context.Background(), &ipam.ReleaseSiteIDPayload{
					Tailnet: "acme", ID: "99999",
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertServiceErrorName(t, test.run(&ipamsvc.Service{RegistryDir: t.TempDir()}), "invalid_input")
		})
	}
}

func TestSubnetPoolValidationReturnsNamedError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(*ipamsvc.Service) error
	}{
		{
			name: "reserve invalid CIDR",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveSubnetPool(context.Background(), &ipam.ReserveSubnetPoolPayload{
					Tailnet: "acme", CIDR: "not-a-cidr", Site: "hq",
				})
				return err
			},
		},
		{
			name: "reserve non-canonical CIDR",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveSubnetPool(context.Background(), &ipam.ReserveSubnetPoolPayload{
					Tailnet: "acme", CIDR: "10.0.0.1/24", Site: "hq",
				})
				return err
			},
		},
		{
			name: "reserve invalid site name",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveSubnetPool(context.Background(), &ipam.ReserveSubnetPoolPayload{
					Tailnet: "acme", CIDR: "10.0.0.0/25", Site: "",
				})
				return err
			},
		},
		{
			name: "release invalid CIDR",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReleaseSubnetPool(context.Background(), &ipam.ReleaseSubnetPoolPayload{
					Tailnet: "acme", CIDR: "not-a-cidr",
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertServiceErrorName(t, test.run(&ipamsvc.Service{RegistryDir: t.TempDir()}), "invalid_input")
		})
	}
}

func TestSiteIDMalformedStoredStateIsNotInvalidInput(t *testing.T) {
	t.Parallel()
	registryDir := t.TempDir()
	registryFile := filepath.Join(registryDir, "acme.ipam.hujson")
	if err := os.WriteFile(registryFile, []byte(`{
		"siteIDs": {
			"99999": "hq",
			"1": "site1"
		}
	}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	service := &ipamsvc.Service{RegistryDir: registryDir}
	ctx := context.Background()

	_, err := service.ReserveSiteID(ctx, &ipam.ReserveSiteIDPayload{
		Tailnet: "acme", ID: "2", Site: "site2",
	})
	if err == nil {
		t.Fatal("ReserveSiteID error = nil, want malformed map rejection")
	}
	var serviceError *goa.ServiceError
	if errors.As(err, &serviceError) && serviceError.Name == "invalid_input" {
		t.Fatalf("ReserveSiteID error misclassified as %q, want generic error", serviceError.Name)
	}

	_, err = service.ReleaseSiteID(ctx, &ipam.ReleaseSiteIDPayload{
		Tailnet: "acme", ID: "1",
	})
	if err == nil {
		t.Fatal("ReleaseSiteID error = nil, want malformed map rejection")
	}
	if errors.As(err, &serviceError) && serviceError.Name == "invalid_input" {
		t.Fatalf("ReleaseSiteID error misclassified as %q, want generic error", serviceError.Name)
	}
}

func TestSubnetPoolMalformedStoredStateIsNotInvalidInput(t *testing.T) {
	t.Parallel()
	registryDir := t.TempDir()
	registryFile := filepath.Join(registryDir, "acme.ipam.hujson")
	if err := os.WriteFile(registryFile, []byte(`{
		"subnetPools": {
			"not-a-cidr": "site1",
			"10.0.0.0/25": "site2"
		}
	}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	service := &ipamsvc.Service{RegistryDir: registryDir}
	ctx := context.Background()

	_, err := service.ReserveSubnetPool(ctx, &ipam.ReserveSubnetPoolPayload{
		Tailnet: "acme", CIDR: "10.0.1.0/25", Site: "site3",
	})
	if err == nil {
		t.Fatal("ReserveSubnetPool error = nil, want malformed map rejection")
	}
	var serviceError *goa.ServiceError
	if errors.As(err, &serviceError) && serviceError.Name == "invalid_input" {
		t.Fatalf("ReserveSubnetPool error misclassified as %q, want generic error", serviceError.Name)
	}

	_, err = service.ReleaseSubnetPool(ctx, &ipam.ReleaseSubnetPoolPayload{
		Tailnet: "acme", CIDR: "10.0.0.0/25",
	})
	if err == nil {
		t.Fatal("ReleaseSubnetPool error = nil, want malformed map rejection")
	}
	if errors.As(err, &serviceError) && serviceError.Name == "invalid_input" {
		t.Fatalf("ReleaseSubnetPool error misclassified as %q, want generic error", serviceError.Name)
	}
}

func TestSiteIDInvalidMetadataReturnsNamedError(t *testing.T) {
	t.Parallel()
	service := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()
	badReservedAt := "not-a-timestamp"

	_, err := service.ReserveSiteID(ctx, &ipam.ReserveSiteIDPayload{
		Tailnet: "acme", ID: "1", Site: "hq", ReservedAt: &badReservedAt,
	})
	assertServiceErrorName(t, err, "invalid_input")
}

func TestSubnetPoolInvalidMetadataReturnsNamedError(t *testing.T) {
	t.Parallel()
	service := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()
	badReservedAt := "not-a-timestamp"

	_, err := service.ReserveSubnetPool(ctx, &ipam.ReserveSubnetPoolPayload{
		Tailnet: "acme", CIDR: "10.0.0.0/25", Site: "hq", ReservedAt: &badReservedAt,
	})
	assertServiceErrorName(t, err, "invalid_input")
}

func assertServiceErrorName(t *testing.T, err error, want string) {
	t.Helper()
	var serviceError *goa.ServiceError
	if !errors.As(err, &serviceError) || serviceError.Name != want {
		t.Fatalf("error = %v, want a %q *goa.ServiceError", err, want)
	}
}
