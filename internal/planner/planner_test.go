package planner_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/planner"
	"github.com/rshade/tsipam/internal/registry"
)

//nolint:gochecknoglobals // test flags must be registered at package scope
var updateGolden = flag.Bool("update", false, "update planner golden reports")

func TestCompareAllocations(t *testing.T) {
	t.Parallel()
	reg, err := registry.LoadForPlanning(filepath.Join("testdata", "allocations.registry.hujson"))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := os.ReadFile(filepath.Join("testdata", "allocations.inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var live devices.Snapshot
	//nolint:musttag // this offline fixture uses internal Go field names, not the public wire contract
	if err = json.Unmarshal(inventory, &live); err != nil {
		t.Fatal(err)
	}
	got, err := planner.Compare(reg, live)
	if err != nil {
		t.Fatal(err)
	}
	assertReportGolden(t, got)
	slices.Reverse(live.Devices)
	slices.Reverse(live.Services)
	reordered, err := planner.Compare(reg, live)
	if err != nil {
		t.Fatal(err)
	}
	assertReportGolden(t, reordered)
}

func assertReportGolden(t *testing.T, report planner.Report) {
	t.Helper()
	//nolint:musttag // the golden intentionally captures every internal report field; CLI JSON has separate types
	got, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "allocations.report.golden.json")
	if *updateGolden {
		if err = os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("comparison report differs from golden:\ngot: %s\nwant: %s", got, want)
	}
}

func TestComparePoolContainment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, pool, route string
		matched           bool
	}{
		{"exact", "10.0.0.0/24", "10.0.0.0/24", true},
		{"host bits masked", "10.0.0.0/24", "10.0.0.42/24", true},
		{"narrower route", "10.0.0.0/16", "10.0.4.0/24", true},
		{"broader route", "10.0.0.0/24", "10.0.0.0/16", false},
		{"default route", "10.0.0.0/8", "0.0.0.0/0", false},
		{"different family", "10.0.0.0/8", "2001:db8::/32", false},
		{"IPv6 containment", "2001:db8::/32", "2001:db8:1::/48", true},
		{"mapped live route", "10.0.0.0/16", "::ffff:10.0.4.0/120", true},
		{"mapped reservation", "::ffff:10.0.0.0/112", "10.0.4.0/24", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := &registry.Registry{SubnetPools: map[string]registry.Entry{tc.pool: {Site: "site"}}}
			live := devices.Snapshot{Devices: []devices.Device{{Name: "router", AdvertisedRoutes: []string{tc.route}}}}
			got, err := planner.Compare(reg, live)
			if err != nil {
				t.Fatal(err)
			}
			if tc.matched {
				if len(got.Entries) != 1 || got.Entries[0].State != planner.Matched || got.DriftCount != 0 {
					t.Fatalf("contained route should match: %#v", got)
				}
			} else if got.DriftCount != 2 || len(got.Entries) != 2 {
				t.Fatalf("uncovered route and unused pool should both drift: %#v", got)
			}
		})
	}
}

func TestCompareLiveSiteIDAndServiceConflict(t *testing.T) {
	t.Parallel()
	reg := &registry.Registry{ServiceAddresses: map[string]registry.ServiceEntry{
		"100.100.100.1": {Service: "svc:one"},
	}}
	live := devices.Snapshot{
		Devices: []devices.Device{{Name: "router", AdvertisedRoutes: []string{"fd7a:115c:a1e0:b1a:0:ffff:a00:0/120"}}},
		Services: []devices.Service{
			{Name: "one", Addresses: []string{"100.100.100.1"}},
			{Name: "two", Addresses: []string{"100.100.100.1"}},
		},
	}
	got, err := planner.Compare(reg, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 3 || got.DriftCount != 3 || got.Entries[0].Key != "65535" ||
		got.Entries[0].State != planner.LiveOnly || got.Entries[2].State != planner.Conflict || len(got.Entries[2].Observed) != 2 {
		t.Fatalf("unreserved site, route and conflicting service = %#v", got)
	}
}

func TestCompareRejectsMalformedLiveData(t *testing.T) {
	t.Parallel()
	cases := map[string]devices.Snapshot{
		"unnamed device": {Devices: []devices.Device{{AdvertisedRoutes: []string{"10.0.0.0/24"}}}},
		"bad route": {
			Devices: []devices.Device{{Name: "private-router", AdvertisedRoutes: []string{"private-invalid"}}},
		},
		"broad via prefix": {
			Devices: []devices.Device{{Name: "router", AdvertisedRoutes: []string{"fd7a:115c:a1e0:b1a::/64"}}},
		},
		"oversized site ID": {
			Devices: []devices.Device{{Name: "router", AdvertisedRoutes: []string{"fd7a:115c:a1e0:b1a:1:0:a00:0/120"}}},
		},
		"bad service": {Services: []devices.Service{{Name: ""}}},
		"bad address": {Services: []devices.Service{{Name: "svc:one", Addresses: []string{"invalid"}}}},
		"mapped service": {
			Services: []devices.Service{{Name: "svc:one", Addresses: []string{"::ffff:100.100.100.1"}}},
		},
		"zoned address": {Services: []devices.Service{{Name: "svc:one", Addresses: []string{"fe80::1%eth0"}}}},
	}
	for name, live := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := planner.Compare(&registry.Registry{}, live)
			if err == nil || len(got.Entries) != 0 {
				t.Fatalf("malformed inventory produced report %#v, error %v", got, err)
			}
		})
	}
}

func TestCompareEmpty(t *testing.T) {
	t.Parallel()
	got, err := planner.Compare(&registry.Registry{}, devices.Snapshot{})
	if err != nil || got.DriftCount != 0 || got.Entries == nil || len(got.Entries) != 0 {
		t.Fatalf("empty comparison = %#v, %v; want non-nil empty entries", got, err)
	}
	if _, nilErr := planner.Compare(nil, devices.Snapshot{}); nilErr == nil {
		t.Fatal("nil registry should be rejected")
	}
}
