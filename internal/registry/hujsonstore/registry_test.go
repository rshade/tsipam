package hujsonstore_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

func TestReserveSiteIDCreatesFileWhenMissing(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	_, _, _, changed, err := ledgerAt(
		path,
	).ReserveSiteID(regCtx(), registryTailnet, "1", "hq-vpc", registry.ReservationMetadata{})
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for a fresh reservation")
	}

	entries, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSiteIDs = %+v, want [{1 hq-vpc}]", entries)
	}
}

func TestReserveSiteIDReturnsCanonicalIDAndSite(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	id, site, _, _, err := ledgerAt(path).ReserveSiteID(regCtx(), registryTailnet, "007",
		"  hq-vpc  ",
		registry.ReservationMetadata{},
	)
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if id != "7" || site != "hq-vpc" {
		t.Errorf("ReserveSiteID(%q, %q) = (%q, %q), want (%q, %q)", "007", "  hq-vpc  ", id, site, "7", "hq-vpc")
	}
}

func TestReserveSiteIDRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	cases := []struct {
		name string
		id   string
		site string
	}{
		{"non-numeric id", "abc", "hq-vpc"},
		{"negative id", "-1", "hq-vpc"},
		{"out of range id", "65536", "hq-vpc"},
		{"empty site name", "1", "   "},
		{"embedded newline in site name", "1", "hq\nvpc"},
		{"embedded tab in site name", "1", "hq\tvpc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, _, _, err := ledgerAt(path).ReserveSiteID(regCtx(), registryTailnet, tc.id,
				tc.site,
				registry.ReservationMetadata{},
			); err == nil {
				t.Errorf("ReserveSiteID(%q, %q) error = nil, want an error", tc.id, tc.site)
			}
		})
	}
}

func TestNormalizeSiteIDAcceptsBoundaryValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		want string
	}{
		{"zero is a valid site ID", "0", "0"},
		{"max 16-bit site ID", "65535", "65535"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := registry.NormalizeSiteID(tc.id)
			if err != nil {
				t.Fatalf("NormalizeSiteID(%q): %v", tc.id, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeSiteID(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestNormalizeSiteIDRejectsOutOfRangeValue(t *testing.T) {
	t.Parallel()

	if _, err := registry.NormalizeSiteID("65536"); err == nil {
		t.Error("NormalizeSiteID(\"65536\") error = nil, want an out-of-range error")
	}
}

func TestReleaseSiteIDOnMissingFileIsError(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, err := ledgerAt(path).ReleaseSiteID(regCtx(), registryTailnet, "1"); err == nil {
		t.Error("ReleaseSiteID on a missing registry file error = nil, want an error")
	}
}

func TestListSiteIDsOnMissingFileIsError(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet); err == nil {
		t.Error("ListSiteIDs on a missing registry file error = nil, want an error")
	}
}

func TestReserveSiteIDPreservesHandWrittenComment(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := "{\n\t// reserved for the HQ migration, do not release before Q1\n\t\"siteIDs\": {\n\t\t\"1\": \"hq-vpc\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	if _, _, _, _, err := ledgerAt(path).ReserveSiteID(regCtx(), registryTailnet, "2",
		"branch-office",
		registry.ReservationMetadata{},
	); err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	want := "// reserved for the HQ migration, do not release before Q1"
	if !strings.Contains(string(got), want) {
		t.Errorf("registry after reserving a second entry = %q, want it to still contain %q", got, want)
	}
}

// TestLoadAcceptsUnrelatedTopLevelKeyAndRoundTrips covers a genuinely
// unrelated top-level key (as opposed to a near-miss spelling of "siteIDs"
// -- see the rejection cases in TestLoadRejectsInvalidRegistryShapes). The
// registry is documented to grow additional top-level keys (subnetPools,
// per-service /32 allocations) in future versions, so Load must not reject
// an unrecognized field wholesale, and a reserve/release round trip must
// leave it completely untouched.
func TestLoadAcceptsUnrelatedTopLevelKeyAndRoundTrips(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := `{"subnetPools":{},"siteIDs":{"1":"hq-vpc"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	entries, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSiteIDs on a registry with an unrelated top-level key: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSiteIDs = %+v, want [{1 hq-vpc}]", entries)
	}

	if _, _, _, _, reserveErr := ledgerAt(path).ReserveSiteID(regCtx(), registryTailnet, "2",
		"branch-office",
		registry.ReservationMetadata{},
	); reserveErr != nil {
		t.Fatalf("ReserveSiteID: %v", reserveErr)
	}
	if _, _, releaseErr := ledgerAt(path).ReleaseSiteID(regCtx(), registryTailnet, "1"); releaseErr != nil {
		t.Fatalf("ReleaseSiteID: %v", releaseErr)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(got), "subnetPools") {
		t.Errorf("registry after a reserve/release round trip = %q, want the untouched %q key still present",
			got, "subnetPools")
	}
}

func TestLoadRejectsAmbiguousDuplicateSiteIDKeys(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"siteIDs":{"01":"hq-vpc","1":"branch-office"}}`), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	_, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err == nil {
		t.Fatal("ListSiteIDs on a registry with ambiguous duplicate site-ID keys: error = nil, want an error")
	}
	if !strings.Contains(err.Error(), `"01"`) || !strings.Contains(err.Error(), `"1"`) {
		t.Errorf("error = %v, want it to name both spellings %q and %q", err, "01", "1")
	}
}

func TestReserveSiteIDCollidesAgainstUnNormalizedFileKey(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"siteIDs":{"01":"hq-vpc"}}`), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	_, _, _, _, err := ledgerAt(
		path,
	).ReserveSiteID(regCtx(), registryTailnet, "1", "branch-office", registry.ReservationMetadata{})
	var collision *registry.SiteIDCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("ReserveSiteID(%q, ...) error = %v, want *SiteIDCollisionError against the un-normalized file key %q",
			"1", err, "01")
	}
	if collision.Existing != "hq-vpc" {
		t.Errorf("collision.Existing = %q, want %q", collision.Existing, "hq-vpc")
	}
}

// TestReserveSiteIDAmendingUnNormalizedFileKeyDoesNotDuplicate is the
// regression test for Issue 2: ReserveSiteIDWithLogger used to patch the
// canonical id ("1") even when the file's raw key was a different, valid
// spelling ("01"). hujson's patch matching is exact (see Registry's doc
// comment), so that add landed as a second, duplicate member rather than
// updating the first -- corrupting the registry file while still reporting
// success.
func TestReserveSiteIDAmendingUnNormalizedFileKeyDoesNotDuplicate(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"siteIDs":{"01":"hq"}}`+"\n"), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	id, site, _, changed, err := ledgerAt(
		path,
	).ReserveSiteID(regCtx(), registryTailnet, "1", "hq", registry.ReservationMetadata{Owner: "ops"})
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if id != "1" || site != "hq" || !changed {
		t.Errorf("ReserveSiteID = (%q, %q, %t), want (1, hq, true)", id, site, changed)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var decoded struct {
		SiteIDs map[string]json.RawMessage `json:"siteIDs"`
	}
	if unmarshalErr := json.Unmarshal(got, &decoded); unmarshalErr != nil {
		t.Fatalf("decoding registry after reserve: %v", unmarshalErr)
	}
	if len(decoded.SiteIDs) != 1 {
		t.Errorf("registry after amending an un-normalized key = %s, want exactly one siteIDs member, got %d",
			got, len(decoded.SiteIDs))
	}

	if _, loadErr := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet); loadErr != nil {
		t.Errorf("ListSiteIDs after amending an un-normalized key: %v, want the registry to remain loadable", loadErr)
	}
}

// TestRawEntriesAlwaysReturnsTheMapKey is the regression test for Issue 1:
// rawReservationMapMember used to silently drop a map key when the entry
// object either lacked its subject field or carried a non-string value for
// it -- exactly the shape RawEntries feeds into the sanitizer's identity
// inventory (see registerKnownIdentityInventory in cmd/tsipam/main.go), so
// a dropped key meant the value it named was never registered as a sweep
// candidate even though Load's own error text names it regardless.
func TestRawEntriesAlwaysReturnsTheMapKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		entryJSON   string
		wantSubject string
	}{
		{name: "string entry", entryJSON: `"svc:payments"`, wantSubject: "svc:payments"},
		{
			name:        "object entry with subject field",
			entryJSON:   `{"service":"svc:payments","owner":"ops"}`,
			wantSubject: "svc:payments",
		},
		{name: "object entry without subject field", entryJSON: `{"owner":"ops"}`, wantSubject: ""},
		{
			name:        "object entry with non-string subject field",
			entryJSON:   `{"service":42,"owner":"ops"}`,
			wantSubject: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			content := fmt.Sprintf(`{"serviceAddresses":{"100.100.100.7":%s}}`, tc.entryJSON)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("seeding fixture: %v", err)
			}

			_, _, serviceAddresses, _ := hujsonstore.RawEntries(path)
			subject, ok := serviceAddresses["100.100.100.7"]
			if !ok {
				t.Fatalf("RawEntries serviceAddresses = %+v, want key %q present", serviceAddresses, "100.100.100.7")
			}
			if subject != tc.wantSubject {
				t.Errorf("RawEntries serviceAddresses[%q] = %q, want %q", "100.100.100.7", subject, tc.wantSubject)
			}
		})
	}
}

func TestReleaseSiteIDRemovesUnNormalizedFileKey(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"siteIDs":{"01":"hq-vpc","2":"branch-office"}}`), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	id, changed, err := ledgerAt(path).ReleaseSiteID(regCtx(), registryTailnet, "1")
	if err != nil {
		t.Fatalf("ReleaseSiteID: %v", err)
	}
	if !changed || id != "1" {
		t.Errorf("ReleaseSiteID(%q) = (id=%q, changed=%v), want (1, true)", "1", id, changed)
	}

	entries, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "2" || entries[0].Site != "branch-office" {
		t.Errorf("ListSiteIDs after release = %+v, want only the untouched entry [{2 branch-office}]", entries)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if strings.Contains(string(got), `"01"`) {
		t.Errorf("registry after releasing the raw key %q still contains it: %q", "01", got)
	}
}

func TestReserveSiteIDConcurrentDistinctIDsLosesNoReservation(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	const workers = 20
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := strconv.Itoa(i + 1)
			site := fmt.Sprintf("site-%d", i+1)
			_, _, _, _, err := ledgerAt(
				path,
			).ReserveSiteID(regCtx(), registryTailnet, id, site, registry.ReservationMetadata{})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: ReserveSiteID: %v", i, err)
		}
	}

	entries, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != workers {
		t.Fatalf("ListSiteIDs returned %d entries, want %d (a concurrent reservation was lost)", len(entries), workers)
	}
	seen := make(map[string]bool, workers)
	for _, entry := range entries {
		if seen[entry.ID] {
			t.Errorf("duplicate entry for site ID %s in %+v", entry.ID, entries)
		}
		seen[entry.ID] = true
	}
}

// TestLoadRejectsInvalidRegistryShapes covers document shapes where the
// top-level "siteIDs" member's syntactic state -- absent, null, present as
// an object, spelled differently, or duplicated -- cannot be told apart from
// a decoded Go map alone, plus siteIDs entries that are individually
// invalid. See Load's doc comment for why the resolution walks the parsed
// HuJSON syntax tree instead.
func TestLoadRejectsInvalidRegistryShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		wantIn  []string
	}{
		{
			name:    "case-variant top-level key alone, empty block",
			content: `{"siteids":{}}`,
			wantIn:  []string{`"siteids"`, `"siteIDs"`},
		},
		{
			name:    "case-variant top-level key alone, null block",
			content: `{"siteids":null}`,
			wantIn:  []string{`"siteids"`, `"siteIDs"`},
		},
		{
			name:    "ambiguous case-insensitive duplicate top-level keys",
			content: `{"siteids":{},"siteIDs":{"1":"hq-vpc"}}`,
			wantIn:  []string{`"siteids"`, `"siteIDs"`},
		},
		{
			name:    "duplicate exact top-level key",
			content: `{"siteIDs":{"1":"a"},"siteIDs":{"2":"b"}}`,
			wantIn:  []string{"multiple top-level keys", `"siteIDs"`},
		},
		{
			name:    "duplicate identical entry key",
			content: `{"siteIDs":{"1":"first","1":"second"}}`,
			wantIn:  []string{"duplicate", `"1"`},
		},
		{
			name:    "null entry value",
			content: `{"siteIDs":{"1":null}}`,
			wantIn:  []string{"1", "empty or null"},
		},
		{
			name:    "empty string entry value",
			content: `{"siteIDs":{"1":""}}`,
			wantIn:  []string{"1", "empty or null"},
		},
		{
			name:    "UTF-8 byte-order mark prefix",
			content: "\ufeff{}",
			wantIn:  []string{"byte-order mark"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("seeding fixture: %v", err)
			}

			_, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
			if err == nil {
				t.Fatalf("ListSiteIDs(%q) error = nil, want an error", tc.content)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestReserveSiteIDIntoExistingEmptyBlock covers reserving into a siteIDs
// block that is present on file as an empty object, as opposed to being
// entirely absent -- the two must not be conflated (see
// Registry.siteIDsPresent's doc comment).
func TestReserveSiteIDIntoExistingEmptyBlock(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"siteIDs":{}}`+"\n"), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	_, _, _, changed, err := ledgerAt(
		path,
	).ReserveSiteID(regCtx(), registryTailnet, "1", "hq-vpc", registry.ReservationMetadata{})
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for a fresh reservation")
	}

	entries, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSiteIDs = %+v, want [{1 hq-vpc}]", entries)
	}
}

// TestReserveSiteIDPreservesCommentInEmptyBlock is the reproduction for
// Regression A: a hand-written comment inside an existing empty
// "siteIDs": {} block used to be silently destroyed by ReserveSiteID,
// because the write path could not distinguish "block absent" from "block
// present but empty" and took a whole-object-replace path in both cases.
func TestReserveSiteIDPreservesCommentInEmptyBlock(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := "{\n\t\"siteIDs\": {\n\t\t// reserved but not yet assigned\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	if _, _, _, _, err := ledgerAt(path).ReserveSiteID(regCtx(), registryTailnet, "1",
		"hq-vpc",
		registry.ReservationMetadata{},
	); err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	want := "// reserved but not yet assigned"
	if !strings.Contains(string(got), want) {
		t.Errorf("registry after reserving into a previously-empty siteIDs block = %q, want it to still contain %q",
			got, want)
	}
}

// TestReserveSiteIDIntoNullBlock covers reserving into a siteIDs member
// present on file as an explicit JSON null, which must still take the
// whole-object-add path (there is no existing object node to add a key
// into).
func TestReserveSiteIDIntoNullBlock(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"siteIDs":null}`+"\n"), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	_, _, _, changed, err := ledgerAt(
		path,
	).ReserveSiteID(regCtx(), registryTailnet, "1", "hq-vpc", registry.ReservationMetadata{})
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for a fresh reservation")
	}

	entries, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSiteIDs = %+v, want [{1 hq-vpc}]", entries)
	}
}

func TestRawEntriesReadsCaseInsensitiveSubject(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := `{"siteIDs":{"1":{"Site":"private-site","Owner":"ops"}},` +
		`"serviceAddresses":{"100.64.0.1":{"Service":"svc:search"}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	siteIDs, _, serviceAddresses, owners := hujsonstore.RawEntries(path)
	if siteIDs["1"] != "private-site" {
		t.Fatalf("RawEntries siteIDs = %+v, want subject private-site", siteIDs)
	}
	if serviceAddresses["100.64.0.1"] != "svc:search" {
		t.Fatalf("RawEntries serviceAddresses = %+v, want subject svc:search", serviceAddresses)
	}
	if len(owners) != 1 || owners[0] != "ops" {
		t.Fatalf("RawEntries owners = %q, want [ops]", owners)
	}
	entries, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Site != "private-site" {
		t.Fatalf("ListSiteIDs = %+v, want the same subject RawEntries collected", entries)
	}
}

// TestRawEntriesExtractsUnvalidatedValuesFromFile tests that RawEntries
// can extract raw key/value pairs even from files that would fail Load
// validation, so a caller can pre-register them with a sanitizer.
func TestRawEntriesExtractsUnvalidatedValuesFromFile(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	content := `{"siteIDs":{"1":{"site":"hq-vpc","owner":"ops"}},` +
		`"subnetPools":{"10.0.0.0/16":"secret-hq","10.0.1.0/24":"secret-branch"},` +
		`"serviceAddresses":{"100.100.100.7":"svc:payments",` +
		`"100.100.100.8":{"service":"svc:search","owner":"platform"}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	siteIDs, subnetPools, serviceAddresses, owners := hujsonstore.RawEntries(path)
	if len(siteIDs) != 1 || siteIDs["1"] != "hq-vpc" {
		t.Errorf("RawEntries siteIDs = %+v, want {1:hq-vpc}", siteIDs)
	}
	if len(subnetPools) != 2 || subnetPools["10.0.0.0/16"] != "secret-hq" ||
		subnetPools["10.0.1.0/24"] != "secret-branch" {
		t.Errorf("RawEntries subnetPools = %+v, want both entries verbatim", subnetPools)
	}
	if len(serviceAddresses) != 2 || serviceAddresses["100.100.100.7"] != "svc:payments" ||
		serviceAddresses["100.100.100.8"] != "svc:search" {
		t.Errorf("RawEntries serviceAddresses = %+v, want string and object values verbatim", serviceAddresses)
	}
	if len(owners) != 2 || owners[0] != "ops" || owners[1] != "platform" {
		t.Errorf("RawEntries owners = %q, want [ops platform]", owners)
	}
}

// TestRawEntriesReturnsNilOnReadFailureOrMalformedHuJSON tests that
// RawEntries gracefully returns nil maps on any failure, never panics.
func TestRawEntriesReturnsNilOnReadFailureOrMalformedHuJSON(t *testing.T) {
	t.Parallel()

	// Missing file
	siteIDs, subnetPools, serviceAddresses, owners := hujsonstore.RawEntries("/nonexistent/path/to/file.hujson")
	if siteIDs != nil || subnetPools != nil || serviceAddresses != nil || owners != nil {
		t.Errorf("RawEntries on missing file = (%v, %v, %v, %v), want all nil",
			siteIDs, subnetPools, serviceAddresses, owners)
	}

	// Malformed HuJSON
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{invalid json}`), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}
	siteIDs, subnetPools, serviceAddresses, owners = hujsonstore.RawEntries(path)
	if siteIDs != nil || subnetPools != nil || serviceAddresses != nil || owners != nil {
		t.Errorf("RawEntries on malformed HuJSON = (%v, %v, %v, %v), want all nil",
			siteIDs, subnetPools, serviceAddresses, owners)
	}
}

// TestLoadScopesSiteIDErrorsAndAllowsSubnetPoolOperations tests that a
// malformed subnetPools block does not prevent site-ID commands from working.
func TestLoadScopesSiteIDErrorsAndAllowsSubnetPoolOperations(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	// Overlapping subnetPools (invalid) with valid siteIDs
	content := `{"siteIDs":{"1":"hq-vpc"},"subnetPools":{"10.0.0.0/16":"hq","10.0.1.0/24":"branch"}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	// All site-ID operations should work despite the subnetPools error
	entries, err := ledgerAt(path).ListSiteIDs(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSiteIDs should succeed with valid siteIDs: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1" {
		t.Errorf("ListSiteIDs = %+v, want one entry for site ID 1", entries)
	}

	id, site, _, changed, err := ledgerAt(path).ReserveSiteID(regCtx(), registryTailnet, "2",
		"branch-office",
		registry.ReservationMetadata{},
	)
	if err != nil {
		t.Fatalf("ReserveSiteID should succeed despite subnetPools error: %v", err)
	}
	if id != "2" || site != "branch-office" || !changed {
		t.Errorf("ReserveSiteID = (%q, %q, %v), want (2, branch-office, true)", id, site, changed)
	}

	_, changed, err = ledgerAt(path).ReleaseSiteID(regCtx(), registryTailnet, "1")
	if err != nil {
		t.Fatalf("ReleaseSiteID should succeed despite subnetPools error: %v", err)
	}
	if !changed {
		t.Error("ReleaseSiteID changed = false, want true")
	}
}

// TestLoadScopesSubnetPoolErrorsAndAllowsSiteIDOperations tests that a
// malformed siteIDs block does not prevent subnet-pool commands from working.
func TestLoadScopesSubnetPoolErrorsAndAllowsSiteIDOperations(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	// Duplicate siteIDs (invalid) with valid subnetPools
	content := `{"siteIDs":{"1":"a","1":"b"},"subnetPools":{"10.0.0.0/16":"hq-vpc"}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	// Subnet pool operations should work despite the siteIDs error
	entries, err := ledgerAt(path).ListSubnetPools(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSubnetPools should succeed with valid subnetPools: %v", err)
	}
	if len(entries) != 1 || entries[0].CIDR != "10.0.0.0/16" {
		t.Errorf("ListSubnetPools = %+v, want one entry for 10.0.0.0/16", entries)
	}

	cidr, site, _, changed, err := ledgerAt(path).ReserveSubnetPool(regCtx(), registryTailnet, "192.168.0.0/16",
		"branch-office",
		registry.ReservationMetadata{},
	)
	if err != nil {
		t.Fatalf("ReserveSubnetPool should succeed despite siteIDs error: %v", err)
	}
	if cidr != "192.168.0.0/16" || site != "branch-office" || !changed {
		t.Errorf("ReserveSubnetPool = (%q, %q, %v), want (192.168.0.0/16, branch-office, true)", cidr, site, changed)
	}
}

// TestReleaseSubnetPoolCanRepairOverlapError tests the specific case from
// issue 2: that ReleaseSubnetPoolWithLogger can remove an overlapping entry
// even when the overlap error would previously block all operations.
func TestReleaseSubnetPoolCanRepairOverlapError(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	// Overlapping pools owned by different sites (invalid)
	content := `{"subnetPools":{"10.0.0.0/16":"hq","10.0.1.0/24":"branch"}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	// Release should succeed and repair the overlap
	cidr, changed, err := ledgerAt(path).ReleaseSubnetPool(regCtx(), registryTailnet, "10.0.1.0/24")
	if err != nil {
		t.Fatalf("ReleaseSubnetPool should succeed to repair overlap: %v", err)
	}
	if cidr != "10.0.1.0/24" || !changed {
		t.Errorf("ReleaseSubnetPool = (%q, %v), want (10.0.1.0/24, true)", cidr, changed)
	}

	// Now ListSubnetPools should succeed (overlap is gone)
	entries, err := ledgerAt(path).ListSubnetPools(regCtx(), registryTailnet)
	if err != nil {
		t.Fatalf("ListSubnetPools after repair: %v", err)
	}
	if len(entries) != 1 || entries[0].CIDR != "10.0.0.0/16" {
		t.Errorf("ListSubnetPools after repair = %+v, want only the hq entry", entries)
	}
}
