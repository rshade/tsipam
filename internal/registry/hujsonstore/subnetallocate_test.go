package hujsonstore_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
)

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
			_, _, _, _, err := ledgerAt(
				path,
			).AllocateSubnetPool(regCtx(), registryTailnet, tc.pool, tc.size, "hq-vpc", tc.liveTaken, registry.ReservationMetadata{})
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

	first, _, firstMeta, changed, err := ledgerAt(
		path,
	).AllocateSubnetPool(regCtx(), registryTailnet, "10.0.0.0/16", 24, "hq-vpc", nil, metadata)
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
	second, site, secondMeta, changed, err := ledgerAt(
		path,
	).AllocateSubnetPool(regCtx(), registryTailnet, "10.0.0.0/16", 24, "hq-vpc", nil, replayMetadata)
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
			got[i], _, _, _, errs[i] = ledgerAt(
				path,
			).AllocateSubnetPool(regCtx(), registryTailnet, "10.0.0.0/16", 24, fmt.Sprintf("site-%d", i+1), nil, registry.ReservationMetadata{})
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
	entries, err := ledgerAt(path).ListSubnetPools(regCtx(), registryTailnet)
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
	if _, _, _, _, err := ledgerAt(
		path,
	).ReserveSubnetPool(regCtx(), registryTailnet, "10.0.0.0/25", "hq-vpc", registry.ReservationMetadata{}); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}

	_, _, _, _, err = ledgerAt(
		path,
	).AllocateSubnetPool(regCtx(), registryTailnet, "10.0.0.0/24", 25, "branch", []string{"10.0.0.128/25"}, registry.ReservationMetadata{})
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

	cidr, _, _, _, err := ledgerAt(
		path,
	).AllocateSubnetPool(regCtx(), registryTailnet, "10.0.0.0/16", 24, "branch", nil, registry.ReservationMetadata{})
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
