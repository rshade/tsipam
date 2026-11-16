package registry_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
)

func TestAllocateSubnetPoolReservesFirstFreeChild(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	cidr, site, _, changed, err := registry.AllocateSubnetPoolWithLogger(
		path, "10.0.0.0/16", 24, "  hq-vpc  ", nil, registry.ReservationMetadata{}, nil,
	)
	if err != nil {
		t.Fatalf("AllocateSubnetPool: %v", err)
	}
	if cidr != "10.0.0.0/24" || site != "hq-vpc" || !changed {
		t.Errorf("AllocateSubnetPool = (%q, %q, %t), want (%q, %q, true)",
			cidr, site, changed, "10.0.0.0/24", "hq-vpc")
	}

	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(entries) != 1 || entries[0].CIDR != "10.0.0.0/24" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSubnetPools = %+v, want the allocated block", entries)
	}
}

func TestAllocateSubnetPoolNormalizesPoolLikeNextFree(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		pool string
		size int
		want string
	}{
		{name: "host bits are masked", pool: "10.0.0.1/16", size: 24, want: "10.0.0.0/24"},
		{name: "IPv4-mapped pool allocates IPv4", pool: "::ffff:10.0.0.0/112", size: 24, want: "10.0.0.0/24"},
		{name: "IPv6 pool allocates IPv6", pool: "2001:db8::/32", size: 48, want: "2001:db8::/48"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, _, _, err := registry.AllocateSubnetPoolWithLogger(
				regPath(t), tc.pool, tc.size, "hq-vpc", nil, registry.ReservationMetadata{}, nil,
			)
			if err != nil {
				t.Fatalf("AllocateSubnetPool(%q): %v", tc.pool, err)
			}
			if got != tc.want {
				t.Errorf("AllocateSubnetPool(%q, /%d) = %s, want %s", tc.pool, tc.size, got, tc.want)
			}
		})
	}
}

func TestAllocateSubnetPoolSkipsRegistryAndLiveExclusions(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if _, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
		path, "10.0.0.0/24", "branch", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}

	cidr, _, _, _, err := registry.AllocateSubnetPoolWithLogger(
		path, "10.0.0.0/16", 24, "hq-vpc", []string{"10.0.1.0/24"}, registry.ReservationMetadata{}, nil,
	)
	if err != nil {
		t.Fatalf("AllocateSubnetPool: %v", err)
	}
	if cidr != "10.0.2.0/24" {
		t.Errorf("allocated %s, want 10.0.2.0/24 (first child reserved by another site, second live)", cidr)
	}
}

func TestAllocateSubnetPoolRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		pool      string
		size      int
		liveTaken []string
		wantIn    string
	}{
		{name: "malformed pool", pool: "10.0.0.0", size: 24, wantIn: "not a valid prefix"},
		{name: "size wider than pool", pool: "10.0.0.0/24", size: 16, wantIn: "between pool /24"},
		{name: "size beyond family", pool: "10.0.0.0/24", size: 33, wantIn: "address length /32"},
		{
			name: "malformed live route", pool: "10.0.0.0/16", size: 24,
			liveTaken: []string{"bogus"}, wantIn: "live route",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			_, _, _, _, err := registry.AllocateSubnetPoolWithLogger(
				path, tc.pool, tc.size, "hq-vpc", tc.liveTaken, registry.ReservationMetadata{}, nil,
			)
			if _, ok := errors.AsType[*registry.InvalidInputError](err); !ok {
				t.Fatalf("error = %v, want *InvalidInputError", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantIn)
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("registry %s was created by a rejected allocation", path)
			}
		})
	}
}

func TestAllocateSubnetPoolTicketReplayReturnsExistingBlockUnchanged(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	metadata := registry.ReservationMetadata{Ticket: "OPS-1", Owner: "alice"}

	first, _, firstMeta, changed, err := registry.AllocateSubnetPoolWithLogger(
		path, "10.0.0.0/16", 24, "hq-vpc", nil, metadata, nil,
	)
	if err != nil || !changed {
		t.Fatalf("first AllocateSubnetPool = (%q, changed=%t, err=%v), want a fresh block", first, changed, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}

	// A replay that also changes description must not merge it in: the
	// replay is read-only.
	replayMetadata := registry.ReservationMetadata{Ticket: "OPS-1", Description: "retry"}
	second, site, secondMeta, changed, err := registry.AllocateSubnetPoolWithLogger(
		path, "10.0.0.0/16", 24, "hq-vpc", nil, replayMetadata, nil,
	)
	if err != nil {
		t.Fatalf("replay AllocateSubnetPool: %v", err)
	}
	if second != first || site != "hq-vpc" || changed {
		t.Errorf("replay = (%q, %q, changed=%t), want (%q, %q, false)", second, site, changed, first, "hq-vpc")
	}
	if secondMeta != firstMeta {
		t.Errorf("replay metadata = %+v, want the stored %+v", secondMeta, firstMeta)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("replay rewrote the registry:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestAllocateSubnetPoolWithoutTicketAllocatesAgain(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	first, _, _, _, err := registry.AllocateSubnetPoolWithLogger(
		path, "10.0.0.0/16", 24, "hq-vpc", nil, registry.ReservationMetadata{}, nil,
	)
	if err != nil {
		t.Fatalf("first AllocateSubnetPool: %v", err)
	}
	second, _, _, changed, err := registry.AllocateSubnetPoolWithLogger(
		path, "10.0.0.0/16", 24, "hq-vpc", nil, registry.ReservationMetadata{}, nil,
	)
	if err != nil {
		t.Fatalf("second AllocateSubnetPool: %v", err)
	}
	if second == first || !changed {
		t.Errorf("second allocation = (%q, changed=%t), want a block other than %q with changed=true",
			second, changed, first)
	}
}

func TestAllocateSubnetPoolTicketReplayScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		seed []struct{ cidr, site, ticket string }
		pool string
		site string
		want string
	}{
		{
			name: "same ticket under another site is not a replay",
			seed: []struct{ cidr, site, ticket string }{{"10.0.1.0/24", "hq-vpc", "OPS-1"}},
			pool: "10.0.0.0/16", site: "branch", want: "10.0.0.0/24",
		},
		{
			name: "same ticket outside the pool is not a replay",
			seed: []struct{ cidr, site, ticket string }{{"192.168.0.0/24", "hq-vpc", "OPS-1"}},
			pool: "10.0.0.0/16", site: "hq-vpc", want: "10.0.0.0/24",
		},
		{
			name: "lowest matching block wins",
			seed: []struct{ cidr, site, ticket string }{
				{"10.0.9.0/24", "hq-vpc", "OPS-1"},
				{"10.0.3.0/24", "hq-vpc", "OPS-1"},
			},
			pool: "10.0.0.0/16", site: "hq-vpc", want: "10.0.3.0/24",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			for _, s := range tc.seed {
				if _, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
					path, s.cidr, s.site, registry.ReservationMetadata{Ticket: s.ticket}, nil,
				); err != nil {
					t.Fatalf("seeding %s: %v", s.cidr, err)
				}
			}
			got, _, _, _, err := registry.AllocateSubnetPoolWithLogger(
				path, tc.pool, 24, tc.site, nil, registry.ReservationMetadata{Ticket: "OPS-1"}, nil,
			)
			if err != nil {
				t.Fatalf("AllocateSubnetPool: %v", err)
			}
			if got != tc.want {
				t.Errorf("allocated %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAllocateSubnetPoolConcurrentCallersReceiveDistinctBlocks(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	const workers = 10
	var wg sync.WaitGroup
	got := make([]string, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], _, _, _, errs[i] = registry.AllocateSubnetPoolWithLogger(
				path, "10.0.0.0/16", 24, fmt.Sprintf("site-%d", i+1), nil, registry.ReservationMetadata{}, nil,
			)
		}(i)
	}
	wg.Wait()

	seen := make(map[string]int, workers)
	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: AllocateSubnetPool: %v", i, err)
			continue
		}
		if prev, dup := seen[got[i]]; dup {
			t.Errorf("workers %d and %d both received %s", prev, i, got[i])
		}
		seen[got[i]] = i
	}
	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(entries) != workers {
		t.Errorf("ListSubnetPools returned %d entries, want %d", len(entries), workers)
	}
}

func TestAllocateSubnetPoolExhaustionWritesNothing(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if _, _, _, _, err := registry.ReserveSubnetPoolWithLogger(
		path, "10.0.0.0/25", "hq-vpc", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}

	_, _, _, _, err = registry.AllocateSubnetPoolWithLogger(
		path, "10.0.0.0/24", 25, "branch", []string{"10.0.0.128/25"}, registry.ReservationMetadata{}, nil,
	)
	var exhausted *registry.SubnetPoolExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("error = %v, want *SubnetPoolExhaustedError", err)
	}
	if exhausted.Pool != "10.0.0.0/24" || exhausted.Size != 25 {
		t.Errorf("exhausted = %+v, want pool 10.0.0.0/24 size 25", exhausted)
	}
	if got, want := err.Error(), "no free /25 block in 10.0.0.0/24"; got != want {
		t.Errorf("Error() = %q, want %q (calc next-free's wording)", got, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("exhaustion rewrote the registry:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestAllocateSubnetPoolPreservesHandWrittenCommentOnAnotherEntry(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := "{\n\t\"subnetPools\": {\n" +
		"\t\t// reserved for the HQ migration, do not release before Q1\n" +
		"\t\t\"10.0.0.0/24\": \"hq-vpc\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	cidr, _, _, _, err := registry.AllocateSubnetPoolWithLogger(
		path, "10.0.0.0/16", 24, "branch", nil, registry.ReservationMetadata{}, nil,
	)
	if err != nil {
		t.Fatalf("AllocateSubnetPool: %v", err)
	}
	if cidr != "10.0.1.0/24" {
		t.Errorf("allocated %s, want 10.0.1.0/24", cidr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	wantOriginal := "\t\t// reserved for the HQ migration, do not release before Q1\n\t\t\"10.0.0.0/24\": \"hq-vpc\",\n"
	if !strings.Contains(string(got), wantOriginal) {
		t.Errorf("registry after allocation = %q, want it to keep %q byte-for-byte", got, wantOriginal)
	}
}
