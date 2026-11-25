package sqlitestore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/sqlitestore"
	"github.com/rshade/tsipam/internal/registry/storetest"
)

const tailnet = "example.ts.net"

func open(t *testing.T, path string) *sqlitestore.Store {
	t.Helper()
	store, err := sqlitestore.Open(path, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	})
	return store
}

// rawDB opens the database the way a program other than tsipam would.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func reserveSiteID(t *testing.T, store registry.Store, id string) {
	t.Helper()
	_, _, _, _, err := registry.NewLedger(store, nil).ReserveSiteID(
		t.Context(), tailnet, id, "hq-vpc", registry.ReservationMetadata{},
	)
	if err != nil {
		t.Fatalf("ReserveSiteID(%s): %v", id, err)
	}
}

func listSiteIDs(t *testing.T, store registry.Store) ([]registry.SiteIDEntry, error) {
	t.Helper()
	return registry.NewLedger(store, nil).ListSiteIDs(t.Context(), tailnet)
}

func TestConformance(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T) registry.Store {
		t.Helper()
		return open(t, filepath.Join(t.TempDir(), "tsipam.db"))
	})
}

func TestReadsCreateNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "absent.db")
	store := open(t, path)
	ledger := registry.NewLedger(store, nil)

	if _, err := ledger.ListSiteIDs(t.Context(), tailnet); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("list error = %v, want ErrNotFound", err)
	}
	if _, _, err := ledger.ReleaseSiteID(t.Context(), tailnet, "1"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("release error = %v, want ErrNotFound", err)
	}
	_, err := ledger.History(t.Context(), tailnet, registry.HistoryFilter{Limit: 1})
	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("history error = %v, want ErrNotFound", err)
	}
	ledger.IdentityValues(t.Context(), tailnet)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("reads created %v", entries)
	}
}

func TestNotFoundErrorHidesThePath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "acme-ipam", "tsipam.db")
	store := open(t, path)
	_, listErr := listSiteIDs(t, store)
	_, _, _, _, reserveErr := registry.NewLedger(store, nil).ReserveSiteID(
		t.Context(), tailnet, "1", "hq-vpc", registry.ReservationMetadata{},
	)
	for _, err := range []error{listErr, reserveErr} {
		if err == nil {
			t.Fatal("a database in a missing directory was accepted")
		}
		if strings.Contains(err.Error(), "acme-ipam") || strings.Contains(err.Error(), "tsipam.db") {
			t.Errorf("error names the path: %v", err)
		}
	}
}

func TestTailnetsAreIsolated(t *testing.T) {
	t.Parallel()
	store := open(t, filepath.Join(t.TempDir(), "tsipam.db"))
	reserveSiteID(t, store, "1")
	_, err := registry.NewLedger(store, nil).ListSiteIDs(t.Context(), "staging")
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound for the other tailnet", err)
	}
	want := `no registry for tailnet "staging" in the selected sqlite database (a reserve command creates it)`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// The file backend leaves an empty registry behind when the operation that
// created it is refused, so a following list succeeds.
func TestRefusedCreateLeavesAnEmptyRegistry(t *testing.T) {
	t.Parallel()
	store := open(t, filepath.Join(t.TempDir(), "tsipam.db"))
	boom := errors.New("boom")
	err := store.Update(t.Context(), tailnet, registry.TxOptions{CreateIfMissing: true}, func(tx registry.Tx) error {
		if putErr := tx.PutSiteID("1", registry.Entry{Site: "hq-vpc"}); putErr != nil {
			return putErr
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update error = %v, want boom", err)
	}
	entries, err := listSiteIDs(t, store)
	if err != nil || len(entries) != 0 {
		t.Errorf("list = %v, %v; want an empty registry", entries, err)
	}
}

func TestDatabaseFilesAreOwnerOnly(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file modes are a POSIX property")
	}
	path := filepath.Join(t.TempDir(), "tsipam.db")
	reserveSiteID(t, open(t, path), "1")
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", filepath.Base(name), info.Mode().Perm())
		}
	}
}

func TestExistingPermissionsAreKept(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file modes are a POSIX property")
	}
	path := filepath.Join(t.TempDir(), "tsipam.db")
	reserveSiteID(t, open(t, path), "1")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	reserveSiteID(t, open(t, path), "2")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want the operator's 0640", info.Mode().Perm())
	}
}

func TestCloseLeavesOnlyTheDatabase(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	store, err := sqlitestore.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	reserveSiteID(t, store, "1")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path + "-wal", path + "-shm"} {
		if _, statErr := os.Stat(name); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("%s remains after Close", filepath.Base(name))
		}
	}
}

func TestPathIsTakenVerbatim(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a?mode=memory.db", "a%20b.db", "a b#c.db", ":memory:", "file:x.db"} {
		if runtime.GOOS == "windows" && strings.ContainsAny(name, "?:") {
			continue
		}
		path := filepath.Join(t.TempDir(), name)
		reserveSiteID(t, open(t, path), "1")
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Errorf("database %q was not written at its own path: %v", name, err)
		}
	}
}

func TestEmptyFileIsUpgradedOnWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	store := open(t, path)
	if _, err := listSiteIDs(t, store); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("list error = %v, want ErrNotFound", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("a list wrote to the empty file: %v, %v", info, err)
	}
	reserveSiteID(t, store, "1")
	var version int
	if err := rawDB(t, path).QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1", version)
	}
}

// writeDatabase creates a database the way another program would, and closes
// it so the file is complete.
func writeDatabase(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		mustExec(t, db, statement)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewerSchemaIsRefusedUnchanged(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	closedDatabase(t, path)
	writeDatabase(t, path, "PRAGMA user_version = 99")
	assertRefusedUnchanged(t, path, "a newer tsipam is required")
}

func TestForeignDatabaseIsRefusedUnchanged(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	writeDatabase(t, path, "CREATE TABLE bookmarks (url TEXT)")
	assertRefusedUnchanged(t, path, "tsipam did not create")
}

func TestNonDatabaseIsRefusedUnchanged(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	content := strings.Repeat("not a database, but long enough to have a header ", 64)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	assertRefusedUnchanged(t, path, "is not a SQLite database")
}

func assertRefusedUnchanged(t *testing.T, path, want string) {
	t.Helper()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlitestore.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, listErr := listSiteIDs(t, store)
	_, _, _, _, reserveErr := registry.NewLedger(store, nil).ReserveSiteID(
		t.Context(), tailnet, "1", "hq-vpc", registry.ReservationMetadata{},
	)
	for _, opErr := range []error{listErr, reserveErr} {
		if opErr == nil || !strings.Contains(opErr.Error(), want) {
			t.Errorf("error = %v, want it to say %q", opErr, want)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the refused database was modified")
	}
}

func TestReadsProceedDuringAWrite(t *testing.T) {
	t.Parallel()
	store := open(t, filepath.Join(t.TempDir(), "tsipam.db"))
	reserveSiteID(t, store, "1")
	err := store.Update(t.Context(), tailnet, registry.TxOptions{}, func(tx registry.Tx) error {
		if putErr := tx.PutSiteID("2", registry.Entry{Site: "hq-vpc"}); putErr != nil {
			return putErr
		}
		entries, listErr := listSiteIDs(t, store)
		if listErr != nil {
			return listErr
		}
		if len(entries) != 1 {
			return fmt.Errorf("a reader saw uncommitted state: %v", entries)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// After SIGTERM the server cancels in-flight requests. A canceled Update must
// leave nothing behind.
func TestCanceledUpdateWritesNothing(t *testing.T) {
	t.Parallel()
	store := open(t, filepath.Join(t.TempDir(), "tsipam.db"))
	reserveSiteID(t, store, "1")
	ctx, cancel := context.WithCancel(t.Context())
	err := store.Update(ctx, tailnet, registry.TxOptions{}, func(tx registry.Tx) error {
		if putErr := tx.PutSiteID("2", registry.Entry{Site: "hq-vpc"}); putErr != nil {
			return putErr
		}
		cancel()
		return nil
	})
	if err == nil {
		t.Error("a canceled update reported success")
	}
	entries, err := listSiteIDs(t, store)
	if err != nil || len(entries) != 1 {
		t.Errorf("list = %v, %v; want only the first reservation", entries, err)
	}
}

func TestConcurrentAllocationsAreDistinct(t *testing.T) {
	t.Parallel()
	const callers = 10
	path := filepath.Join(t.TempDir(), "tsipam.db")
	cidrs := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		store := open(t, path)
		wg.Go(func() {
			cidrs[i], _, _, _, errs[i] = registry.NewLedger(store, nil).AllocateSubnetPool(
				context.Background(), tailnet, "10.40.0.0/16", 24, fmt.Sprintf("site-%d", i), nil,
				registry.ReservationMetadata{},
			)
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	for i, left := range cidrs {
		for _, right := range cidrs[i+1:] {
			if netip.MustParsePrefix(left).Overlaps(netip.MustParsePrefix(right)) {
				t.Errorf("%s and %s overlap", left, right)
			}
		}
	}
	events, err := registry.NewLedger(open(t, path), nil).History(t.Context(), tailnet, registry.HistoryFilter{
		Kind: registry.MapSubnetPools, Limit: registry.DefaultHistoryLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != callers {
		t.Fatalf("events = %d, want %d", len(events), callers)
	}
	for _, event := range events {
		if event.Action != registry.ActionAllocate {
			t.Errorf("event action = %s, want allocate", event.Action)
		}
	}
}
