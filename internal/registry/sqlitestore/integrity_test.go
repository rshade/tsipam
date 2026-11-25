package sqlitestore_test

import (
	"bytes"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
)

func address(t *testing.T, text string) []byte {
	t.Helper()
	bytes16 := netip.MustParseAddr(text).As16()
	return bytes16[:]
}

// seeded returns a database path holding one reservation in every map.
func seeded(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	ledger := registry.NewLedger(open(t, path), nil)
	ctx := registry.WithActor(t.Context(), "user:carol@example.com")
	metadata := registry.ReservationMetadata{Owner: "alice@example.com", Ticket: "OPS-1"}
	if _, _, _, _, err := ledger.ReserveSiteID(ctx, tailnet, "7", "hq-vpc", metadata); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ledger.ReserveSubnetPool(ctx, tailnet, "10.20.0.0/16", "hq-vpc", metadata); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ledger.ReserveServiceAddress(
		ctx, tailnet, "100.100.100.8", "dashboard", metadata,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ledger.ReserveUDPPort(ctx, tailnet, "hq-vpc", "41642", "hq-fw01"); err != nil {
		t.Fatal(err)
	}
	return path
}

const insertPool = `INSERT INTO subnet_pools (tailnet_id, cidr, family, range_start, range_end, site)
	VALUES (1, ?, ?, ?, ?, ?)`

func TestOutOfBandWritesAreRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		query string
		args  []any
		want  string
	}{
		{
			name:  "overlapping pool for another site",
			query: insertPool,
			args: []any{
				"10.20.5.0/24", 4, address(t, "10.20.5.0"), address(t, "10.20.5.255"), "branch-office",
			},
			want: "overlaps a pool held by a different site",
		},
		{
			name:  "replacing a pool for another site",
			query: strings.Replace(insertPool, "INSERT", "INSERT OR REPLACE", 1),
			args: []any{
				"10.20.0.0/16", 4, address(t, "10.20.0.0"), address(t, "10.20.255.255"), "branch-office",
			},
			want: "overlaps a pool held by a different site",
		},
		{
			name:  "text range that would compare below every blob",
			query: insertPool,
			args:  []any{"10.20.5.0/24", 4, strings.Repeat("a", 16), strings.Repeat("b", 16), "branch-office"},
			want:  "cannot store TEXT value in BLOB column",
		},
		{
			name:  "site ID above the range",
			query: "INSERT INTO site_ids (tailnet_id, site_id, site) VALUES (1, 65536, 'hq-vpc')",
			want:  "CHECK constraint failed",
		},
		{
			name:  "negative site ID",
			query: "INSERT INTO site_ids (tailnet_id, site_id, site) VALUES (1, -1, 'hq-vpc')",
			want:  "CHECK constraint failed",
		},
		{
			name:  "fractional site ID",
			query: "INSERT INTO site_ids (tailnet_id, site_id, site) VALUES (1, 1.5, 'hq-vpc')",
			want:  "cannot store REAL value in INTEGER column",
		},
		{
			name:  "duplicate site ID",
			query: "INSERT INTO site_ids (tailnet_id, site_id, site) VALUES (1, 7, 'branch-office')",
			want:  "UNIQUE constraint failed",
		},
		{
			name:  "port below the range",
			query: "INSERT INTO udp_ports (tailnet_id, site, port, device) VALUES (1, 'hq-vpc', 1023, 'fw')",
			want:  "CHECK constraint failed",
		},
		{
			name:  "port above the range",
			query: "INSERT INTO udp_ports (tailnet_id, site, port, device) VALUES (1, 'hq-vpc', 65536, 'fw')",
			want:  "CHECK constraint failed",
		},
		{
			name:  "service without its selector prefix",
			query: "INSERT INTO service_addresses (tailnet_id, address, service) VALUES (1, '100.100.100.9', 'x')",
			want:  "CHECK constraint failed",
		},
		{
			name: "event update", query: "UPDATE events SET actor = 'user:mallory@example.com'",
			want: "append-only",
		},
		{name: "event delete", query: "DELETE FROM events", want: "append-only"},
		{
			name: "event replace",
			query: `INSERT OR REPLACE INTO events (id, tailnet_id, at, action, kind, key)
				VALUES (1, 1, '2026-01-01T00:00:00Z', 'reserve', 'site_id', '7')`,
			want: "append-only",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := rawDB(t, seeded(t)).ExecContext(t.Context(), tc.query, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

func TestOutOfBandMoveIntoAnOverlapIsRejected(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	db := rawDB(t, path)
	mustExec(t, db, insertPool,
		"10.30.0.0/16", 4, address(t, "10.30.0.0"), address(t, "10.30.255.255"), "branch-office")
	mustExec(t, db, "INSERT INTO tailnets (id, key, created_at) VALUES (2, 'staging', '2026-01-01T00:00:00Z')")
	mustExec(t, db, `INSERT INTO subnet_pools (tailnet_id, cidr, family, range_start, range_end, site)
		VALUES (2, '10.20.5.0/24', 4, ?, ?, 'branch-office')`,
		address(t, "10.20.5.0"), address(t, "10.20.5.255"))

	for _, query := range []string{
		"UPDATE subnet_pools SET range_start = (SELECT range_start FROM subnet_pools WHERE site = 'hq-vpc') " +
			"WHERE cidr = '10.30.0.0/16'",
		"UPDATE subnet_pools SET tailnet_id = 1 WHERE tailnet_id = 2",
	} {
		_, err := db.ExecContext(t.Context(), query)
		if err == nil || !strings.Contains(err.Error(), "overlaps a pool held by a different site") {
			t.Errorf("%s: error = %v, want the overlap refusal", query, err)
		}
	}
}

// An invalid row disables its own map and no other, and a release that would
// touch it fails closed.
func TestInvalidRowsFailClosedPerMap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		query string
		args  []any
		want  string
	}{
		{
			name:  "range that does not match the CIDR",
			query: insertPool,
			args:  []any{"10.99.0.0/16", 4, address(t, "10.98.0.0"), address(t, "10.98.255.255"), "branch-office"},
			want:  "subnet CIDR 10.99.0.0/16: stored address range does not match the CIDR",
		},
		{
			name:  "family that does not match the CIDR",
			query: insertPool,
			args:  []any{"10.99.0.0/16", 6, address(t, "10.99.0.0"), address(t, "10.99.255.255"), "branch-office"},
			want:  "subnet CIDR 10.99.0.0/16: stored address range does not match the CIDR",
		},
		{
			name:  "CIDR with host bits",
			query: insertPool,
			args:  []any{"10.99.0.1/16", 4, address(t, "10.99.0.0"), address(t, "10.99.255.255"), "branch-office"},
			want:  `invalid subnet pool key "10.99.0.1/16"`,
		},
		{
			name:  "padded site name",
			query: "UPDATE subnet_pools SET site = ' hq-vpc'",
			want:  "is not canonical",
		},
		{
			name:  "control character in an owner",
			query: "UPDATE subnet_pools SET owner = 'a' || char(27) || 'b'",
			want:  "must not contain control characters",
		},
		{
			name:  "unreadable reserved_at",
			query: "UPDATE subnet_pools SET reserved_at = 'yesterday'",
			want:  "is not a valid RFC 3339 timestamp",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := seeded(t)
			mustExec(t, rawDB(t, path), tc.query, tc.args...)
			ledger := registry.NewLedger(open(t, path), nil)

			_, err := ledger.ListSubnetPools(t.Context(), tailnet)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("list error = %v, want it to say %q", err, tc.want)
			}
			_, _, err = ledger.ReleaseSubnetPool(t.Context(), tailnet, "10.20.0.0/16")
			if err == nil || !strings.Contains(err.Error(), "cannot determine whether subnet CIDR") {
				t.Errorf("release error = %v, want it to fail closed", err)
			}
			if _, _, _, _, err = ledger.ReserveSubnetPool(
				t.Context(), tailnet, "172.16.0.0/24", "hq-vpc", registry.ReservationMetadata{},
			); err == nil {
				t.Error("a reserve into an invalid map succeeded")
			}
			if _, err = ledger.ListSiteIDs(t.Context(), tailnet); err != nil {
				t.Errorf("another map became unusable: %v", err)
			}
			if _, _, err = ledger.ReleaseSiteID(t.Context(), tailnet, "7"); err != nil {
				t.Errorf("a release in another map failed: %v", err)
			}
		})
	}
}

func TestInvalidRowsInTheOtherMaps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		query string
		check func(*testing.T, *registry.Ledger) (error, error)
	}{
		{
			name: "site ID", query: "UPDATE site_ids SET site = 'hq' || char(7)",
			check: func(t *testing.T, ledger *registry.Ledger) (error, error) {
				t.Helper()
				_, listErr := ledger.ListSiteIDs(t.Context(), tailnet)
				_, _, releaseErr := ledger.ReleaseSiteID(t.Context(), tailnet, "7")
				return listErr, releaseErr
			},
		},
		{
			name: "service address", query: "UPDATE service_addresses SET address = '100.100.100.8/32'",
			check: func(t *testing.T, ledger *registry.Ledger) (error, error) {
				t.Helper()
				_, listErr := ledger.ListServiceAddresses(t.Context(), tailnet)
				_, _, releaseErr := ledger.ReleaseServiceAddress(t.Context(), tailnet, "100.100.100.8")
				return listErr, releaseErr
			},
		},
		{
			name: "UDP port", query: "UPDATE udp_ports SET device = 'fw' || char(7)",
			check: func(t *testing.T, ledger *registry.Ledger) (error, error) {
				t.Helper()
				_, listErr := ledger.ListUDPPorts(t.Context(), tailnet, "")
				_, _, _, releaseErr := ledger.ReleaseUDPPort(t.Context(), tailnet, "hq-vpc", "41642")
				return listErr, releaseErr
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := seeded(t)
			mustExec(t, rawDB(t, path), tc.query)
			listErr, releaseErr := tc.check(t, registry.NewLedger(open(t, path), nil))
			if listErr == nil {
				t.Error("the list ignored an invalid row")
			}
			if releaseErr == nil || !strings.Contains(releaseErr.Error(), "cannot determine whether") {
				t.Errorf("release error = %v, want it to fail closed", releaseErr)
			}
		})
	}
}

func TestInvalidEventFailsHistory(t *testing.T) {
	t.Parallel()
	inserts := []string{
		`INSERT INTO events (tailnet_id, at, action, kind, key)
			VALUES (1, 'yesterday', 'reserve', 'site_id', '9')`,
		`INSERT INTO events (tailnet_id, at, action, kind, key, after_value)
			VALUES (1, '2026-01-01T00:00:00Z', 'reserve', 'site_id', '9', '{')`,
		`INSERT INTO events (tailnet_id, at, action, kind, key, actor)
			VALUES (1, '2026-01-01T00:00:00Z', 'reserve', 'site_id', '9', 'user:' || char(27))`,
	}
	for _, insert := range inserts {
		path := seeded(t)
		mustExec(t, rawDB(t, path), insert)
		_, err := registry.NewLedger(open(t, path), nil).History(t.Context(), tailnet, registry.HistoryFilter{Limit: 1})
		if err == nil || !strings.Contains(err.Error(), "registry event 5 is invalid") {
			t.Errorf("error = %v, want the invalid event named", err)
		}
	}
}

func TestIdentityValuesTolerateInvalidRows(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	ledger := registry.NewLedger(open(t, path), nil)
	if _, _, err := ledger.ReleaseServiceAddress(t.Context(), tailnet, "100.100.100.8"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, rawDB(t, path), insertPool,
		"10.99.0.1/16", 4, address(t, "10.99.0.0"), address(t, "10.99.255.255"), "branch-office")

	got := ledger.IdentityValues(t.Context(), tailnet)
	for _, check := range []struct {
		kind   string
		values []string
		want   string
	}{
		{"sites", got.Sites, "hq-vpc"},
		{"sites", got.Sites, "branch-office"},
		{"CIDRs", got.CIDRs, "10.20.0.0/16"},
		{"CIDRs", got.CIDRs, "10.99.0.1/16"},
		{"addresses kept by history", got.Addresses, "100.100.100.8"},
		{"services kept by history", got.Services, "svc:dashboard"},
		{"devices", got.Devices, "hq-fw01"},
		{"owners", got.Owners, "alice@example.com"},
		{"actors", got.Actors, "user:carol@example.com"},
	} {
		if !slices.Contains(check.values, check.want) {
			t.Errorf("%s = %v, want %s included", check.kind, check.values, check.want)
		}
	}
	if other := ledger.IdentityValues(t.Context(), "staging"); len(other.Sites) != 0 {
		t.Errorf("another tailnet's identities = %+v, want none", other)
	}
}

func TestOlderSchemaIsUpgradedInPlace(t *testing.T) {
	t.Parallel()
	const script = "ALTER TABLE site_ids ADD COLUMN later TEXT"
	for _, operation := range []struct {
		name string
		run  func(*registry.Ledger) error
	}{
		{"read", func(ledger *registry.Ledger) error {
			_, err := ledger.ListSiteIDs(t.Context(), tailnet)
			return err
		}},
		{"write", func(ledger *registry.Ledger) error {
			_, _, _, _, err := ledger.ReserveSiteID(
				t.Context(), tailnet, "8", "hq-vpc", registry.ReservationMetadata{},
			)
			return err
		}},
	} {
		path := seeded(t)
		store := open(t, path)
		store.AddMigration(script)
		if err := operation.run(registry.NewLedger(store, nil)); err != nil {
			t.Fatalf("%s: %v", operation.name, err)
		}
		var version int
		if err := rawDB(t, path).QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != 2 {
			t.Errorf("%s: user_version = %d, want 2", operation.name, version)
		}
	}
}

func TestFailedUpgradeChangesNothing(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	store := open(t, path)
	store.AddMigration("CREATE TABLE later (id INTEGER); SELECT id FROM missing_table;")
	if _, err := listSiteIDs(t, store); err == nil {
		t.Fatal("a failed upgrade was accepted")
	}
	db := rawDB(t, path)
	var version, tables int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(),
		"SELECT count(*) FROM sqlite_schema WHERE name = 'later'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 1 || tables != 0 {
		t.Errorf("version = %d, later tables = %d; want the upgrade rolled back", version, tables)
	}
}

func TestRangeColumnsCoverBothFamilies(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	ledger := registry.NewLedger(open(t, path), nil)
	for _, pool := range []struct {
		cidr, start, end string
		family           int
	}{
		{"10.0.0.0/8", "10.0.0.0", "10.255.255.255", 4},
		{"0.0.0.0/32", "0.0.0.0", "0.0.0.0", 4},
		{"2001:db8::/32", "2001:db8::", "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", 6},
		{"::ffff:192.0.2.0/120", "192.0.2.0", "192.0.2.255", 4},
	} {
		if _, _, _, _, err := ledger.ReserveSubnetPool(
			t.Context(), tailnet, pool.cidr, "hq-vpc", registry.ReservationMetadata{},
		); err != nil {
			t.Fatalf("%s: %v", pool.cidr, err)
		}
		var (
			family     int
			start, end []byte
		)
		if err := rawDB(t, path).QueryRowContext(t.Context(),
			"SELECT family, range_start, range_end FROM subnet_pools WHERE cidr = ?", pool.cidr,
		).Scan(&family, &start, &end); err != nil {
			t.Fatal(err)
		}
		if family != pool.family || !bytes.Equal(start, address(t, pool.start)) ||
			!bytes.Equal(end, address(t, pool.end)) {
			t.Errorf("%s: family %d, range %x-%x", pool.cidr, family, start, end)
		}
	}
}
