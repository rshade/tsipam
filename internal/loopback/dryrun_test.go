package loopback_test

import (
	"context"
	"os"
	"reflect"
	"testing"

	goa "goa.design/goa/v3/pkg"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/loopback"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

const dryRunRegistry = `{
  "siteIDs": {"1": "hq"},
  "subnetPools": {"10.0.0.0/24": "hq"},
  "serviceAddresses": {"100.100.100.7": "svc:payments"},
  "udpPorts": {"hq": {"41642": "fw01"}}
}
`

type dryRunCall struct {
	name     string
	endpoint func(*ipamclient.Client) goa.Endpoint
	payload  func(dryRun bool) any
}

func dryRunCalls() []dryRunCall {
	const tailnet = "acme"
	return []dryRunCall{
		{"reserve_site_id", (*ipamclient.Client).ReserveSiteID, func(dry bool) any {
			return &ipam.ReserveSiteIDPayload{Tailnet: tailnet, ID: "2", Site: "branch", DryRun: dry}
		}},
		{"release_site_id", (*ipamclient.Client).ReleaseSiteID, func(dry bool) any {
			return &ipam.ReleaseSiteIDPayload{Tailnet: tailnet, ID: "1", DryRun: dry}
		}},
		{"reserve_subnet_pool", (*ipamclient.Client).ReserveSubnetPool, func(dry bool) any {
			return &ipam.ReserveSubnetPoolPayload{Tailnet: tailnet, CIDR: "10.1.0.0/24", Site: "hq", DryRun: dry}
		}},
		{"allocate_subnet_pool", (*ipamclient.Client).AllocateSubnetPool, func(dry bool) any {
			return &ipam.AllocateSubnetPoolPayload{
				Tailnet: tailnet, Pool: "10.0.0.0/16", Size: 24, Site: "branch", DryRun: dry,
			}
		}},
		{"release_subnet_pool", (*ipamclient.Client).ReleaseSubnetPool, func(dry bool) any {
			return &ipam.ReleaseSubnetPoolPayload{Tailnet: tailnet, CIDR: "10.0.0.0/24", DryRun: dry}
		}},
		{"reserve_service_address", (*ipamclient.Client).ReserveServiceAddress, func(dry bool) any {
			return &ipam.ReserveServiceAddressPayload{
				Tailnet: tailnet, Address: "100.100.100.8", Service: "search", DryRun: dry,
			}
		}},
		{"release_service_address", (*ipamclient.Client).ReleaseServiceAddress, func(dry bool) any {
			return &ipam.ReleaseServiceAddressPayload{Tailnet: tailnet, Address: "100.100.100.7", DryRun: dry}
		}},
		{"reserve_udp_port", (*ipamclient.Client).ReserveUDPPort, func(dry bool) any {
			return &ipam.ReserveUDPPortPayload{
				Tailnet: tailnet, Site: "branch", Port: "41642", Device: "fw02", DryRun: dry,
			}
		}},
		{"release_udp_port", (*ipamclient.Client).ReleaseUDPPort, func(dry bool) any {
			return &ipam.ReleaseUDPPortPayload{Tailnet: tailnet, Site: "hq", Port: "41642", DryRun: dry}
		}},
	}
}

func callOverLoopback(t *testing.T, dir string, call dryRunCall, dryRun bool) any {
	t.Helper()
	var result any
	err := loopback.Run(
		t.Context(), newHandler(dir), nil,
		func(ctx context.Context, client *ipamclient.Client) error {
			var callErr error
			result, callErr = call.endpoint(client)(ctx, call.payload(dryRun))
			return callErr
		})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDryRunCrossesTheLoopback(t *testing.T) {
	t.Parallel()
	for _, call := range dryRunCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := hujsonstore.Path(dir, "acme")
			if err := os.WriteFile(path, []byte(dryRunRegistry), 0o600); err != nil {
				t.Fatal(err)
			}

			rehearsed := callOverLoopback(t, dir, call, true)
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != dryRunRegistry {
				t.Fatalf("the rehearsal wrote %s", after)
			}

			committed := callOverLoopback(t, dir, call, false)
			if !reflect.DeepEqual(rehearsed, committed) {
				t.Errorf("rehearsed %+v, committed %+v", rehearsed, committed)
			}
			after, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) == dryRunRegistry {
				t.Error("the real call left the registry unchanged, so the rehearsal proves nothing")
			}
		})
	}
}
