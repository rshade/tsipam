package registry_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

// TestLoadRejectsControlCharactersInStoredMetadata is the regression test for
// Issue 5: a control character embedded in a stored owner/description/ticket
// field used to reach human-readable output completely unvalidated -- only
// the site/service name went through NormalizeSiteName/NormalizeServiceName's
// rejectControlChars call. The error is also asserted to not be an
// *InvalidInputError: this is stored-state corruption, not caller input.
func TestLoadRejectsControlCharactersInStoredMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		list    func(path string) error
	}{
		{
			name:    "site ID owner",
			content: `{"siteIDs":{"1":{"site":"hq","owner":"bad\u0007owner"}}}` + "\n",
			list: func(path string) error {
				_, err := registry.ListSiteIDsWithLogger(path, nil)
				return err
			},
		},
		{
			name:    "subnet pool description",
			content: `{"subnetPools":{"10.0.0.0/24":{"site":"hq","description":"bad\u0007desc"}}}` + "\n",
			list: func(path string) error {
				_, err := registry.ListSubnetPoolsWithLogger(path, nil)
				return err
			},
		},
		{
			name:    "service address ticket",
			content: `{"serviceAddresses":{"100.100.100.7":{"service":"svc:payments","ticket":"OPS\u00071"}}}` + "\n",
			list: func(path string) error {
				_, err := registry.ListServiceAddressesWithLogger(path, nil)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("seeding fixture: %v", err)
			}

			err := tc.list(path)
			if err == nil {
				t.Fatal("error = nil, want control-character rejection")
			}
			if !strings.Contains(err.Error(), "control characters") {
				t.Errorf("error = %v, want it to mention control characters", err)
			}
			if _, ok := errors.AsType[*registry.InvalidInputError](err); ok {
				t.Error("error chain contains *InvalidInputError, but stored-state should strip the marker")
			}
		})
	}
}

func TestReserveSiteIDWithoutMetadataStaysBareString(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, _, err := registry.ReserveSiteIDWithLogger(
		path, "1", "hq-vpc", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(got), `"1": "hq-vpc"`) {
		t.Errorf("registry after a metadata-free reserve = %q, want the bare-string entry shape %q",
			got, `"1": "hq-vpc"`)
	}
}

func TestReserveSiteIDWithMetadataWritesObjectShapeAndAutoStamps(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	_, _, metadata, changed, err := registry.ReserveSiteIDWithLogger(
		path, "1", "hq-vpc",
		registry.ReservationMetadata{Owner: "alice", Ticket: "OPS-1234"}, nil,
	)
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false for a fresh metadata reservation, want true")
	}
	if metadata.Owner != "alice" || metadata.Ticket != "OPS-1234" {
		t.Errorf("metadata = %+v, want Owner=alice Ticket=OPS-1234", metadata)
	}
	if metadata.ReservedAt == "" {
		t.Error("metadata.ReservedAt is empty, want it auto-stamped")
	}
	if _, parseErr := time.Parse(time.RFC3339, metadata.ReservedAt); parseErr != nil {
		t.Errorf("metadata.ReservedAt = %q is not RFC3339: %v", metadata.ReservedAt, parseErr)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	// hujson.Format aligns each block's colons on its longest key, so the
	// exact spacing around "owner"/"alice" varies with sibling key lengths --
	// assert presence of each token rather than an exact substring.
	for _, want := range []string{`"owner"`, `"alice"`, `"ticket"`, `"OPS-1234"`, `"reservedAt"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("registry after a metadata reserve = %q, want it to contain %q", got, want)
		}
	}
}

func TestReserveSiteIDMetadataExplicitReservedAtIsNotOverwritten(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	const explicit = "2020-01-02T03:04:05Z"

	_, _, metadata, _, err := registry.ReserveSiteIDWithLogger(
		path, "1", "hq-vpc",
		registry.ReservationMetadata{Owner: "alice", ReservedAt: explicit}, nil,
	)
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if metadata.ReservedAt != explicit {
		t.Errorf(
			"metadata.ReservedAt = %q, want the explicit %q preserved, not auto-stamped",
			metadata.ReservedAt,
			explicit,
		)
	}
}

func TestReserveSiteIDMetadataMergeUpdatesOnlyGivenFields(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	_, _, first, _, err := registry.ReserveSiteIDWithLogger(
		path, "1", "hq-vpc", registry.ReservationMetadata{Owner: "alice"}, nil,
	)
	if err != nil {
		t.Fatalf("first ReserveSiteID: %v", err)
	}

	_, _, second, changed, err := registry.ReserveSiteIDWithLogger(
		path, "1", "hq-vpc", registry.ReservationMetadata{Ticket: "OPS-9"}, nil,
	)
	if err != nil {
		t.Fatalf("second ReserveSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false amending a stored reservation's ticket, want true")
	}
	if second.Owner != "alice" {
		t.Errorf("second.Owner = %q, want the untouched %q preserved by the merge", second.Owner, "alice")
	}
	if second.Ticket != "OPS-9" {
		t.Errorf("second.Ticket = %q, want %q", second.Ticket, "OPS-9")
	}
	if second.ReservedAt != first.ReservedAt {
		t.Errorf("second.ReservedAt = %q, want the original stamp %q left untouched by an amend",
			second.ReservedAt, first.ReservedAt)
	}
}

func TestReserveSiteIDMetadataIdenticalRepeatIsNoop(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	metadata := registry.ReservationMetadata{Owner: "alice", ReservedAt: "2020-01-02T03:04:05Z"}

	if _, _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", metadata, nil); err != nil {
		t.Fatalf("first ReserveSiteID: %v", err)
	}
	_, _, _, changed, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", metadata, nil)
	if err != nil {
		t.Fatalf("second ReserveSiteID: %v", err)
	}
	if changed {
		t.Error("changed = true on an exact repeat with identical metadata, want false")
	}
}

// TestReserveSiteIDBareRepeatDoesNotStampHandAuthoredMetadata guards the
// asymmetry called out in stampReservedAt's doc comment: a hand-edited entry
// that carries metadata but no reservedAt (an operator wrote "owner" by hand
// without a timestamp) must not have one silently backfilled by a later bare
// `reserve` of the same id+site -- that would turn an idempotent no-op into a
// surprise write.
func TestReserveSiteIDBareRepeatDoesNotStampHandAuthoredMetadata(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := `{"siteIDs":{"1":{"site":"hq-vpc","owner":"alice"}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	_, _, _, changed, err := registry.ReserveSiteIDWithLogger(
		path, "1", "hq-vpc", registry.ReservationMetadata{}, nil,
	)
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if changed {
		t.Error("changed = true on a bare reserve of an already-owned entry, want false")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(got) != content {
		t.Errorf("registry after a bare repeat reserve = %q, want it byte-identical to the seeded %q", got, content)
	}
}

func TestReserveSiteIDRefusesCollisionRegardlessOfMetadata(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, _, err := registry.ReserveSiteIDWithLogger(
		path, "1", "hq-vpc", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("first ReserveSiteID: %v", err)
	}
	_, _, _, _, err := registry.ReserveSiteIDWithLogger(
		path, "1", "branch-office", registry.ReservationMetadata{Owner: "alice"}, nil,
	)
	if _, ok := errors.AsType[*registry.SiteIDCollisionError](err); !ok {
		t.Fatalf("ReserveSiteID error = %v, want *SiteIDCollisionError even with metadata attached", err)
	}
}

func TestReserveSiteIDMetadataRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		metadata registry.ReservationMetadata
	}{
		{"control character in owner", registry.ReservationMetadata{Owner: "ali\nce"}},
		{"control character in description", registry.ReservationMetadata{Description: "line1\nline2"}},
		{"control character in ticket", registry.ReservationMetadata{Ticket: "OPS\t1"}},
		{"non-RFC3339 reserved-at", registry.ReservationMetadata{Owner: "alice", ReservedAt: "not-a-timestamp"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			if _, _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", tc.metadata, nil); err == nil {
				t.Errorf("ReserveSiteID(metadata=%+v) error = nil, want an error", tc.metadata)
			}
		})
	}
}

// TestReserveSiteIDWithMetadataPreservesHandWrittenComment mirrors
// TestReserveSiteIDPreservesHandWrittenComment, but adds metadata to the new
// entry -- the schema-evolution round trip called out as the riskiest part
// of this change: hujsonedit's comment-preserving patch mechanics must
// tolerate an object-shaped added value exactly as they already tolerate a
// bare string.
func TestReserveSiteIDWithMetadataPreservesHandWrittenComment(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := "{\n\t// reserved for the HQ migration, do not release before Q1\n\t\"siteIDs\": {\n\t\t\"1\": \"hq-vpc\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	if _, _, _, _, err := registry.ReserveSiteIDWithLogger(
		path, "2", "branch-office", registry.ReservationMetadata{Owner: "bob", Ticket: "OPS-2"}, nil,
	); err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	want := "// reserved for the HQ migration, do not release before Q1"
	if !strings.Contains(string(got), want) {
		t.Errorf("registry after reserving a metadata-bearing second entry = %q, want it to still contain %q",
			got, want)
	}
	if !strings.Contains(string(got), `"owner"`) || !strings.Contains(string(got), `"bob"`) {
		t.Errorf("registry = %q, want the new entry's metadata written", got)
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListSiteIDs = %+v, want 2 entries", entries)
	}
}

func TestListSiteIDsIncludesMetadataWhenPresent(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, _, err := registry.ReserveSiteIDWithLogger(
		path, "1", "hq-vpc", registry.ReservationMetadata{Owner: "alice", Description: "HQ gateway"}, nil,
	); err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if _, _, _, _, err := registry.ReserveSiteIDWithLogger(
		path, "2", "branch-office", registry.ReservationMetadata{}, nil,
	); err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListSiteIDs = %+v, want 2 entries", entries)
	}
	if entries[0].Owner != "alice" || entries[0].Description != "HQ gateway" {
		t.Errorf("entries[0] = %+v, want Owner=alice Description=%q", entries[0], "HQ gateway")
	}
	if entries[1].ReservationMetadata != (registry.ReservationMetadata{}) {
		t.Errorf("entries[1].ReservationMetadata = %+v, want zero for a metadata-free reservation", entries[1])
	}
}

// TestReserveSubnetPoolWithMetadataListsAndPreservesComment covers the same
// object-shape write, list round trip, and comment-preservation guarantee as
// the SiteID tests above, for the subnet-pool map -- both maps share Entry's
// marshal/merge/stamp mechanics (see entry.go), so this does not repeat every
// site-ID case, only the ones that exercise subnetpool.go's own call sites
// into that shared code.
func TestReserveSubnetPoolWithMetadataListsAndPreservesComment(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := "{\n\t// hq-vpc's primary block, do not reassign\n\t\"subnetPools\": {\n\t\t\"10.0.0.0/24\": \"hq-vpc\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	_, _, metadata, changed, err := registry.ReserveSubnetPoolWithLogger(
		path, "10.0.1.0/24", "branch-office",
		registry.ReservationMetadata{Owner: "carol", Ticket: "OPS-3"}, nil,
	)
	if err != nil {
		t.Fatalf("ReserveSubnetPool: %v", err)
	}
	if !changed || metadata.Owner != "carol" || metadata.ReservedAt == "" {
		t.Errorf(
			"ReserveSubnetPool result = (changed=%t, metadata=%+v), want changed=true, Owner=carol, and an auto-stamp",
			changed,
			metadata,
		)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(got), "// hq-vpc's primary block, do not reassign") {
		t.Errorf("registry after a metadata reserve = %q, want the hand-written comment preserved", got)
	}

	entries, err := registry.ListSubnetPoolsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSubnetPools: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListSubnetPools = %+v, want 2 entries", entries)
	}
	for _, entry := range entries {
		if entry.CIDR == "10.0.1.0/24" && (entry.Owner != "carol" || entry.Ticket != "OPS-3") {
			t.Errorf("ListSubnetPools entry for 10.0.1.0/24 = %+v, want Owner=carol Ticket=OPS-3", entry)
		}
	}
}
