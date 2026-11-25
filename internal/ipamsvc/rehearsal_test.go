package ipamsvc_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	goa "goa.design/goa/v3/pkg"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/ipamsvc"
	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

const rehearsalTailnet = "example.ts.net"

const rehearsalRegistry = `{
  // Kept as written.
  "siteIDs": {"1": "hq"},
  "subnetPools": {"10.0.0.0/24": {"site": "hq", "ticket": "OPS-1", "reservedAt": "2026-11-25T12:00:00Z"}},
  "serviceAddresses": {"100.100.100.7": "svc:payments"},
  "udpPorts": {"hq": {"41642": "fw01"}},
}
`

type rehearsalCall struct {
	name string
	// run calls one method with dry_run set.
	run func(context.Context, ipam.Service, bool) (any, error)
}

func rehearsalCalls() []rehearsalCall {
	const tailnet = rehearsalTailnet
	return []rehearsalCall{
		{"reserve_site_id", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.ReserveSiteID(ctx, &ipam.ReserveSiteIDPayload{
				Tailnet: tailnet, ID: "2", Site: "branch", DryRun: dry,
			})
		}},
		{"release_site_id", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.ReleaseSiteID(ctx, &ipam.ReleaseSiteIDPayload{Tailnet: tailnet, ID: "1", DryRun: dry})
		}},
		{"reserve_subnet_pool", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.ReserveSubnetPool(ctx, &ipam.ReserveSubnetPoolPayload{
				Tailnet: tailnet, CIDR: "10.1.0.0/24", Site: "hq", DryRun: dry,
			})
		}},
		{"allocate_subnet_pool", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.AllocateSubnetPool(ctx, &ipam.AllocateSubnetPoolPayload{
				Tailnet: tailnet, Pool: "10.0.0.0/16", Size: 24, Site: "branch", DryRun: dry,
			})
		}},
		{"release_subnet_pool", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.ReleaseSubnetPool(ctx, &ipam.ReleaseSubnetPoolPayload{
				Tailnet: tailnet, CIDR: "10.0.0.0/24", DryRun: dry,
			})
		}},
		{"reserve_service_address", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.ReserveServiceAddress(ctx, &ipam.ReserveServiceAddressPayload{
				Tailnet: tailnet, Address: "100.100.100.8", Service: "search", DryRun: dry,
			})
		}},
		{"release_service_address", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.ReleaseServiceAddress(ctx, &ipam.ReleaseServiceAddressPayload{
				Tailnet: tailnet, Address: "100.100.100.7", DryRun: dry,
			})
		}},
		{"reserve_udp_port", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.ReserveUDPPort(ctx, &ipam.ReserveUDPPortPayload{
				Tailnet: tailnet, Site: "branch", Port: "41642", Device: "fw02", DryRun: dry,
			})
		}},
		{"release_udp_port", func(ctx context.Context, svc ipam.Service, dry bool) (any, error) {
			return svc.ReleaseUDPPort(ctx, &ipam.ReleaseUDPPortPayload{
				Tailnet: tailnet, Site: "hq", Port: "41642", DryRun: dry,
			})
		}},
	}
}

func rehearsalService(t *testing.T) (*ipamsvc.Service, string) {
	t.Helper()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, rehearsalTailnet)
	if err := os.WriteFile(path, []byte(rehearsalRegistry), 0o600); err != nil {
		t.Fatal(err)
	}
	return &ipamsvc.Service{Ledger: registry.NewLedger(hujsonstore.New(dir, nil), nil)}, path
}

func assertRegistryBytes(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("registry = %s, want %s", got, want)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want only the registry", len(entries))
	}
}

func TestDryRunReportsTheRealOutcomeAndWritesNothing(t *testing.T) {
	t.Parallel()
	for _, call := range rehearsalCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()
			svc, path := rehearsalService(t)
			rehearsed, err := call.run(context.Background(), svc, true)
			if err != nil {
				t.Fatal(err)
			}
			assertRegistryBytes(t, path, rehearsalRegistry)

			committed, err := call.run(context.Background(), svc, false)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rehearsed, committed) {
				t.Errorf("rehearsed %+v, committed %+v", rehearsed, committed)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) == rehearsalRegistry {
				t.Error("the real call left the registry unchanged, so the rehearsal proves nothing")
			}
		})
	}
}

func TestDryRunRefusesWhatARealCallRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, wantName string
		run            func(context.Context, ipam.Service) error
	}{
		{"collision", "collision", func(ctx context.Context, svc ipam.Service) error {
			_, err := svc.ReserveSiteID(ctx, &ipam.ReserveSiteIDPayload{
				Tailnet: rehearsalTailnet, ID: "1", Site: "branch", DryRun: true,
			})
			return err
		}},
		{"exhausted", "exhausted", func(ctx context.Context, svc ipam.Service) error {
			_, err := svc.AllocateSubnetPool(ctx, &ipam.AllocateSubnetPoolPayload{
				Tailnet: rehearsalTailnet, Pool: "10.0.0.0/24", Size: 24, Site: "branch", DryRun: true,
			})
			return err
		}},
		{"invalid input", "invalid_input", func(ctx context.Context, svc ipam.Service) error {
			_, err := svc.ReleaseUDPPort(ctx, &ipam.ReleaseUDPPortPayload{
				Tailnet: rehearsalTailnet, Site: "hq", Port: "80", DryRun: true,
			})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			svc, path := rehearsalService(t)
			err := test.run(context.Background(), svc)
			svcErr, ok := errors.AsType[*goa.ServiceError](err)
			if !ok || svcErr.Name != test.wantName {
				t.Fatalf("error = %v, want a %s service error", err, test.wantName)
			}
			assertRegistryBytes(t, path, rehearsalRegistry)
		})
	}
}

func TestDryRunReplaysATicket(t *testing.T) {
	t.Parallel()
	svc, path := rehearsalService(t)
	ticket := "OPS-1"
	result, err := svc.AllocateSubnetPool(context.Background(), &ipam.AllocateSubnetPoolPayload{
		Tailnet: rehearsalTailnet, Pool: "10.0.0.0/16", Size: 24, Site: "hq", Ticket: &ticket, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CIDR != "10.0.0.0/24" || result.Changed {
		t.Errorf("result = %+v, want the ticket's reservation unchanged", result)
	}
	assertRegistryBytes(t, path, rehearsalRegistry)
}
