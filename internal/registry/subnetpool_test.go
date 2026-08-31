package registry_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

func TestNormalizeSubnetCIDR(t *testing.T) {
	t.Parallel()

	valid := []string{"0.0.0.0/0", "10.0.0.0/24", "2001:db8::/48", "::/0"}
	for _, cidr := range valid {
		t.Run(cidr, func(t *testing.T) {
			t.Parallel()
			got, err := registry.NormalizeSubnetCIDR(cidr)
			if err != nil {
				t.Fatalf("NormalizeSubnetCIDR(%q): %v", cidr, err)
			}
			if got != cidr {
				t.Errorf("NormalizeSubnetCIDR(%q) = %q, want unchanged", cidr, got)
			}
		})
	}
}

func TestNormalizeSubnetCIDRRejectsInvalidOrNonCanonicalInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cidr   string
		wantIn string
	}{
		{name: "empty", cidr: "", wantIn: "must not be empty"},
		{name: "bare address", cidr: "10.0.0.1", wantIn: "not a valid prefix"},
		{name: "IPv4 host bits", cidr: "10.0.0.1/24", wantIn: `use "10.0.0.0/24"`},
		{name: "expanded IPv6", cidr: "2001:0db8:0000:0000::/64", wantIn: `use "2001:db8::/64"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := registry.NormalizeSubnetCIDR(tc.cidr)
			if err == nil {
				t.Fatalf("NormalizeSubnetCIDR(%q) error = nil, want an error", tc.cidr)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantIn)
			}
		})
	}
}

func TestReserveSubnetPoolCreatesListsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	cidr, site, _, changed, err := registry.ReserveSubnetPoolWithLogger(
		path,
		"10.0.0.0/24",
		"  hq-vpc  ",
		registry.ReservationMetadata{},
		nil,
	)
	if err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}
	if cidr != "10.0.0.0/24" || site != "hq-vpc" || !changed {
		t.Errorf("ReserveSubnetPool = (%q, %q, %t), want (%q, %q, true)",
			cidr, site, changed, "10.0.0.0/24", "hq-vpc")
	}

	_, _, _, changed, err = registry.ReserveSubnetPoolWithLogger(path, cidr, site, registry.ReservationMetadata{}, nil)
	if err != nil {
		t.Fatalf("repeat ReserveSubnetPool: %v", err)
	}
	if changed {
		t.Error("changed = true on an exact repeat, want false")
	}

	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(entries) != 1 || entries[0].CIDR != cidr || entries[0].Site != site {
		t.Errorf("ListSubnetPools = %+v, want [{10.0.0.0/24 hq-vpc}]", entries)
	}
}

type subnetPoolOverlapCase struct {
	name          string
	existingCIDR  string
	candidateCIDR string
	candidateSite string
	wantMessage   string
	wantCollision bool
}

func TestReserveSubnetPoolOverlapRules(t *testing.T) {
	t.Parallel()

	cases := []subnetPoolOverlapCase{
		{
			name: "adjacent prefixes do not overlap", existingCIDR: "10.0.0.0/24",
			candidateCIDR: "10.0.1.0/24", candidateSite: "branch-office",
		},
		{
			name: "exact duplicate for another site", existingCIDR: "10.0.0.0/24",
			candidateCIDR: "10.0.0.0/24", candidateSite: "branch-office",
			wantMessage:   `subnet CIDR 10.0.0.0/24 is already reserved for "hq-vpc", not "branch-office"`,
			wantCollision: true,
		},
		{
			name: "candidate contained within existing", existingCIDR: "10.0.0.0/16",
			candidateCIDR: "10.0.1.0/24", candidateSite: "branch-office",
			wantMessage:   `subnet CIDR 10.0.1.0/24 overlaps 10.0.0.0/16 reserved for "hq-vpc", not "branch-office"`,
			wantCollision: true,
		},
		{
			name: "existing contained within candidate", existingCIDR: "10.0.1.0/24",
			candidateCIDR: "10.0.0.0/16", candidateSite: "branch-office",
			wantMessage:   `subnet CIDR 10.0.0.0/16 overlaps 10.0.1.0/24 reserved for "hq-vpc", not "branch-office"`,
			wantCollision: true,
		},
		{
			name: "overlap within one site is allowed", existingCIDR: "10.0.0.0/16",
			candidateCIDR: "10.0.1.0/24", candidateSite: "hq-vpc",
		},
		{
			name:         "IPv4 and IPv4-mapped IPv6 spelling overlap",
			existingCIDR: "10.0.0.0/8", candidateCIDR: "::ffff:10.0.0.0/104",
			candidateSite: "branch-office",
			wantMessage: "subnet CIDR ::ffff:10.0.0.0/104 overlaps 10.0.0.0/8 reserved for " +
				`"hq-vpc", not "branch-office"`,
			wantCollision: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSubnetPoolOverlapRule(t, tc)
		})
	}
}

func assertSubnetPoolOverlapRule(t *testing.T, tc subnetPoolOverlapCase) {
	t.Helper()
	path := regPath(t)
	if _, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
		path, tc.existingCIDR, "hq-vpc", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	_, _, _, changed, err := registry.ReserveSubnetPoolWithLogger(
		path, tc.candidateCIDR, tc.candidateSite, registry.ReservationMetadata{}, nil,
	)
	var collision *registry.SubnetPoolCollisionError
	if tc.wantCollision {
		if !errors.As(err, &collision) {
			t.Fatalf("ReserveSubnetPool error = %v, want *SubnetPoolCollisionError", err)
		}
		if collision.ExistingCIDR != tc.existingCIDR || collision.ExistingSite != "hq-vpc" ||
			collision.WantedSite != tc.candidateSite {
			t.Errorf("collision = %+v, want existing CIDR/site and wanted site", collision)
		}
		if err.Error() != tc.wantMessage {
			t.Errorf("collision error = %q, want %q", err, tc.wantMessage)
		}
		if changed {
			t.Error("changed = true on a refused overlap, want false")
		}
		return
	}
	if err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}
	if !changed {
		t.Error("changed = false for an allowed distinct reservation, want true")
	}
}

func TestListSubnetPoolsSortsByNetworkAddress(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	for _, cidr := range []string{"192.168.0.0/16", "10.0.2.0/24", "10.0.1.0/24", "2001:db8::/48"} {
		if _, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
			path,
			cidr,
			"hq-vpc",
			registry.ReservationMetadata{},
			nil,
		); err != nil {
			t.Fatalf("ReserveSubnetPool(%q): %v", cidr, err)
		}
	}
	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	want := []string{"10.0.1.0/24", "10.0.2.0/24", "192.168.0.0/16", "2001:db8::/48"}
	if len(entries) != len(want) {
		t.Fatalf("ListSubnetPools = %+v, want %d entries", entries, len(want))
	}
	for i := range want {
		if entries[i].CIDR != want[i] {
			t.Errorf("ListSubnetPools[%d].CIDR = %q, want %q", i, entries[i].CIDR, want[i])
		}
	}
}

func TestReleaseSubnetPoolRemovesEntryAndAbsentIsNoop(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
		path,
		"10.0.0.0/24",
		"hq-vpc",
		registry.ReservationMetadata{},
		nil,
	); err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}
	cidr, changed, err := registry.ReleaseSubnetPoolWithLogger(path, "10.0.0.0/24", nil)
	if err != nil {
		t.Fatalf("ReleaseSubnetPool: %v", err)
	}
	if cidr != "10.0.0.0/24" || !changed {
		t.Errorf("ReleaseSubnetPool = (%q, %t), want (%q, true)", cidr, changed, "10.0.0.0/24")
	}
	_, changed, err = registry.ReleaseSubnetPoolWithLogger(path, "10.0.0.0/24", nil)
	if err != nil {
		t.Fatalf("repeat ReleaseSubnetPool: %v", err)
	}
	if changed {
		t.Error("changed = true releasing an absent CIDR, want false")
	}
}

func TestReserveSubnetPoolPreservesHandWrittenCommentOnAnotherEntry(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := "{\n\t\"subnetPools\": {\n" +
		"\t\t// reserved for the HQ migration, do not release before Q1\n" +
		"\t\t\"10.0.0.0/24\": \"hq-vpc\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	if _, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
		path,
		"10.0.1.0/24",
		"hq-vpc",
		registry.ReservationMetadata{},
		nil,
	); err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	want := "// reserved for the HQ migration, do not release before Q1"
	if !strings.Contains(string(got), want) {
		t.Errorf("registry after reserving a second subnet = %q, want it to contain %q", got, want)
	}
}

func TestLoadValidatesSubnetPoolShapeAndOverlaps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		wantIn  string
	}{
		{name: "case-variant top-level key", content: `{"subnetpools":{}}`, wantIn: `"subnetPools"`},
		{
			name:    "duplicate exact entry key",
			content: `{"subnetPools":{"10.0.0.0/24":"hq","10.0.0.0/24":"branch"}}`,
			wantIn:  "duplicate",
		},
		{
			name:    "non-canonical entry key",
			content: `{"subnetPools":{"10.0.0.1/24":"hq"}}`,
			wantIn:  "not canonical",
		},
		{
			name:    "null site",
			content: `{"subnetPools":{"10.0.0.0/24":null}}`,
			wantIn:  "empty or null",
		},
		{
			name:    "overlap owned by different sites",
			content: `{"subnetPools":{"10.0.0.0/16":"hq","10.0.1.0/24":"branch"}}`,
			wantIn:  "overlap",
		},
		{
			name:    "IPv4 and 4-in-6 IPv6 spelling overlap owned by different sites",
			content: `{"subnetPools":{"10.0.0.0/8":"hq","::ffff:10.0.0.0/104":"branch"}}`,
			wantIn:  "overlap",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("seeding fixture: %v", err)
			}
			_, err := registry.ListSubnetPoolsWithLogger(path, nil)
			if err == nil {
				t.Fatalf("ListSubnetPools error = nil, want an error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantIn)
			}
		})
	}
}

func TestLoadAllowsOverlappingPoolsForOneSite(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := `{"subnetPools":{"10.0.0.0/16":"hq","10.0.1.0/24":"hq"}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}
	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("ListSubnetPools = %+v, want two same-site entries", entries)
	}
}

// TestReserveSubnetPoolIntoExistingEmptyBlock covers reserving into a
// subnetPools block that is present on file as an empty object, as opposed
// to being entirely absent -- the two must not be conflated.
func TestReserveSubnetPoolIntoExistingEmptyBlock(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"subnetPools":{}}`+"\n"), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	_, _, _, changed, err := registry.ReserveSubnetPoolWithLogger(
		path,
		"10.0.0.0/24",
		"hq-vpc",
		registry.ReservationMetadata{},
		nil,
	)
	if err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for a fresh reservation")
	}

	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(entries) != 1 || entries[0].CIDR != "10.0.0.0/24" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSubnetPools = %+v, want [{10.0.0.0/24 hq-vpc}]", entries)
	}
}

// TestReserveSubnetPoolPreservesCommentInEmptyBlock is the reproduction for
// a regression: a hand-written comment inside an existing empty
// "subnetPools": {} block must be preserved by ReserveSubnetPool.
func TestReserveSubnetPoolPreservesCommentInEmptyBlock(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := "{\n\t\"subnetPools\": {\n\t\t// reserved but not yet assigned\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	if _, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
		path,
		"10.0.0.0/24",
		"hq-vpc",
		registry.ReservationMetadata{},
		nil,
	); err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	want := "// reserved but not yet assigned"
	if !strings.Contains(string(got), want) {
		t.Errorf("registry after reserving into a previously-empty subnetPools block = %q, want it to still contain %q",
			got, want)
	}
}

// TestReserveSubnetPoolIntoNullBlock covers reserving into a subnetPools
// member present on file as an explicit JSON null, which must still take the
// whole-object-add path (there is no existing object node to add a key into).
func TestReserveSubnetPoolIntoNullBlock(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"subnetPools":null}`+"\n"), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	_, _, _, changed, err := registry.ReserveSubnetPoolWithLogger(
		path,
		"10.0.0.0/24",
		"hq-vpc",
		registry.ReservationMetadata{},
		nil,
	)
	if err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for a fresh reservation")
	}

	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(entries) != 1 || entries[0].CIDR != "10.0.0.0/24" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSubnetPools = %+v, want [{10.0.0.0/24 hq-vpc}]", entries)
	}
}

// TestReserveSubnetPoolConcurrentDistinctCIDRsLosesNoReservation tests that
// concurrent reservations of distinct, non-overlapping CIDRs lose no entries.
func TestReserveSubnetPoolConcurrentDistinctCIDRsLosesNoReservation(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	const workers = 10
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Reserve non-overlapping /24 subnets (10.0.i.0/24)
			cidr := fmt.Sprintf("10.0.%d.0/24", i)
			site := fmt.Sprintf("site-%d", i+1)
			_, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
				path,
				cidr,
				site,
				registry.ReservationMetadata{},
				nil,
			)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: ReserveSubnetPool: %v", i, err)
		}
	}

	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(entries) != workers {
		t.Fatalf("ListSubnetPools returned %d entries, want %d (a concurrent reservation was lost)",
			len(entries), workers)
	}
	seen := make(map[string]bool, workers)
	for _, entry := range entries {
		if seen[entry.CIDR] {
			t.Errorf("duplicate entry for subnet CIDR %s in %+v", entry.CIDR, entries)
		}
		seen[entry.CIDR] = true
	}
}

func BenchmarkListSubnetPoolsWithLogger(b *testing.B) {
	for _, count := range []int{500, 2_000, 8_000} {
		b.Run(fmt.Sprintf("pools=%d", count), func(b *testing.B) {
			path := benchmarkSubnetPoolRegistry(b, count)
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
				if err != nil {
					b.Fatalf("ListSubnetPoolsWithLogger: %v", err)
				}
				if len(entries) != count {
					b.Fatalf("ListSubnetPoolsWithLogger returned %d entries, want %d", len(entries), count)
				}
			}
		})
	}
}

func benchmarkSubnetPoolRegistry(b *testing.B, count int) string {
	b.Helper()
	pools := make(map[string]string, count)
	for index := range count {
		addr := netip.AddrFrom4([4]byte{
			10,
			byte(index >> 16),
			byte(index >> 8),
			byte(index),
		})
		pools[addr.String()+"/32"] = "benchmark-site"
	}
	content, err := json.Marshal(map[string]any{"subnetPools": pools})
	if err != nil {
		b.Fatalf("marshaling benchmark registry: %v", err)
	}
	path := filepath.Join(b.TempDir(), "tail-ipam.hujson")
	if writeErr := os.WriteFile(path, content, 0o600); writeErr != nil {
		b.Fatalf("writing benchmark registry: %v", writeErr)
	}
	return path
}
