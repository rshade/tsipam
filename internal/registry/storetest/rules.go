package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rshade/tsipam/internal/registry"
)

func ledgerFor(t *testing.T, newStore NewStore) *registry.Ledger {
	t.Helper()
	return registry.NewLedger(newStore(t), nil)
}

func runLedgerRules(t *testing.T, newStore NewStore) {
	t.Helper()
	runCollisions(t, newStore)
	runSubnetOverlaps(t, newStore)
	runIdempotentRepeats(t, newStore)
	runMetadataRules(t, newStore)
	runTicketReplay(t, newStore)
	runNextFree(t, newStore)
	runListOrdering(t, newStore)
	runReleases(t, newStore)
}

//nolint:gocognit,goconst // reservation fixtures repeat the same identities
func runCollisions(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("collision per map", func(t *testing.T) {
		t.Run("site ID", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			ctx := context.Background()
			if _, _, _, _, err := led.ReserveSiteID(
				ctx, contractTailnet, "1", "hq-vpc", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			_, _, _, _, err := led.ReserveSiteID(
				ctx, contractTailnet, "1", "branch-office", registry.ReservationMetadata{Owner: "alice"},
			)
			collision, ok := errors.AsType[*registry.SiteIDCollisionError](err)
			if !ok {
				t.Fatalf("error = %v, want *SiteIDCollisionError", err)
			}
			if collision.Existing != "hq-vpc" || collision.Wanted != "branch-office" {
				t.Errorf("collision = %+v, want Existing=hq-vpc Wanted=branch-office", collision)
			}
		})
		t.Run("service address", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			ctx := context.Background()
			if _, _, _, _, err := led.ReserveServiceAddress(
				ctx, contractTailnet, "100.100.100.7", "payments", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			_, _, _, changed, err := led.ReserveServiceAddress(
				ctx, contractTailnet, "100.100.100.7/32", "search",
				registry.ReservationMetadata{Owner: "platform"},
			)
			collision, ok := errors.AsType[*registry.ServiceAddressCollisionError](err)
			if !ok {
				t.Fatalf("error = %v, want *ServiceAddressCollisionError", err)
			}
			if collision.Address != "100.100.100.7" || collision.Existing != "svc:payments" ||
				collision.Wanted != "svc:search" {
				t.Errorf("collision = %+v, want canonical address and both services", collision)
			}
			want := `service address 100.100.100.7 is already reserved for "svc:payments", not "svc:search"`
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if changed {
				t.Error("changed = true on a refused collision, want false")
			}
		})
		t.Run("UDP port is scoped to site", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			ctx := context.Background()
			if _, _, _, _, err := led.ReserveUDPPort(
				ctx, contractTailnet, "branch-office", "41642", "branch-fw01",
			); err != nil {
				t.Fatal(err)
			}
			_, _, _, changed, err := led.ReserveUDPPort(
				ctx, contractTailnet, "branch-office", "41642", "branch-fw02",
			)
			collision, ok := errors.AsType[*registry.UDPPortCollisionError](err)
			if !ok {
				t.Fatalf("error = %v, want *UDPPortCollisionError", err)
			}
			if collision.Site != "branch-office" || collision.Port != "41642" ||
				collision.Existing != "branch-fw01" || collision.Wanted != "branch-fw02" {
				t.Errorf("collision = %+v, want canonical site/port and both devices", collision)
			}
			want := `UDP port 41642 in site "branch-office" is already reserved for "branch-fw01", not "branch-fw02"`
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if changed {
				t.Error("changed = true on a same-site collision, want false")
			}
			_, _, _, changed, err = led.ReserveUDPPort(
				ctx, contractTailnet, "hq-vpc", "41642", "core-router-2",
			)
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Error("cross-site reuse changed = false, want true")
			}
		})
	})
}

//nolint:gocognit,goconst // reservation fixtures repeat the same identities
func runSubnetOverlaps(t *testing.T, newStore NewStore) {
	t.Helper()
	cases := []struct {
		name          string
		existingCIDR  string
		candidateCIDR string
		candidateSite string
		wantMessage   string
		wantCollision bool
	}{
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
			name: "IPv4 and IPv4-mapped IPv6 spelling overlap", existingCIDR: "10.0.0.0/8",
			candidateCIDR: "::ffff:10.0.0.0/104", candidateSite: "branch-office",
			wantMessage: "subnet CIDR ::ffff:10.0.0.0/104 overlaps 10.0.0.0/8 reserved for " +
				`"hq-vpc", not "branch-office"`,
			wantCollision: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			ctx := context.Background()
			if _, _, _, _, err := led.ReserveSubnetPool(
				ctx, contractTailnet, tc.existingCIDR, "hq-vpc", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			_, _, _, changed, err := led.ReserveSubnetPool(
				ctx, contractTailnet, tc.candidateCIDR, tc.candidateSite, registry.ReservationMetadata{},
			)
			if tc.wantCollision {
				collision, ok := errors.AsType[*registry.SubnetPoolCollisionError](err)
				if !ok {
					t.Fatalf("error = %v, want *SubnetPoolCollisionError", err)
				}
				if collision.ExistingCIDR != tc.existingCIDR || collision.ExistingSite != "hq-vpc" ||
					collision.WantedSite != tc.candidateSite {
					t.Errorf("collision = %+v, want existing CIDR/site and wanted site", collision)
				}
				if err.Error() != tc.wantMessage {
					t.Errorf("error = %q, want %q", err, tc.wantMessage)
				}
				if changed {
					t.Error("changed = true on a refused overlap, want false")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Error("changed = false for an allowed reservation, want true")
			}
		})
	}
}

//nolint:gocognit // one subtest per map's idempotent repeat
func runIdempotentRepeats(t *testing.T, newStore NewStore) {
	t.Helper()
	ctx := context.Background()
	t.Run("idempotent repeat", func(t *testing.T) {
		t.Run("site ID", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			if _, _, _, _, err := led.ReserveSiteID(
				ctx, contractTailnet, "1", "hq-vpc", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			_, _, _, changed, err := led.ReserveSiteID(
				ctx, contractTailnet, "1", "hq-vpc", registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Error("changed = true on a repeat reservation, want false")
			}
		})
		t.Run("subnet pool", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			cidr, site, _, changed, err := led.ReserveSubnetPool(
				ctx, contractTailnet, "10.0.0.0/24", "  hq-vpc  ", registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if cidr != "10.0.0.0/24" || site != "hq-vpc" || !changed {
				t.Fatalf("reserve = (%q, %q, %t), want (10.0.0.0/24, hq-vpc, true)", cidr, site, changed)
			}
			_, _, _, changed, err = led.ReserveSubnetPool(
				ctx, contractTailnet, cidr, site, registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Error("changed = true on an exact repeat, want false")
			}
		})
		t.Run("service address", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			address, service, _, changed, err := led.ReserveServiceAddress(
				ctx, contractTailnet, "100.100.100.7/32", "payments", registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if address != "100.100.100.7" || service != "svc:payments" || !changed {
				t.Fatalf("reserve = (%q, %q, %t), want canonical address/service and changed",
					address, service, changed)
			}
			_, _, _, changed, err = led.ReserveServiceAddress(
				ctx, contractTailnet, address, "svc:payments", registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Error("changed = true on an equivalent repeat, want false")
			}
			if _, _, _, _, err = led.ReserveServiceAddress(
				ctx, contractTailnet, "100.100.100.8", "payments", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			entries, err := led.ListServiceAddresses(ctx, contractTailnet)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 || entries[0].Address != "100.100.100.7" || entries[1].Address != "100.100.100.8" {
				t.Errorf("entries = %+v, want two addresses for svc:payments", entries)
			}
		})
		t.Run("UDP port", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			if _, _, _, _, err := led.ReserveUDPPort(
				ctx, contractTailnet, "branch-office", "41642", "branch-fw01",
			); err != nil {
				t.Fatal(err)
			}
			_, _, _, changed, err := led.ReserveUDPPort(
				ctx, contractTailnet, " branch-office ", "041642", " branch-fw01 ",
			)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Error("changed = true on an exact repeat, want false")
			}
		})
	})
}

//nolint:gocognit,goconst // reservation fixtures repeat the same identities
func runMetadataRules(t *testing.T, newStore NewStore) {
	t.Helper()
	fixed := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	wantStamp := fixed.Format(time.RFC3339)
	t.Run("metadata merge and reservedAt", func(t *testing.T) {
		t.Parallel()
		led := ledgerFor(t, newStore)
		led.SetNow(func() time.Time { return fixed })
		ctx := context.Background()
		_, _, first, changed, err := led.ReserveSiteID(
			ctx, contractTailnet, "1", "hq-vpc", registry.ReservationMetadata{Owner: "alice"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !changed || first.ReservedAt != wantStamp || first.Owner != "alice" {
			t.Fatalf("first = %+v changed=%t, want owner alice and stamp %s", first, changed, wantStamp)
		}
		_, _, second, changed, err := led.ReserveSiteID(
			ctx, contractTailnet, "1", "hq-vpc", registry.ReservationMetadata{Ticket: "OPS-9"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !changed || second.Owner != "alice" || second.Ticket != "OPS-9" || second.ReservedAt != first.ReservedAt {
			t.Errorf("merged = %+v changed=%t, want owner and stamp preserved and ticket set", second, changed)
		}
		merged := registry.ReservationMetadata{Owner: "alice", Ticket: "OPS-9", ReservedAt: wantStamp}
		_, _, _, changed, err = led.ReserveSiteID(ctx, contractTailnet, "1", "hq-vpc", merged)
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			t.Error("changed = true on an identical metadata repeat, want false")
		}
		const explicit = "2020-01-02T03:04:05Z"
		_, _, stamped, _, err := led.ReserveSiteID(
			ctx, contractTailnet, "2", "branch",
			registry.ReservationMetadata{Owner: "bob", ReservedAt: explicit},
		)
		if err != nil {
			t.Fatal(err)
		}
		if stamped.ReservedAt != explicit {
			t.Errorf("ReservedAt = %q, want the explicit %q left as written", stamped.ReservedAt, explicit)
		}
	})
	t.Run("bare repeat does not stamp hand-authored metadata", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		ctx := context.Background()
		opts := registry.TxOptions{CreateIfMissing: true}
		err := store.Update(ctx, contractTailnet, opts, func(tx registry.Tx) error {
			return tx.PutSiteID("1", registry.Entry{Site: "hq-vpc", Owner: "alice"})
		})
		if err != nil {
			t.Fatal(err)
		}
		led := registry.NewLedger(store, nil)
		led.SetNow(func() time.Time { return fixed })
		_, _, meta, changed, err := led.ReserveSiteID(
			ctx, contractTailnet, "1", "hq-vpc", registry.ReservationMetadata{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if changed || meta.Owner != "alice" || meta.ReservedAt != "" {
			t.Errorf("repeat = %+v changed=%t, want the stored owner and no backfilled stamp", meta, changed)
		}
	})
}

//nolint:gocognit,goconst,mnd // reservation fixtures repeat identities and prefix lengths
func runTicketReplay(t *testing.T, newStore NewStore) {
	t.Helper()
	ctx := context.Background()
	t.Run("ticket replay", func(t *testing.T) {
		t.Run("returns the existing block and ignores new metadata", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			meta := registry.ReservationMetadata{Ticket: "OPS-1", Owner: "alice"}
			first, _, firstMeta, changed, err := led.AllocateSubnetPool(
				ctx, contractTailnet, "10.0.0.0/16", 24, "hq-vpc", nil, meta,
			)
			if err != nil || !changed {
				t.Fatalf("first = (%q, changed=%t, err=%v), want a fresh block", first, changed, err)
			}
			second, site, secondMeta, changed, err := led.AllocateSubnetPool(
				ctx, contractTailnet, "10.0.0.0/16", 24, "hq-vpc", nil,
				registry.ReservationMetadata{Ticket: "OPS-1", Description: "retry"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if second != first || site != "hq-vpc" || changed || secondMeta != firstMeta {
				t.Errorf("replay = (%q, %q, %+v, changed=%t), want (%q, hq-vpc, %+v, false)",
					second, site, secondMeta, changed, first, firstMeta)
			}
		})
		t.Run("without a ticket allocates again", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			first, _, _, _, err := led.AllocateSubnetPool(
				ctx, contractTailnet, "10.0.0.0/16", 24, "hq-vpc", nil, registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			second, _, _, changed, err := led.AllocateSubnetPool(
				ctx, contractTailnet, "10.0.0.0/16", 24, "hq-vpc", nil, registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if second == first || !changed {
				t.Errorf("second = (%q, changed=%t), want a block other than %q", second, changed, first)
			}
		})
		cases := []struct {
			name string
			seed []struct{ cidr, site, ticket string }
			site string
			want string
		}{
			{
				name: "same ticket under another site is not a replay",
				seed: []struct{ cidr, site, ticket string }{{"10.0.1.0/24", "hq-vpc", "OPS-1"}},
				site: "branch", want: "10.0.0.0/24",
			},
			{
				name: "same ticket outside the pool is not a replay",
				seed: []struct{ cidr, site, ticket string }{{"192.168.0.0/24", "hq-vpc", "OPS-1"}},
				site: "hq-vpc", want: "10.0.0.0/24",
			},
			{
				name: "lowest matching block wins",
				seed: []struct{ cidr, site, ticket string }{
					{"10.0.9.0/24", "hq-vpc", "OPS-1"},
					{"10.0.3.0/24", "hq-vpc", "OPS-1"},
				},
				site: "hq-vpc", want: "10.0.3.0/24",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				led := ledgerFor(t, newStore)
				for _, seed := range tc.seed {
					if _, _, _, _, err := led.ReserveSubnetPool(
						ctx, contractTailnet, seed.cidr, seed.site,
						registry.ReservationMetadata{Ticket: seed.ticket},
					); err != nil {
						t.Fatal(err)
					}
				}
				got, _, _, _, err := led.AllocateSubnetPool(
					ctx, contractTailnet, "10.0.0.0/16", 24, tc.site, nil,
					registry.ReservationMetadata{Ticket: "OPS-1"},
				)
				if err != nil {
					t.Fatal(err)
				}
				if got != tc.want {
					t.Errorf("allocated %s, want %s", got, tc.want)
				}
			})
		}
	})
}

//nolint:gocognit,goconst,mnd // reservation fixtures repeat identities and prefix lengths
func runNextFree(t *testing.T, newStore NewStore) {
	t.Helper()
	ctx := context.Background()
	t.Run("next free", func(t *testing.T) {
		t.Run("first child", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			cidr, site, _, changed, err := led.AllocateSubnetPool(
				ctx, contractTailnet, "10.0.0.0/16", 24, "  hq-vpc  ", nil, registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if cidr != "10.0.0.0/24" || site != "hq-vpc" || !changed {
				t.Errorf("allocated = (%q, %q, %t), want (10.0.0.0/24, hq-vpc, true)", cidr, site, changed)
			}
		})
		normalize := []struct {
			name string
			pool string
			size int
			want string
		}{
			{name: "host bits are masked", pool: "10.0.0.1/16", size: 24, want: "10.0.0.0/24"},
			{name: "IPv4-mapped pool allocates IPv4", pool: "::ffff:10.0.0.0/112", size: 24, want: "10.0.0.0/24"},
			{name: "IPv6 pool allocates IPv6", pool: "2001:db8::/32", size: 48, want: "2001:db8::/48"},
		}
		for _, tc := range normalize {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, _, _, _, err := ledgerFor(t, newStore).AllocateSubnetPool(
					ctx, contractTailnet, tc.pool, tc.size, "hq-vpc", nil, registry.ReservationMetadata{},
				)
				if err != nil {
					t.Fatal(err)
				}
				if got != tc.want {
					t.Errorf("AllocateSubnetPool(%q, /%d) = %s, want %s", tc.pool, tc.size, got, tc.want)
				}
			})
		}
		t.Run("skips registry and live exclusions", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			if _, _, _, _, err := led.ReserveSubnetPool(
				ctx, contractTailnet, "10.0.0.0/24", "branch", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			cidr, _, _, _, err := led.AllocateSubnetPool(
				ctx, contractTailnet, "10.0.0.0/16", 24, "hq-vpc",
				[]string{"10.0.1.0/24"}, registry.ReservationMetadata{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if cidr != "10.0.2.0/24" {
				t.Errorf("allocated %s, want 10.0.2.0/24", cidr)
			}
		})
		t.Run("exhaustion", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			if _, _, _, _, err := led.ReserveSubnetPool(
				ctx, contractTailnet, "10.0.0.0/25", "hq-vpc", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			_, _, _, _, err := led.AllocateSubnetPool(
				ctx, contractTailnet, "10.0.0.0/24", 25, "branch",
				[]string{"10.0.0.128/25"}, registry.ReservationMetadata{},
			)
			exhausted, ok := errors.AsType[*registry.SubnetPoolExhaustedError](err)
			if !ok {
				t.Fatalf("error = %v, want *SubnetPoolExhaustedError", err)
			}
			if exhausted.Pool != "10.0.0.0/24" || exhausted.Size != 25 {
				t.Errorf("exhausted = %+v, want pool 10.0.0.0/24 size 25", exhausted)
			}
			if got, want := err.Error(), "no free /25 block in 10.0.0.0/24"; got != want {
				t.Errorf("Error() = %q, want %q", got, want)
			}
			entries, listErr := led.ListSubnetPools(ctx, contractTailnet)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(entries) != 1 || entries[0].CIDR != "10.0.0.0/25" {
				t.Errorf("entries = %+v, want only the seeded reservation", entries)
			}
		})
	})
}

//nolint:gocognit,gocyclo,cyclop,funlen // one table covers every map's sort
func runListOrdering(t *testing.T, newStore NewStore) {
	t.Helper()
	ctx := context.Background()
	t.Run("list ordering", func(t *testing.T) {
		t.Run("site IDs sort numerically", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			for _, id := range []string{"10", "2", "1"} {
				if _, _, _, _, err := led.ReserveSiteID(
					ctx, contractTailnet, id, "hq-vpc", registry.ReservationMetadata{},
				); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := led.ListSiteIDs(ctx, contractTailnet)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"1", "2", "10"}
			if len(entries) != len(want) {
				t.Fatalf("entries = %+v, want %v", entries, want)
			}
			for i := range want {
				if entries[i].ID != want[i] {
					t.Errorf("entries[%d].ID = %q, want %q", i, entries[i].ID, want[i])
				}
			}
		})
		t.Run("subnet pools sort by network address", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			for _, cidr := range []string{"192.168.0.0/16", "10.0.2.0/24", "10.0.1.0/24", "2001:db8::/48"} {
				if _, _, _, _, err := led.ReserveSubnetPool(
					ctx, contractTailnet, cidr, "hq-vpc", registry.ReservationMetadata{},
				); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := led.ListSubnetPools(ctx, contractTailnet)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"10.0.1.0/24", "10.0.2.0/24", "192.168.0.0/16", "2001:db8::/48"}
			if len(entries) != len(want) {
				t.Fatalf("entries = %+v, want %v", entries, want)
			}
			for i := range want {
				if entries[i].CIDR != want[i] {
					t.Errorf("entries[%d].CIDR = %q, want %q", i, entries[i].CIDR, want[i])
				}
			}
		})
		t.Run("service addresses sort numerically", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			for _, address := range []string{"100.100.100.10", "100.100.100.2", "100.100.100.1"} {
				if _, _, _, _, err := led.ReserveServiceAddress(
					ctx, contractTailnet, address, "svc", registry.ReservationMetadata{},
				); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := led.ListServiceAddresses(ctx, contractTailnet)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"100.100.100.1", "100.100.100.2", "100.100.100.10"}
			if len(entries) != len(want) {
				t.Fatalf("entries = %+v, want %v", entries, want)
			}
			for i := range want {
				if entries[i].Address != want[i] {
					t.Errorf("entries[%d].Address = %q, want %q", i, entries[i].Address, want[i])
				}
			}
		})
		t.Run("UDP ports sort by site then numeric port and filter", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			for _, reservation := range []struct{ site, port, device string }{
				{site: "hq", port: "41650", device: "hq-c"},
				{site: "branch", port: "41642", device: "branch-b"},
				{site: "hq", port: "1024", device: "hq-a"},
				{site: "branch", port: "10240", device: "branch-a"},
			} {
				if _, _, _, _, err := led.ReserveUDPPort(
					ctx, contractTailnet, reservation.site, reservation.port, reservation.device,
				); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := led.ListUDPPorts(ctx, contractTailnet, "")
			if err != nil {
				t.Fatal(err)
			}
			want := []struct{ site, port string }{
				{site: "branch", port: "10240"},
				{site: "branch", port: "41642"},
				{site: "hq", port: "1024"},
				{site: "hq", port: "41650"},
			}
			if len(entries) != len(want) {
				t.Fatalf("entries = %+v, want %d", entries, len(want))
			}
			for i := range want {
				if entries[i].Site != want[i].site || entries[i].Port != want[i].port {
					t.Errorf("entry %d = %+v, want site=%q port=%q", i, entries[i], want[i].site, want[i].port)
				}
			}
			filtered, err := led.ListUDPPorts(ctx, contractTailnet, " hq ")
			if err != nil {
				t.Fatal(err)
			}
			if len(filtered) != 2 || filtered[0].Port != "1024" || filtered[1].Port != "41650" {
				t.Errorf("filtered = %+v, want hq ports 1024, 41650", filtered)
			}
			empty, err := led.ListUDPPorts(ctx, contractTailnet, "unknown")
			if err != nil {
				t.Fatal(err)
			}
			if len(empty) != 0 {
				t.Errorf("unknown-site entries = %+v, want empty", empty)
			}
		})
	})
}

//nolint:gocognit,gocyclo,cyclop,funlen // one table covers every map's release
func runReleases(t *testing.T, newStore NewStore) {
	t.Helper()
	ctx := context.Background()
	t.Run("release", func(t *testing.T) {
		t.Run("site ID removes an entry and an absent id is a no-op", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			if _, _, _, _, err := led.ReserveSiteID(
				ctx, contractTailnet, "1", "hq-vpc", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			id, changed, err := led.ReleaseSiteID(ctx, contractTailnet, "1")
			if err != nil {
				t.Fatal(err)
			}
			if id != "1" || !changed {
				t.Errorf("release = (%q, %t), want (1, true)", id, changed)
			}
			entries, err := led.ListSiteIDs(ctx, contractTailnet)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("entries = %+v, want empty after release", entries)
			}
			if _, _, _, _, err = led.ReserveSiteID(
				ctx, contractTailnet, "1", "hq-vpc", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			_, changed, err = led.ReleaseSiteID(ctx, contractTailnet, "2")
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Error("changed = true releasing an id that was never reserved, want false")
			}
		})
		t.Run("subnet pool removes an entry and an absent CIDR is a no-op", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			if _, _, _, _, err := led.ReserveSubnetPool(
				ctx, contractTailnet, "10.0.0.0/24", "hq-vpc", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			cidr, changed, err := led.ReleaseSubnetPool(ctx, contractTailnet, "10.0.0.0/24")
			if err != nil {
				t.Fatal(err)
			}
			if cidr != "10.0.0.0/24" || !changed {
				t.Errorf("release = (%q, %t), want (10.0.0.0/24, true)", cidr, changed)
			}
			_, changed, err = led.ReleaseSubnetPool(ctx, contractTailnet, "10.0.0.0/24")
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Error("changed = true releasing an absent CIDR, want false")
			}
		})
		t.Run("service address removes an entry and an absent address is a no-op", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			if _, _, _, _, err := led.ReserveServiceAddress(
				ctx, contractTailnet, "100.100.100.7", "payments", registry.ReservationMetadata{},
			); err != nil {
				t.Fatal(err)
			}
			address, changed, err := led.ReleaseServiceAddress(ctx, contractTailnet, "100.100.100.7/32")
			if err != nil {
				t.Fatal(err)
			}
			if address != "100.100.100.7" || !changed {
				t.Errorf("release = (%q, %t), want canonical address and changed", address, changed)
			}
			_, changed, err = led.ReleaseServiceAddress(ctx, contractTailnet, "100.100.100.7")
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				t.Error("changed = true releasing an absent address, want false")
			}
		})
		t.Run("UDP port removes only the selected reservation", func(t *testing.T) {
			t.Parallel()
			led := ledgerFor(t, newStore)
			for _, reservation := range []struct{ site, port, device string }{
				{site: "branch", port: "41642", device: "branch-fw01"},
				{site: "branch", port: "41643", device: "branch-fw02"},
				{site: "hq", port: "41642", device: "hq-fw01"},
			} {
				if _, _, _, _, err := led.ReserveUDPPort(
					ctx, contractTailnet, reservation.site, reservation.port, reservation.device,
				); err != nil {
					t.Fatal(err)
				}
			}
			site, port, changed, err := led.ReleaseUDPPort(ctx, contractTailnet, " branch ", "041642")
			if err != nil {
				t.Fatal(err)
			}
			if site != "branch" || port != "41642" || !changed {
				t.Errorf("release = (%q, %q, %t), want (branch, 41642, true)", site, port, changed)
			}
			entries, err := led.ListUDPPorts(ctx, contractTailnet, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 || entries[0].Port != "41643" || entries[1].Site != "hq" {
				t.Errorf("entries = %+v, want branch/41643 and hq/41642", entries)
			}
		})
	})
}
