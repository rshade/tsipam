package storetest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
)

const contractTailnet = "example.ts.net"

// NewStore builds a fresh store for one conformance case.
type NewStore func(t *testing.T) registry.Store

type commitFailer interface {
	FailNextCommit()
}

// Run executes the Store contract cases against newStore.
//
//nolint:gocognit,gocyclo,cyclop,funlen // one entry point runs every contract case as its own subtest
func Run(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("not found without create", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		err := store.View(context.Background(), contractTailnet, registry.TxOptions{}, func(registry.Snapshot) error {
			return nil
		})
		if !errors.Is(err, registry.ErrNotFound) {
			t.Fatalf("View error = %v, want ErrNotFound", err)
		}
		err = store.Update(context.Background(), contractTailnet, registry.TxOptions{}, func(registry.Tx) error {
			return nil
		})
		if !errors.Is(err, registry.ErrNotFound) {
			t.Fatalf("Update error = %v, want ErrNotFound", err)
		}
	})

	t.Run("create if missing", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		err := store.Update(
			context.Background(),
			contractTailnet,
			registry.TxOptions{CreateIfMissing: true},
			func(tx registry.Tx) error {
				return tx.PutSiteID("1", registry.Entry{Site: "hq"})
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		err = store.View(context.Background(), contractTailnet, registry.TxOptions{
			Require: []registry.MapKind{registry.MapSiteIDs},
		}, func(snap registry.Snapshot) error {
			sites, readErr := snap.SiteIDs()
			if readErr != nil {
				return readErr
			}
			got = sites["1"].Site
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != "hq" {
			t.Fatalf("site = %q, want hq", got)
		}
	})

	t.Run("rollback when fn errors after put", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		ctx := context.Background()
		tailnet := contractTailnet
		opts := registry.TxOptions{CreateIfMissing: true}
		if err := store.Update(ctx, tailnet, opts, func(tx registry.Tx) error {
			return tx.PutSiteID("1", registry.Entry{Site: "kept"})
		}); err != nil {
			t.Fatal(err)
		}
		boom := errors.New("boom")
		err := store.Update(ctx, tailnet, opts, func(tx registry.Tx) error {
			if putErr := tx.PutSiteID("1", registry.Entry{Site: "rolled-back"}); putErr != nil {
				return putErr
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Update error = %v, want boom", err)
		}
		var site string
		if err = store.View(ctx, tailnet, registry.TxOptions{}, func(snap registry.Snapshot) error {
			sites, readErr := snap.SiteIDs()
			if readErr != nil {
				return readErr
			}
			site = sites["1"].Site
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if site != "kept" {
			t.Fatalf("site = %q, want kept", site)
		}
	})

	t.Run("read your writes", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		err := store.Update(
			context.Background(),
			contractTailnet,
			registry.TxOptions{CreateIfMissing: true},
			func(tx registry.Tx) error {
				if putErr := tx.PutSiteID("1", registry.Entry{Site: "hq"}); putErr != nil {
					return putErr
				}
				sites, readErr := tx.SiteIDs()
				if readErr != nil {
					return readErr
				}
				if sites["1"].Site != "hq" {
					return errors.New("put was not visible")
				}
				removed, delErr := tx.DeleteSiteID("1")
				if delErr != nil || !removed {
					return errors.New("delete of written key failed")
				}
				sites, readErr = tx.SiteIDs()
				if readErr != nil {
					return readErr
				}
				if _, still := sites["1"]; still {
					return errors.New("delete was not visible")
				}
				return tx.PutSiteID("1", registry.Entry{Site: "hq"})
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("absent delete", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		err := store.Update(
			context.Background(),
			contractTailnet,
			registry.TxOptions{CreateIfMissing: true},
			func(tx registry.Tx) error {
				removed, delErr := tx.DeleteSiteID("9")
				if delErr != nil {
					return delErr
				}
				if removed {
					return errors.New("absent delete reported removed")
				}
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("commit error passthrough", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		failer, ok := store.(commitFailer)
		if !ok {
			t.Fatal("store does not implement FailNextCommit")
		}
		ctx := context.Background()
		tailnet := contractTailnet
		opts := registry.TxOptions{CreateIfMissing: true}
		if err := store.Update(ctx, tailnet, opts, func(registry.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
		failer.FailNextCommit()
		err := store.Update(ctx, tailnet, opts, func(tx registry.Tx) error {
			return tx.PutSiteID("1", registry.Entry{Site: "hq"})
		})
		if _, isCommit := errors.AsType[*registry.CommitError](err); !isCommit {
			t.Fatalf("Update error = %v, want *CommitError", err)
		}
		err = store.View(ctx, tailnet, registry.TxOptions{}, func(snap registry.Snapshot) error {
			sites, readErr := snap.SiteIDs()
			if readErr != nil {
				return readErr
			}
			if _, present := sites["1"]; present {
				return errors.New("commit failure persisted the put")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("concurrent updates lose no write", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		ctx := context.Background()
		tailnet := contractTailnet
		opts := registry.TxOptions{CreateIfMissing: true}
		if err := store.Update(ctx, tailnet, opts, func(tx registry.Tx) error {
			return tx.PutSiteID("1", registry.Entry{Site: "s", Description: "0"})
		}); err != nil {
			t.Fatal(err)
		}
		const updates = 10
		var wg sync.WaitGroup
		errs := make(chan error, updates)
		for range updates {
			wg.Go(func() {
				errs <- store.Update(ctx, tailnet, opts, func(tx registry.Tx) error {
					sites, readErr := tx.SiteIDs()
					if readErr != nil {
						return readErr
					}
					current, convErr := strconv.Atoi(sites["1"].Description)
					if convErr != nil {
						return convErr
					}
					return tx.PutSiteID("1", registry.Entry{
						Site:        "s",
						Description: strconv.Itoa(current + 1),
					})
				})
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		err := store.View(ctx, tailnet, registry.TxOptions{}, func(snap registry.Snapshot) error {
			sites, readErr := snap.SiteIDs()
			if readErr != nil {
				return readErr
			}
			if sites["1"].Description != strconv.Itoa(updates) {
				return errors.New("lost update: counter=" + sites["1"].Description)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	runLedgerRules(t, newStore)
}
