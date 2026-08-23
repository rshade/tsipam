package registry_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

func regPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tail-ipam.hujson")
}

func TestReserveSiteIDCreatesFileWhenMissing(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	_, _, changed, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil)
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for a fresh reservation")
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSiteIDs = %+v, want [{1 hq-vpc}]", entries)
	}
}

func TestReserveSiteIDIsIdempotent(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil); err != nil {
		t.Fatalf("first ReserveSiteID: %v", err)
	}
	_, _, changed, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil)
	if err != nil {
		t.Fatalf("second ReserveSiteID: %v", err)
	}
	if changed {
		t.Error("changed = true on a repeat reservation, want false (no-op)")
	}
}

func TestReserveSiteIDReturnsCanonicalIDAndSite(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	id, site, _, err := registry.ReserveSiteIDWithLogger(path, "007", "  hq-vpc  ", nil)
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if id != "7" || site != "hq-vpc" {
		t.Errorf("ReserveSiteID(%q, %q) = (%q, %q), want (%q, %q)", "007", "  hq-vpc  ", id, site, "7", "hq-vpc")
	}
}

func TestReserveSiteIDRefusesCollision(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil); err != nil {
		t.Fatalf("first ReserveSiteID: %v", err)
	}
	_, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "branch-office", nil)
	var collision *registry.SiteIDCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("ReserveSiteID error = %v, want *SiteIDCollisionError", err)
	}
	if collision.Existing != "hq-vpc" || collision.Wanted != "branch-office" {
		t.Errorf("collision = %+v, want Existing=hq-vpc Wanted=branch-office", collision)
	}
}

func TestReserveSiteIDAddsSecondEntryToExistingRegistry(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil); err != nil {
		t.Fatalf("first ReserveSiteID: %v", err)
	}
	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "2", "branch-office", nil); err != nil {
		t.Fatalf("second ReserveSiteID: %v", err)
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 2 || entries[0].ID != "1" || entries[1].ID != "2" {
		t.Errorf("ListSiteIDs = %+v, want [{1 hq-vpc} {2 branch-office}] sorted numerically", entries)
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
			if _, _, _, err := registry.ReserveSiteIDWithLogger(path, tc.id, tc.site, nil); err == nil {
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

func TestReleaseSiteIDRemovesEntry(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil); err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	id, changed, err := registry.ReleaseSiteIDWithLogger(path, "1", nil)
	if err != nil {
		t.Fatalf("ReleaseSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for an existing entry")
	}
	if id != "1" {
		t.Errorf("ReleaseSiteID id = %q, want %q", id, "1")
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ListSiteIDs = %+v, want empty after release", entries)
	}
}

func TestReleaseSiteIDOnAbsentIDIsNoopNotError(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil); err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	_, changed, err := registry.ReleaseSiteIDWithLogger(path, "2", nil)
	if err != nil {
		t.Fatalf("ReleaseSiteID: %v", err)
	}
	if changed {
		t.Error("changed = true releasing an id that was never reserved, want false")
	}
}

func TestReleaseSiteIDOnMissingFileIsError(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, _, err := registry.ReleaseSiteIDWithLogger(path, "1", nil); err == nil {
		t.Error("ReleaseSiteID on a missing registry file error = nil, want an error")
	}
}

func TestListSiteIDsOnMissingFileIsError(t *testing.T) {
	t.Parallel()
	path := regPath(t)

	if _, err := registry.ListSiteIDsWithLogger(path, nil); err == nil {
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

	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "2", "branch-office", nil); err != nil {
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

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSiteIDs on a registry with an unrelated top-level key: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSiteIDs = %+v, want [{1 hq-vpc}]", entries)
	}

	if _, _, _, reserveErr := registry.ReserveSiteIDWithLogger(path, "2", "branch-office", nil); reserveErr != nil {
		t.Fatalf("ReserveSiteID: %v", reserveErr)
	}
	if _, _, releaseErr := registry.ReleaseSiteIDWithLogger(path, "1", nil); releaseErr != nil {
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

	_, err := registry.ListSiteIDsWithLogger(path, nil)
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

	_, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "branch-office", nil)
	var collision *registry.SiteIDCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("ReserveSiteID(%q, ...) error = %v, want *SiteIDCollisionError against the un-normalized file key %q",
			"1", err, "01")
	}
	if collision.Existing != "hq-vpc" {
		t.Errorf("collision.Existing = %q, want %q", collision.Existing, "hq-vpc")
	}
}

func TestReleaseSiteIDRemovesUnNormalizedFileKey(t *testing.T) {
	t.Parallel()
	path := regPath(t)
	if err := os.WriteFile(path, []byte(`{"siteIDs":{"01":"hq-vpc","2":"branch-office"}}`), 0o644); err != nil {
		t.Fatalf("seeding fixture: %v", err)
	}

	id, changed, err := registry.ReleaseSiteIDWithLogger(path, "1", nil)
	if err != nil {
		t.Fatalf("ReleaseSiteID: %v", err)
	}
	if !changed || id != "1" {
		t.Errorf("ReleaseSiteID(%q) = (id=%q, changed=%v), want (1, true)", "1", id, changed)
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
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
			_, _, _, err := registry.ReserveSiteIDWithLogger(path, id, site, nil)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: ReserveSiteID: %v", i, err)
		}
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
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

			_, err := registry.ListSiteIDsWithLogger(path, nil)
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

	_, _, changed, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil)
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for a fresh reservation")
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
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

	if _, _, _, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil); err != nil {
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

	_, _, changed, err := registry.ReserveSiteIDWithLogger(path, "1", "hq-vpc", nil)
	if err != nil {
		t.Fatalf("ReserveSiteID: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true for a fresh reservation")
	}

	entries, err := registry.ListSiteIDsWithLogger(path, nil)
	if err != nil {
		t.Fatalf("ListSiteIDs: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "1" || entries[0].Site != "hq-vpc" {
		t.Errorf("ListSiteIDs = %+v, want [{1 hq-vpc}]", entries)
	}
}
