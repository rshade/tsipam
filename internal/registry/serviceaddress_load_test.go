package registry //nolint:testpackage // scoped Load validation state is intentionally unexported

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rshade/tailscale-utils/policycore/hujsonedit"
)

func TestLoadServiceAddressesAbsentEmptyAndValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    map[string]ServiceEntry
	}{
		{name: "absent", content: `{}` + "\n"},
		{name: "empty", content: `{"serviceAddresses": {}}` + "\n"},
		{
			name: "bare and slash 32 keys with string and object values",
			content: `{
				"serviceAddresses": {
					"100.100.100.7": "payments",
					"100.100.100.8/32": {"service":"svc:search","owner":"platform"}
				}
			}` + "\n",
			want: map[string]ServiceEntry{
				"100.100.100.7": {Service: "svc:payments"},
				"100.100.100.8": {
					Service:             "svc:search",
					ReservationMetadata: ReservationMetadata{Owner: "platform"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := serviceRegistryPath(t)
			writeRegistryFixture(t, path, tc.content)
			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(got.ServiceAddresses) != len(tc.want) {
				t.Fatalf("ServiceAddresses = %+v, want %+v", got.ServiceAddresses, tc.want)
			}
			for address, wantEntry := range tc.want {
				if got.ServiceAddresses[address] != wantEntry {
					t.Errorf("ServiceAddresses[%q] = %+v, want %+v", address, got.ServiceAddresses[address], wantEntry)
				}
			}
		})
	}
}

func TestLoadRejectsInvalidServiceAddressRegistryShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		wantIn  string
	}{
		{
			name:    "canonical alias duplicate",
			content: `{"serviceAddresses":{"100.100.100.7":"svc:a","100.100.100.7/32":"svc:b"}}`,
			wantIn:  "defined twice",
		},
		{
			name:    "exact duplicate entry",
			content: `{"serviceAddresses":{"100.100.100.7":"svc:a","100.100.100.7":"svc:b"}}`,
			wantIn:  "duplicate serviceAddresses entry key",
		},
		{
			name:    "near miss top level key",
			content: `{"serviceaddresses":{"100.100.100.7":"svc:a"}}`,
			wantIn:  `must be spelled "serviceAddresses"`,
		},
		{
			name:    "duplicate top level key",
			content: `{"serviceAddresses":{},"serviceAddresses":{"100.100.100.7":"svc:a"}}`,
			wantIn:  `multiple top-level keys match "serviceAddresses"`,
		},
		{
			name:    "wider prefix key",
			content: `{"serviceAddresses":{"100.100.100.7/24":"svc:a"}}`,
			wantIn:  "IPv4 host or exact /32",
		},
		{
			name:    "IPv6 key",
			content: `{"serviceAddresses":{"2001:db8::7":"svc:a"}}`,
			wantIn:  "IPv4 host or exact /32",
		},
		{
			name:    "empty string service",
			content: `{"serviceAddresses":{"100.100.100.7":""}}`,
			wantIn:  "service name must not be empty",
		},
		{
			name:    "object missing service",
			content: `{"serviceAddresses":{"100.100.100.7":{"owner":"platform"}}}`,
			wantIn:  "service name must not be empty",
		},
		{
			name:    "unsupported value shape",
			content: `{"serviceAddresses":{"100.100.100.7":[]}}`,
			wantIn:  "service registry entry must be a string or an object",
		},
		{
			name:    "invalid reserved-at metadata",
			content: `{"serviceAddresses":{"100.100.100.7":{"service":"svc:a","reservedAt":"not-a-time"}}}`,
			wantIn:  "not a valid RFC 3339 timestamp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := serviceRegistryPath(t)
			writeRegistryFixture(t, path, tc.content+"\n")
			reg, loadErr := Load(path)
			if loadErr != nil {
				t.Fatalf("Load: %v", loadErr)
			}
			err := reg.mapError(serviceAddressesKey)
			if err == nil {
				t.Fatal("ListServiceAddresses error = nil, want malformed map rejection")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("ListServiceAddresses error = %q, want it to contain %q", err, tc.wantIn)
			}
		})
	}
}

func TestReserveServiceAddressRejectsStoredInvalidReservedAtWithoutWriting(t *testing.T) {
	t.Parallel()

	path := serviceRegistryPath(t)
	writeRegistryFixture(t, path, `{
		"serviceAddresses": {
			"100.100.100.7": {
				"service": "svc:payments",
				"owner": "platform",
				"reservedAt": "not-a-time"
			}
		}
	}`+"\n")
	before := snapshotRegistry(t, path)

	_, _, _, changed, err := ReserveServiceAddressWithLogger(
		path,
		"100.100.100.7",
		"svc:payments",
		ReservationMetadata{Description: "payments VIP"},
		nil,
	)
	if err == nil {
		t.Fatal("ReserveServiceAddress error = nil, want malformed stored reservedAt rejection")
	}
	if !strings.Contains(err.Error(), "not a valid RFC 3339 timestamp") {
		t.Errorf("ReserveServiceAddress error = %q, want RFC 3339 validation context", err)
	}
	if changed {
		t.Error("changed = true despite malformed stored reservedAt, want false")
	}
	assertRegistryUntouched(t, path, before)
}

func TestServiceAddressValidationErrorsAreScopedByMap(t *testing.T) {
	t.Parallel()

	t.Run("malformed subnet map does not block service list", func(t *testing.T) {
		t.Parallel()
		path := serviceRegistryPath(t)
		writeRegistryFixture(t, path, `{
			"subnetPools":{"10.0.0.1/24":"hq"},
			"serviceAddresses":{"100.100.100.7":"svc:payments"}
		}`+"\n")
		reg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if mapErr := reg.mapError(serviceAddressesKey); mapErr != nil {
			t.Fatalf("serviceAddresses map error = %v, want nil", mapErr)
		}
		if mapErr := reg.mapError(subnetPoolsKey); mapErr == nil {
			t.Fatal("subnetPools map error = nil, want malformed subnet rejection")
		}
		if len(reg.ServiceAddresses) != 1 || reg.ServiceAddresses["100.100.100.7"].Service != "svc:payments" {
			t.Errorf("ServiceAddresses = %+v, want payments entry", reg.ServiceAddresses)
		}
	})

	t.Run("malformed service map does not block site or subnet lists", func(t *testing.T) {
		t.Parallel()
		path := serviceRegistryPath(t)
		writeRegistryFixture(t, path, `{
			"siteIDs":{"1":"hq"},
			"subnetPools":{"10.0.0.0/24":"hq"},
			"serviceAddresses":{"100.100.100.7/24":"svc:payments"}
		}`+"\n")
		reg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if mapErr := reg.mapError(serviceAddressesKey); mapErr == nil {
			t.Fatal("serviceAddresses map error = nil, want malformed address rejection")
		}
		if mapErr := reg.mapError(siteIDsKey); mapErr != nil {
			t.Errorf("siteIDs map error = %v, want nil", mapErr)
		}
		if mapErr := reg.mapError(subnetPoolsKey); mapErr != nil {
			t.Errorf("subnetPools map error = %v, want nil", mapErr)
		}
		if len(reg.SiteIDs) != 1 || len(reg.SubnetPools) != 1 {
			t.Errorf("site/subnet entries = %+v/%+v, want one valid entry each", reg.SiteIDs, reg.SubnetPools)
		}
	})
}

// TestReleaseServiceAddressAgainstMalformedMapFailsWhenNoView is the
// regression guard for the narrowed Issue 6 repair: releasing a key that
// literally matches an on-file entry ("10.0.0.5") is not enough to earn a
// repair, because a literal-key match proves nothing about which entry
// actually caused the map to fail. The malformed sibling ("999.1.1.1")
// remains, so removing the healthy one would still leave the map unloadable
// -- release must refuse rather than silently destroy the healthy entry, and
// the file must be left byte-for-byte as it was. See
// TestReleaseServiceAddressRepairsRawKeyMatchInMalformedMap for the case
// where the raw-key match is the actual culprit and the repair is kept.
func TestReleaseServiceAddressAgainstMalformedMapFailsWhenNoView(t *testing.T) {
	t.Parallel()

	path := serviceRegistryPath(t)
	writeRegistryFixture(t, path, `{
		"serviceAddresses": {
			"999.1.1.1": "svc:a",
			"10.0.0.5": "svc:b"
		}
	}`+"\n")

	before := snapshotRegistry(t, path)

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if mapErr := reg.mapError(serviceAddressesKey); mapErr == nil {
		t.Fatal("mapError = nil, want malformed address rejection")
	}

	address, changed, releaseErr := ReleaseServiceAddressWithLogger(path, "10.0.0.5", nil)
	if releaseErr == nil {
		t.Fatal("ReleaseServiceAddress error = nil, want failure: removing the healthy sibling does not repair the map")
	}
	if changed {
		t.Error("changed = true despite error, want false")
	}
	if address != "" {
		t.Errorf("address = %q, want empty on error", address)
	}
	if !strings.Contains(releaseErr.Error(), "cannot determine") {
		t.Errorf("error = %q, want 'cannot determine' message", releaseErr)
	}

	assertRegistryUntouched(t, path, before)
}

// TestReleaseServiceAddressRepairsRawKeyMatchInMalformedMap is the regression
// test for Issue 6: releasing the entry that is actually malformed repairs
// the map, and a healthy sibling elsewhere in the same document survives the
// repair untouched. The bad entry here fails validation on its metadata (a
// control character), not its key, since a caller can never pass a
// syntactically invalid address through ReleaseServiceAddressWithLogger --
// NormalizeServiceAddress rejects it before releaseRegistryEntry ever sees
// it -- so the only reachable "release the actual culprit" case is a
// syntactically valid key with an invalid stored value. See
// TestReleaseServiceAddressAgainstMalformedMapFailsWhenNoView for the
// narrowed refusal when the released key is not the culprit.
func TestReleaseServiceAddressRepairsRawKeyMatchInMalformedMap(t *testing.T) {
	t.Parallel()

	path := serviceRegistryPath(t)
	writeRegistryFixture(t, path, `{
		"serviceAddresses": {
			"100.100.100.7": {"service":"svc:payments","owner":"bad\u0007owner"},
			"10.0.0.5": "svc:b"
		}
	}`+"\n")

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if mapErr := reg.mapError(serviceAddressesKey); mapErr == nil {
		t.Fatal("mapError = nil, want malformed address rejection")
	}

	address, changed, releaseErr := ReleaseServiceAddressWithLogger(path, "100.100.100.7", nil)
	if releaseErr != nil {
		t.Fatalf("ReleaseServiceAddress: %v, want the raw-key repair to succeed", releaseErr)
	}
	if !changed || address != "100.100.100.7" {
		t.Errorf("ReleaseServiceAddress = (%q, %t), want (100.100.100.7, true)", address, changed)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading %s: %v", path, readErr)
	}
	if strings.Contains(string(got), `"100.100.100.7"`) {
		t.Errorf("registry after repair-mode release = %s, want the bad raw key removed", got)
	}
	if !strings.Contains(string(got), `"10.0.0.5"`) {
		t.Errorf("registry after repair-mode release = %s, want the untouched healthy sibling preserved", got)
	}

	repaired, loadErr := Load(path)
	if loadErr != nil {
		t.Fatalf("Load after repair: %v, want the registry to be loadable again", loadErr)
	}
	if mapErr := repaired.mapError(serviceAddressesKey); mapErr != nil {
		t.Errorf("mapError after repair = %v, want nil", mapErr)
	}
	if len(repaired.ServiceAddresses) != 1 || repaired.ServiceAddresses["10.0.0.5"].Service != "svc:b" {
		t.Errorf("ServiceAddresses after repair = %+v, want only the healthy sibling", repaired.ServiceAddresses)
	}
}

// TestReleaseRepairsMapBrickedByOneBadEntry bricks a map with a single bad
// entry (metadata carrying a control character -- see Issue 5), releases
// that exact entry by its raw key, and confirms the registry loads cleanly
// afterward. This is the escape hatch Issue 6 asks for: previously, an
// operator whose registry had exactly this shape had no CLI path back and
// could only hand-edit the HuJSON.
func TestReleaseRepairsMapBrickedByOneBadEntry(t *testing.T) {
	t.Parallel()

	path := serviceRegistryPath(t)
	writeRegistryFixture(t, path,
		`{"serviceAddresses":{"100.100.100.7":{"service":"svc:payments","owner":"bad\u0007owner"}}}`+"\n")

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if mapErr := reg.mapError(serviceAddressesKey); mapErr == nil {
		t.Fatal("mapError = nil, want the control-character metadata to brick this map")
	}

	address, changed, releaseErr := ReleaseServiceAddressWithLogger(path, "100.100.100.7", nil)
	if releaseErr != nil {
		t.Fatalf("ReleaseServiceAddress: %v, want the repair to succeed", releaseErr)
	}
	if !changed || address != "100.100.100.7" {
		t.Errorf("ReleaseServiceAddress = (%q, %t), want (100.100.100.7, true)", address, changed)
	}

	repaired, loadErr := Load(path)
	if loadErr != nil {
		t.Fatalf("Load after repair: %v, want the registry to be loadable again", loadErr)
	}
	if mapErr := repaired.mapError(serviceAddressesKey); mapErr != nil {
		t.Errorf("mapError after repair = %v, want nil", mapErr)
	}
	if len(repaired.ServiceAddresses) != 0 {
		t.Errorf("ServiceAddresses after repair = %+v, want empty", repaired.ServiceAddresses)
	}
}

// repairDecisionTableCase is one row of the repairMalformedMap decision
// table: given content on file and a release call, does the release land the
// expected result, and does the file end up in the expected state.
type repairDecisionTableCase struct {
	name              string
	content           string
	release           func(path string) (bool, error)
	wantChanged       bool
	wantErr           bool
	wantErrContains   string
	wantByteIdentical bool
	verify            func(t *testing.T, path string)
}

func releaseServiceAddressChanged(address string) func(path string) (bool, error) {
	return func(path string) (bool, error) {
		_, changed, err := ReleaseServiceAddressWithLogger(path, address, nil)
		return changed, err
	}
}

func releaseSiteIDChanged(id string) func(path string) (bool, error) {
	return func(path string) (bool, error) {
		_, changed, err := ReleaseSiteIDWithLogger(path, id, nil)
		return changed, err
	}
}

func releaseSubnetPoolChanged(cidr string) func(path string) (bool, error) {
	return func(path string) (bool, error) {
		_, changed, err := ReleaseSubnetPoolWithLogger(path, cidr, nil)
		return changed, err
	}
}

func verifyReleasedCanonicalKey(topLevel, key string) func(t *testing.T, path string) {
	return func(t *testing.T, path string) {
		t.Helper()
		reg, err := Load(path)
		if err != nil {
			t.Fatalf("Load after repair: %v", err)
		}
		acc, err := reg.mapAccessorFor(topLevel)
		if err != nil {
			t.Fatalf("mapAccessorFor(%q): %v", topLevel, err)
		}
		if acc.err != nil {
			t.Fatalf("mapError(%q) after repair = %v, want nil", topLevel, acc.err)
		}
		if acc.hasKey(key) {
			t.Errorf("%s still contains released canonical key %q", topLevel, key)
		}
	}
}

// TestRepairMalformedMapHonorsKeyMembershipAfterReload is the decision-table
// test for repairMalformedMap: every row must hold at once, not just the
// examples that motivated the fix. Rows 6-8 are the regression guard for the
// defect where a second on-file spelling canonicalizing to the released key
// (an exact duplicate, a bare/CIDR alias, or a non-canonical numeric spelling
// like "01") let repairMalformedMap accept a reload that merely parsed
// without checking that the released key was actually gone -- silently
// leaving the twin spelling in force while reporting success. See
// repairMalformedMap's doc comment for the fixed predicate.
func TestRepairMalformedMapHonorsKeyMembershipAfterReload(t *testing.T) {
	t.Parallel()

	const brickedSibling = `{
		"serviceAddresses": {
			"100.100.100.7": {"service":"svc:a","owner":"bad\u0007owner"},
			"10.0.0.5": "svc:b"
		}
	}` + "\n"

	cases := []repairDecisionTableCase{
		{
			name:        "row 1: clean map, key present is released",
			content:     `{"serviceAddresses":{"10.0.0.5":"svc:a","10.0.0.6":"svc:b"}}` + "\n",
			release:     releaseServiceAddressChanged("10.0.0.5"),
			wantChanged: true,
			verify: func(t *testing.T, path string) {
				t.Helper()
				reg, err := Load(path)
				if err != nil {
					t.Fatalf("Load after release: %v", err)
				}
				if mapErr := reg.mapError(serviceAddressesKey); mapErr != nil {
					t.Fatalf("mapError after release = %v, want nil", mapErr)
				}
				if _, stillThere := reg.ServiceAddresses["10.0.0.5"]; stillThere {
					t.Error("ServiceAddresses still contains the released key 10.0.0.5")
				}
				if len(reg.ServiceAddresses) != 1 || reg.ServiceAddresses["10.0.0.6"].Service != "svc:b" {
					t.Errorf("ServiceAddresses after release = %+v, want only {10.0.0.6: svc:b}", reg.ServiceAddresses)
				}
			},
		},
		{
			name:              "row 2: clean map, key absent is an idempotent no-op",
			content:           `{"serviceAddresses":{"10.0.0.5":"svc:a"}}` + "\n",
			release:           releaseServiceAddressChanged("10.0.0.9"),
			wantChanged:       false,
			wantByteIdentical: true,
		},
		{
			name:        "row 3: one bad entry, key is the culprit is repaired",
			content:     brickedSibling,
			release:     releaseServiceAddressChanged("100.100.100.7"),
			wantChanged: true,
			verify: func(t *testing.T, path string) {
				t.Helper()
				reg, err := Load(path)
				if err != nil {
					t.Fatalf("Load after repair: %v", err)
				}
				if mapErr := reg.mapError(serviceAddressesKey); mapErr != nil {
					t.Errorf("mapError after repair = %v, want nil", mapErr)
				}
				if len(reg.ServiceAddresses) != 1 || reg.ServiceAddresses["10.0.0.5"].Service != "svc:b" {
					t.Errorf(
						"ServiceAddresses after repair = %+v, want only the healthy sibling {10.0.0.5: svc:b}",
						reg.ServiceAddresses,
					)
				}
			},
		},
		{
			name:              "row 4: one bad entry, key is a healthy sibling refuses",
			content:           brickedSibling,
			release:           releaseServiceAddressChanged("10.0.0.5"),
			wantErr:           true,
			wantErrContains:   "cannot determine",
			wantByteIdentical: true,
		},
		{
			name:              "row 5: one bad entry, key not on file at all refuses",
			content:           brickedSibling,
			release:           releaseServiceAddressChanged("10.0.0.9"),
			wantErr:           true,
			wantErrContains:   "cannot determine",
			wantByteIdentical: true,
		},
		{
			name:              "row 6: exact duplicate key, survivor still canonicalizes to it refuses",
			content:           `{"serviceAddresses":{"10.0.0.5":"svc:a","10.0.0.5":"svc:b"}}` + "\n",
			release:           releaseServiceAddressChanged("10.0.0.5"),
			wantErr:           true,
			wantErrContains:   "cannot determine",
			wantByteIdentical: true,
		},
		{
			name:              "row 7: bare/slash-32 alias pair, canonical form still reserved refuses",
			content:           `{"serviceAddresses":{"10.0.0.5":"svc:a","10.0.0.5/32":"svc:b"}}` + "\n",
			release:           releaseServiceAddressChanged("10.0.0.5"),
			wantErr:           true,
			wantErrContains:   "cannot determine",
			wantByteIdentical: true,
		},
		{
			name:              `row 8: site ID "01"/"1" alias pair, canonical form still reserved refuses`,
			content:           `{"siteIDs":{"01":"hq-vpc","1":"branch-office"}}` + "\n",
			release:           releaseSiteIDChanged("1"),
			wantErr:           true,
			wantErrContains:   "cannot determine",
			wantByteIdentical: true,
		},
		{
			name: "row 9: two bad entries, removing one culprit does not validate refuses",
			content: `{"serviceAddresses":{
				"100.100.100.7": {"service":"svc:a","owner":"bad\u0007owner"},
				"100.100.100.8": {"service":"svc:b","owner":"bad\u0007owner"}
			}}` + "\n",
			release:           releaseServiceAddressChanged("100.100.100.7"),
			wantErr:           true,
			wantErrContains:   "cannot determine",
			wantByteIdentical: true,
		},
		{
			name:              "row 10: top-level member entirely absent is an idempotent no-op",
			content:           `{}` + "\n",
			release:           releaseServiceAddressChanged("10.0.0.5"),
			wantChanged:       false,
			wantByteIdentical: true,
		},
		{
			name:        `row 11: malformed site ID entry under raw alias "01" is repaired`,
			content:     `{"siteIDs":{"01":{"site":"hq","owner":"bad\u0007owner"}}}` + "\n",
			release:     releaseSiteIDChanged("1"),
			wantChanged: true,
			verify:      verifyReleasedCanonicalKey(siteIDsKey, "1"),
		},
		{
			name:        "row 12: malformed subnet entry under whitespace alias is repaired",
			content:     `{"subnetPools":{" 10.0.0.0/24":{"site":"hq","owner":"bad\u0007owner"}}}` + "\n",
			release:     releaseSubnetPoolChanged("10.0.0.0/24"),
			wantChanged: true,
			verify:      verifyReleasedCanonicalKey(subnetPoolsKey, "10.0.0.0/24"),
		},
		{
			name:        "row 13: malformed service-address entry under slash-32 alias is repaired",
			content:     `{"serviceAddresses":{"100.100.100.7/32":{"service":"svc:a","owner":"bad\u0007owner"}}}` + "\n",
			release:     releaseServiceAddressChanged("100.100.100.7"),
			wantChanged: true,
			verify:      verifyReleasedCanonicalKey(serviceAddressesKey, "100.100.100.7"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runRepairDecisionTableCase(t, tc)
		})
	}
}

// runRepairDecisionTableCase seeds tc.content, runs tc.release, and asserts
// its outcome. Split out of TestRepairMalformedMapHonorsKeyMembershipAfterReload
// so each concern -- error shape, changed flag, byte-identity, and any
// row-specific verify -- stays a flat, separately readable step.
func runRepairDecisionTableCase(t *testing.T, tc repairDecisionTableCase) {
	t.Helper()
	path := serviceRegistryPath(t)
	writeRegistryFixture(t, path, tc.content)

	before := snapshotRegistry(t, path)

	changed, err := tc.release(path)

	switch {
	case tc.wantErr && err == nil:
		t.Fatal("release error = nil, want an error")
	case tc.wantErr:
		if tc.wantErrContains != "" && !strings.Contains(err.Error(), tc.wantErrContains) {
			t.Errorf("release error = %q, want it to contain %q", err, tc.wantErrContains)
		}
	case err != nil:
		t.Fatalf("release error = %v, want nil", err)
	}
	if changed != tc.wantChanged {
		t.Errorf("changed = %t, want %t", changed, tc.wantChanged)
	}

	if tc.wantByteIdentical {
		assertRegistryUntouched(t, path, before)
	}
	if tc.verify != nil {
		tc.verify(t, path)
	}
}

// registrySnapshot is a registry file's contents and modification time,
// captured together so a refused repair can be shown to have written nothing
// at all -- not merely to have ended up with the same bytes.
type registrySnapshot struct {
	content []byte
	modTime time.Time
}

func snapshotRegistry(t *testing.T, path string) registrySnapshot {
	t.Helper()
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading %s: %v", path, readErr)
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat %s: %v", path, statErr)
	}
	return registrySnapshot{content: content, modTime: info.ModTime()}
}

// assertRegistryUntouched fails the test unless path is exactly as before:
// the same bytes, and never rewritten. The byte check is the user-facing
// contract -- a refused repair must never re-serialize the
// comment-preserving HuJSON document. The modification time is what
// distinguishes deciding before writing from writing and then restoring: a
// repair that patches the file and puts the original bytes back also
// satisfies the byte check, while leaving a window in which the registry
// held a rejected repair and a restore that could itself have failed.
func assertRegistryUntouched(t *testing.T, path string, before registrySnapshot) {
	t.Helper()
	after := snapshotRegistry(t, path)
	if string(after.content) != string(before.content) {
		t.Errorf(
			"registry after a refused repair = %s, want it byte-identical to the original %s",
			after.content, before.content,
		)
	}
	if !after.modTime.Equal(before.modTime) {
		t.Errorf(
			"registry modification time moved from %s to %s: a refused repair must not write the file at all",
			before.modTime, after.modTime,
		)
	}
}

func TestServiceAddressLoadValidationErrorsHaveProperContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "bad address key",
			content: `{"serviceAddresses":{"999.1.1.1":"svc:a"}}`,
			wantErr: "invalid service address key",
		},
		{
			name:    "non-/32 address key",
			content: `{"serviceAddresses":{"10.0.0.5/24":"svc:a"}}`,
			wantErr: "invalid service address key",
		},
		{
			name:    "empty service value",
			content: `{"serviceAddresses":{"10.0.0.5":""}}`,
			wantErr: "service address 10.0.0.5 (key \"10.0.0.5\")",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := serviceRegistryPath(t)
			writeRegistryFixture(t, path, tc.content+"\n")
			reg, loadErr := Load(path)
			if loadErr != nil {
				t.Fatalf("Load: %v", loadErr)
			}
			err := reg.mapError(serviceAddressesKey)
			if err == nil {
				t.Fatal("mapError = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want to contain %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "decoding registry") {
				t.Errorf("error = %q, want 'decoding registry' context frame", err)
			}
			var invalidErr *InvalidInputError
			if errors.As(err, &invalidErr) {
				t.Error("error chain contains *InvalidInputError, but stored-state should strip the marker")
			}
		})
	}
}

func serviceRegistryPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tail-ipam.hujson")
}

func writeRegistryFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}
}

// TestReservePatchOpReadsEveryInputOffTheRegistry is the structural guard on
// reservePatchOp taking no hand-passed per-map state. Naming a top-level key
// must select that map's presence flag, raw-key spellings, and membership
// test together: pairing one map's key with another map's state was how the
// subnet-pool corruption defect happened, and a fourth map added later must
// not be able to reintroduce it by forgetting an argument.
//
// Each operation is judged by the document it produces rather than by its
// segments, since the corruption this guards against is only visible in the
// patched bytes: an add aimed at the canonical spelling of an entry stored
// under a different raw spelling lands as a second, duplicate member.
func TestReservePatchOpReadsEveryInputOffTheRegistry(t *testing.T) {
	t.Parallel()

	// serviceAddresses is present with a non-canonical raw spelling; siteIDs
	// is absent entirely, so the two maps need opposite patch strategies from
	// one and the same registry.
	const document = `{"serviceAddresses":{"10.0.0.5/32":"svc:a"}}` + "\n"
	reg, err := loadBytes([]byte(document))
	if err != nil {
		t.Fatalf("loadBytes: %v", err)
	}

	presentOp, err := reservePatchOp(reg, serviceAddressesKey, "10.0.0.5", ServiceEntry{Service: "svc:b"})
	if err != nil {
		t.Fatalf("reservePatchOp(serviceAddresses): %v", err)
	}
	amended := applyReservePatchOp(t, document, presentOp)
	if got := amended.ServiceAddresses["10.0.0.5"].Service; got != "svc:b" {
		t.Errorf("service address 10.0.0.5 = %q after the amend, want %q", got, "svc:b")
	}
	if len(amended.ServiceAddresses) != 1 {
		t.Errorf(
			"serviceAddresses = %+v after the amend, want the single on-file entry replaced, not duplicated",
			amended.ServiceAddresses,
		)
	}

	absentOp, err := reservePatchOp(reg, siteIDsKey, "1", Entry{Site: "hq"})
	if err != nil {
		t.Fatalf("reservePatchOp(siteIDs): %v", err)
	}
	created := applyReservePatchOp(t, document, absentOp)
	if got := created.SiteIDs["1"].Site; got != "hq" {
		t.Errorf("site ID 1 = %q after the create, want %q", got, "hq")
	}
	if len(created.ServiceAddresses) != 1 {
		t.Errorf("serviceAddresses = %+v after the siteIDs create, want it untouched", created.ServiceAddresses)
	}

	if _, err = reservePatchOp(reg, "nextFreePools", "1", Entry{Site: "hq"}); err == nil {
		t.Error("reservePatchOp for an unknown map = nil error, want it to refuse rather than patch blind")
	}
}

// applyReservePatchOp applies op to document in memory and returns the
// registry the result decodes to, failing the test if either step does not
// succeed.
func applyReservePatchOp(t *testing.T, document string, op hujsonedit.PatchOperation) *Registry {
	t.Helper()
	patched, err := hujsonedit.PatchBytes([]byte(document), nil, op)
	if err != nil {
		t.Fatalf("PatchBytes: %v", err)
	}
	reg, err := loadBytes(patched)
	if err != nil {
		t.Fatalf("loadBytes after patch: %v\npatched document: %s", err, patched)
	}
	for _, mapErr := range []error{reg.siteIDsErr, reg.serviceAddressesErr} {
		if mapErr != nil {
			t.Fatalf("patched document does not validate: %v\npatched document: %s", mapErr, patched)
		}
	}
	return reg
}
