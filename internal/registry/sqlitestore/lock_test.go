package sqlitestore_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/sqlitestore"
)

const lockTimeoutMessage = "acquiring lock: timed out after 5s waiting for a concurrent tsipam to finish"

func TestLockTimeout(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	store := open(t, path)
	reserveSiteID(t, store, "1")

	conn, err := rawDB(t, path).Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err = conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = registry.NewLedger(store, nil).ReserveSiteID(
		t.Context(), tailnet, "2", "hq-vpc", registry.ReservationMetadata{},
	)
	if err == nil || err.Error() != lockTimeoutMessage {
		t.Fatalf("error = %v, want %q", err, lockTimeoutMessage)
	}
	if _, err = conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	entries, err := listSiteIDs(t, store)
	if err != nil || len(entries) != 1 {
		t.Errorf("list = %v, %v; want only the first reservation", entries, err)
	}
}

func TestLockTimeoutWithinOneStore(t *testing.T) {
	t.Parallel()
	store := open(t, filepath.Join(t.TempDir(), "tsipam.db"))
	reserveSiteID(t, store, "1")
	held, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.Update(context.Background(), tailnet, registry.TxOptions{}, func(registry.Tx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	err := store.Update(t.Context(), tailnet, registry.TxOptions{}, func(registry.Tx) error { return nil })
	close(release)
	if holderErr := <-done; holderErr != nil {
		t.Fatal(holderErr)
	}
	if err == nil || err.Error() != lockTimeoutMessage {
		t.Errorf("error = %v, want %q", err, lockTimeoutMessage)
	}
}

// Switching a database to WAL reports busy at once, without busy_timeout, while
// another connection holds its write lock. Concurrent first uses collide there.
func TestWALSwitchWaitsForAWriter(t *testing.T) {
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
	db := rawDB(t, path)
	mustExec(t, db, "PRAGMA journal_mode = DELETE")
	writer, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, err = writer.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, endErr := writer.ExecContext(context.Background(), "ROLLBACK")
		done <- endErr
	}()
	reserveSiteID(t, open(t, path), "2")
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHookRunsBeforeTheNextUpdate(t *testing.T) {
	t.Parallel()
	store := open(t, filepath.Join(t.TempDir(), "tsipam.db"))
	reserveSiteID(t, store, "1")

	var nextStarted, startedDuringHook atomic.Bool
	done := make(chan error, 1)
	ctx := registry.WithAfterSuccess(t.Context(), func() {
		go func() {
			done <- store.Update(context.Background(), tailnet, registry.TxOptions{}, func(registry.Tx) error {
				nextStarted.Store(true)
				return nil
			})
		}()
		time.Sleep(200 * time.Millisecond)
		startedDuringHook.Store(nextStarted.Load())
	})
	if err := store.Update(ctx, tailnet, registry.TxOptions{}, func(registry.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if startedDuringHook.Load() {
		t.Error("the next update started before the hook returned")
	}
}
