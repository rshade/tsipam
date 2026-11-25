package sqlitestore_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/sqlitestore"
)

const (
	writableDirMode  = 0o700
	readOnlyDirMode  = 0o500
	readOnlyFileMode = 0o400
)

// dirState maps every name in dir to its content digest and modification time.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]string{}
	for _, entry := range entries {
		data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		state[entry.Name()] = fmt.Sprintf("%x %s", sha256.Sum256(data), info.ModTime())
	}
	return state
}

func rehearseReserve(t *testing.T, store registry.Store, id string) (bool, error) {
	t.Helper()
	_, _, _, changed, err := registry.NewLedger(store, nil).Rehearsal().ReserveSiteID(
		t.Context(), tailnet, id, "hq-vpc", registry.ReservationMetadata{},
	)
	return changed, err
}

func closedDatabase(t *testing.T, path string) {
	t.Helper()
	store, err := sqlitestore.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	reserveSiteID(t, store, "1")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRehearsalTouchesNoFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		prepare func(t *testing.T, path string)
		extra   string
	}{
		{name: "absent", prepare: func(*testing.T, string) {}},
		{name: "empty file", prepare: func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, nil, readOnlyFileMode); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "closed", prepare: func(t *testing.T, path string) { t.Helper(); closedDatabase(t, path) }},
		{name: "open elsewhere", prepare: func(t *testing.T, path string) {
			t.Helper()
			reserveSiteID(t, open(t, path), "1")
			if _, err := os.Stat(path + "-wal"); err != nil {
				t.Fatalf("the fixture has no log: %v", err)
			}
		}},
		{
			name:    "older schema",
			prepare: func(t *testing.T, path string) { t.Helper(); closedDatabase(t, path) },
			extra:   "CREATE TABLE added (id INTEGER PRIMARY KEY) STRICT;",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "tsipam.db")
			tc.prepare(t, path)
			before := dirState(t, dir)
			store := open(t, path)
			if tc.extra != "" {
				store.AddMigration(tc.extra)
			}
			changed, err := rehearseReserve(t, store, "2")
			if err != nil || !changed {
				t.Fatalf("rehearsal = %t, %v; want a change", changed, err)
			}
			store.IdentityValues(t.Context(), tailnet, true)
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			if after := dirState(t, dir); !reflect.DeepEqual(after, before) {
				t.Errorf("directory changed from %v to %v", before, after)
			}
		})
	}
}

func TestRehearsalNeedsNoWritableDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind this user")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tsipam.db")
	closedDatabase(t, path)
	if err := os.Chmod(path, readOnlyFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, readOnlyDirMode); err != nil {
		t.Fatal(err)
	}
	// The owner needs access again for the test directory to be removed.
	t.Cleanup(func() { _ = os.Chmod(dir, writableDirMode) })
	store := open(t, path)
	if changed, err := rehearseReserve(t, store, "1"); err != nil || changed {
		t.Errorf("rehearsed repeat = %t, %v; want no change", changed, err)
	}
	if changed, err := rehearseReserve(t, store, "2"); err != nil || !changed {
		t.Errorf("rehearsed reserve = %t, %v; want a change", changed, err)
	}
	if sites := store.IdentityValues(t.Context(), tailnet, true).Sites; len(sites) == 0 {
		t.Error("no identities were read")
	}
}

func TestRehearsalSeesUncheckpointedCommits(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	writer := open(t, path)
	for id := range 40 {
		reserveSiteID(t, writer, strconv.Itoa(id))
	}
	if _, _, err := registry.NewLedger(writer, nil).ReleaseSiteID(t.Context(), tailnet, "7"); err != nil {
		t.Fatal(err)
	}
	other := open(t, path)
	for id, want := range map[string]bool{"6": false, "7": true, "39": false, "40": true} {
		if changed, err := rehearseReserve(t, other, id); err != nil || changed != want {
			t.Errorf("rehearsed reserve %s = %t, %v; want %t", id, changed, err, want)
		}
	}
}

func TestRehearsalRecordsNoEvent(t *testing.T) {
	t.Parallel()
	store := open(t, filepath.Join(t.TempDir(), "tsipam.db"))
	reserveSiteID(t, store, "1")
	ctx := registry.WithActor(t.Context(), "user:alice@example.com")
	rehearsal := registry.NewLedger(store, nil).Rehearsal()
	if _, _, err := rehearsal.ReleaseSiteID(ctx, tailnet, "1"); err != nil {
		t.Fatal(err)
	}
	// Reads on a rehearsing ledger are unchanged, so it still lists history.
	events, err := rehearsal.History(t.Context(), tailnet, registry.HistoryFilter{Limit: 10})
	if err != nil || len(events) != 1 {
		t.Errorf("events = %v, %v; want only the reserve", events, err)
	}
	if sites := rehearsal.IdentityValues(t.Context(), tailnet).Sites; len(sites) == 0 {
		t.Error("a rehearsing ledger hides the identity values")
	}
}

// A rehearsal does not wait for a writer, and does not see its uncommitted
// changes.
func TestRehearsalIgnoresTheWriteLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	store := open(t, path)
	reserveSiteID(t, store, "1")
	err := store.Update(context.Background(), tailnet, registry.TxOptions{}, func(tx registry.Tx) error {
		if putErr := tx.PutSiteID("2", registry.Entry{Site: "branch"}); putErr != nil {
			return putErr
		}
		changed, rehearseErr := rehearseReserve(t, store, "2")
		if rehearseErr != nil || !changed {
			t.Errorf("rehearsal during a write = %t, %v; want a change", changed, rehearseErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRehearsalErrorsMatchTheRealOnes(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, path string){
		"newer schema": func(t *testing.T, path string) {
			t.Helper()
			closedDatabase(t, path)
			mustExec(t, rawDB(t, path), "PRAGMA user_version = 99")
		},
		"foreign database": func(t *testing.T, path string) {
			t.Helper()
			mustExec(t, rawDB(t, path), "CREATE TABLE other (id INTEGER)")
		},
		"not a database": func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte(strings.Repeat("not sqlite ", 100)), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"no parent directory": func(*testing.T, string) {},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "tsipam.db")
			if name == "no parent directory" {
				path = filepath.Join(filepath.Dir(path), "missing", "tsipam.db")
			}
			prepare(t, path)
			store := open(t, path)
			_, rehearsed := rehearseReserve(t, store, "1")
			_, _, _, _, actual := registry.NewLedger(store, nil).ReserveSiteID(
				t.Context(), tailnet, "1", "hq-vpc", registry.ReservationMetadata{},
			)
			if rehearsed == nil || actual == nil || rehearsed.Error() != actual.Error() {
				t.Errorf("rehearsed error = %v, real error = %v", rehearsed, actual)
			}
		})
	}
}
