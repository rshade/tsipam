package storetest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rshade/tsipam/internal/registry"
)

// rehearsalBlockSize is the child prefix length every allocate case asks for.
const rehearsalBlockSize = 24

func runRehearsal(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("rehearsal", func(t *testing.T) {
		t.Run("store", func(t *testing.T) {
			runStoreRehearsal(t, newStore)
		})
		t.Run("ledger", func(t *testing.T) {
			runLedgerRehearsal(t, newStore)
		})
	})
}

//nolint:gocognit,funlen,goconst // one entry point runs every rule 9 case as its own subtest
func runStoreRehearsal(t *testing.T, newStore NewStore) {
	t.Helper()
	dryRun := registry.TxOptions{DryRun: true, CreateIfMissing: true}
	putBranch := func(tx registry.Tx) error {
		return tx.PutSiteID("2", registry.Entry{Site: "branch"})
	}

	t.Run("persists nothing", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		seedSiteID(t, store)
		if err := store.Update(context.Background(), contractTailnet, dryRun, putBranch); err != nil {
			t.Fatal(err)
		}
		if err := store.Update(context.Background(), contractTailnet, dryRun, func(tx registry.Tx) error {
			_, delErr := tx.DeleteSiteID("1")
			return delErr
		}); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"1": "hq"}
		if got := siteIDs(t, store); !reflect.DeepEqual(got, want) {
			t.Fatalf("site IDs = %v, want %v", got, want)
		}
	})

	t.Run("creates nothing", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		called := false
		err := store.Update(context.Background(), contractTailnet, dryRun, func(tx registry.Tx) error {
			called = true
			if counts := tx.Counts(); counts != (registry.Counts{}) {
				return errors.New("an absent registry did not rehearse as empty")
			}
			return putBranch(tx)
		})
		if err != nil || !called {
			t.Fatalf("Update = %v, fn called = %t; want nil and true", err, called)
		}
		assertNoRegistry(t, store)
	})

	t.Run("not found without create", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		err := store.Update(
			context.Background(), contractTailnet, registry.TxOptions{DryRun: true},
			func(registry.Tx) error { return errors.New("fn ran") },
		)
		if !errors.Is(err, registry.ErrNotFound) {
			t.Fatalf("Update error = %v, want ErrNotFound", err)
		}
		assertNoRegistry(t, store)
	})

	t.Run("skips the after-success hook", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		seedSiteID(t, store)
		ran := false
		ctx := registry.WithAfterSuccess(context.Background(), func() { ran = true })
		if err := store.Update(ctx, contractTailnet, dryRun, putBranch); err != nil {
			t.Fatal(err)
		}
		noChange := func(registry.Tx) error { return nil }
		if err := store.Update(ctx, contractTailnet, dryRun, noChange); err != nil {
			t.Fatal(err)
		}
		if ran {
			t.Fatal("the after-success hook ran in a rehearsal")
		}
	})

	t.Run("returns the error fn returns", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		boom := errors.New("boom")
		err := store.Update(context.Background(), contractTailnet, dryRun, func(registry.Tx) error { return boom })
		if !errors.Is(err, boom) {
			t.Fatalf("Update error = %v, want boom", err)
		}
		assertNoRegistry(t, store)
	})

	t.Run("reads its writes", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		err := store.Update(context.Background(), contractTailnet, dryRun, func(tx registry.Tx) error {
			if putErr := putBranch(tx); putErr != nil {
				return putErr
			}
			sites, readErr := tx.SiteIDs()
			if readErr != nil {
				return readErr
			}
			if sites["2"].Site != "branch" {
				return errors.New("put was not visible")
			}
			removed, delErr := tx.DeleteSiteID("2")
			if delErr != nil || !removed {
				return errors.New("delete of written key failed")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("is not a commit", func(t *testing.T) {
		t.Parallel()
		store := newStore(t)
		seedSiteID(t, store)
		failer, ok := store.(commitFailer)
		if !ok {
			t.Fatal("store does not implement FailNextCommit")
		}
		failer.FailNextCommit()
		if err := store.Update(context.Background(), contractTailnet, dryRun, putBranch); err != nil {
			t.Fatalf("rehearsal hit the commit failure: %v", err)
		}
	})
}

func seedSiteID(t *testing.T, store registry.Store) {
	t.Helper()
	err := store.Update(
		context.Background(), contractTailnet, registry.TxOptions{CreateIfMissing: true},
		func(tx registry.Tx) error { return tx.PutSiteID("1", registry.Entry{Site: "hq"}) },
	)
	if err != nil {
		t.Fatal(err)
	}
}

func siteIDs(t *testing.T, store registry.Store) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := store.View(context.Background(), contractTailnet, registry.TxOptions{}, func(snap registry.Snapshot) error {
		sites, readErr := snap.SiteIDs()
		for id, entry := range sites {
			got[id] = entry.Site
		}
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func assertNoRegistry(t *testing.T, store registry.Store) {
	t.Helper()
	err := store.View(context.Background(), contractTailnet, registry.TxOptions{}, func(registry.Snapshot) error {
		return nil
	})
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("View error = %v, want ErrNotFound: the rehearsal created a registry", err)
	}
}

// outcome is everything a ledger mutation tells its caller.
type outcome struct {
	values   []string
	metadata registry.ReservationMetadata
	changed  bool
	err      string
}

func outcomeOf(
	first, second string, metadata registry.ReservationMetadata, changed bool, err error,
) outcome {
	result := outcome{values: []string{first, second}, metadata: metadata, changed: changed}
	if err != nil {
		result.err = err.Error()
	}
	return result
}

type mutation func(context.Context, *registry.Ledger) outcome

type rehearsalCase struct {
	name string
	// absent leaves the registry missing instead of seeding it.
	absent bool
	run    mutation
	// want pins the part of the outcome that proves the case is reached.
	changed   bool
	wantError string
}

func reserveSite(id, site string, metadata registry.ReservationMetadata) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		return outcomeOf(led.ReserveSiteID(ctx, contractTailnet, id, site, metadata))
	}
}

func releaseSite(id string) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		released, changed, err := led.ReleaseSiteID(ctx, contractTailnet, id)
		return outcomeOf(released, "", registry.ReservationMetadata{}, changed, err)
	}
}

func reservePool(cidr, site string) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		return outcomeOf(led.ReserveSubnetPool(ctx, contractTailnet, cidr, site, registry.ReservationMetadata{}))
	}
}

func allocatePool(pool, site, ticket string, live ...string) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		return outcomeOf(led.AllocateSubnetPool(
			ctx, contractTailnet, pool, rehearsalBlockSize, site, live, registry.ReservationMetadata{Ticket: ticket},
		))
	}
}

func releasePool(cidr string) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		released, changed, err := led.ReleaseSubnetPool(ctx, contractTailnet, cidr)
		return outcomeOf(released, "", registry.ReservationMetadata{}, changed, err)
	}
}

func reserveAddress(address, service string) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		return outcomeOf(led.ReserveServiceAddress(
			ctx, contractTailnet, address, service, registry.ReservationMetadata{},
		))
	}
}

func releaseAddress(address string) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		released, changed, err := led.ReleaseServiceAddress(ctx, contractTailnet, address)
		return outcomeOf(released, "", registry.ReservationMetadata{}, changed, err)
	}
}

func reservePort(site, port, device string) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		gotSite, gotPort, gotDevice, changed, err := led.ReserveUDPPort(ctx, contractTailnet, site, port, device)
		return outcomeOf(gotSite, gotPort+" "+gotDevice, registry.ReservationMetadata{}, changed, err)
	}
}

func releasePort(site, port string) mutation {
	return func(ctx context.Context, led *registry.Ledger) outcome {
		gotSite, gotPort, changed, err := led.ReleaseUDPPort(ctx, contractTailnet, site, port)
		return outcomeOf(gotSite, gotPort, registry.ReservationMetadata{}, changed, err)
	}
}

func seededReservations() []mutation {
	return []mutation{
		reserveSite("1", "hq", registry.ReservationMetadata{}),
		allocatePool("10.0.0.0/16", "hq", "OPS-1"),
		reserveAddress("100.100.100.7", "payments"),
		reservePort("hq", "41642", "fw01"),
	}
}

//nolint:goconst // reservation fixtures repeat the same identities
func rehearsalCases() []rehearsalCase {
	owned := registry.ReservationMetadata{Owner: "netops"}
	return []rehearsalCase{
		{name: "site ID reserve", run: reserveSite("2", "branch", owned), changed: true},
		{
			name:    "site ID reserve in an absent registry",
			absent:  true,
			run:     reserveSite("1", "hq", owned),
			changed: true,
		},
		{name: "site ID reserve again", run: reserveSite("1", "hq", registry.ReservationMetadata{})},
		{name: "site ID reserve adds metadata", run: reserveSite("1", "hq", owned), changed: true},
		{name: "site ID collision", run: reserveSite("1", "branch", owned), wantError: "already reserved"},
		{
			name: "site ID out of range", run: reserveSite("65536", "hq", owned),
			wantError: "out of range",
		},
		{name: "site ID release", run: releaseSite("1"), changed: true},
		{name: "site ID release again", run: releaseSite("9")},
		{
			name: "site ID release in an absent registry", absent: true, run: releaseSite("1"),
			wantError: "registry",
		},
		{name: "subnet reserve", run: reservePool("10.1.0.0/24", "hq"), changed: true},
		{name: "subnet overlap", run: reservePool("10.0.0.0/8", "branch"), wantError: "overlaps"},
		{name: "subnet allocate", run: allocatePool("10.0.0.0/16", "branch", "OPS-2"), changed: true},
		{
			name: "subnet allocate around a live route",
			run:  allocatePool("10.0.0.0/16", "branch", "", "10.0.1.0/24"), changed: true,
		},
		{name: "subnet allocate replays a ticket", run: allocatePool("10.0.0.0/16", "hq", "OPS-1")},
		{
			name: "subnet allocate exhausted", run: allocatePool("10.0.0.0/24", "branch", ""),
			wantError: "no free /24 block",
		},
		{
			name: "subnet allocate exhausted in an absent registry", absent: true,
			run: allocatePool("10.0.0.0/24", "hq", "", "10.0.0.0/8"), wantError: "no free /24 block",
		},
		{name: "subnet release", run: releasePool("10.0.0.0/24"), changed: true},
		{name: "subnet release again", run: releasePool("10.9.0.0/24")},
		{name: "service reserve", run: reserveAddress("100.100.100.8", "search"), changed: true},
		{name: "service collision", run: reserveAddress("100.100.100.7", "search"), wantError: "already reserved"},
		{name: "service release", run: releaseAddress("100.100.100.7"), changed: true},
		{name: "service release again", run: releaseAddress("100.100.100.9")},
		{name: "port reserve", run: reservePort("branch", "41642", "fw02"), changed: true},
		{name: "port collision", run: reservePort("hq", "41642", "fw02"), wantError: "already reserved"},
		{name: "port release", run: releasePort("hq", "41642"), changed: true},
		{name: "port release again", run: releasePort("branch", "41642")},
	}
}

// runLedgerRehearsal checks that a rehearsal reports what the real call then
// does against the same registry, and that rehearsing first changed nothing.
func runLedgerRehearsal(t *testing.T, newStore NewStore) {
	t.Helper()
	for _, test := range rehearsalCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			logger := &eventLogger{}
			led := registry.NewLedger(newStore(t), logger)
			led.SetNow(func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC) })
			if !test.absent {
				for _, seed := range seededReservations() {
					if got := seed(ctx, led); got.err != "" {
						t.Fatal(got.err)
					}
				}
			}

			logger.reset()
			rehearsed := test.run(ctx, led.Rehearsal())
			assertNoWriteEvent(t, logger.events())
			committed := test.run(ctx, led)
			assertRehearsed(t, test, rehearsed, committed)
		})
	}
}

func assertNoWriteEvent(t *testing.T, events []string) {
	t.Helper()
	for _, event := range events {
		if strings.Contains(event, "written") || strings.Contains(event, "removed") {
			t.Errorf("the rehearsal logged %q", event)
		}
	}
}

func assertRehearsed(t *testing.T, test rehearsalCase, rehearsed, committed outcome) {
	t.Helper()
	if !reflect.DeepEqual(normalizeNotFound(rehearsed), normalizeNotFound(committed)) {
		t.Errorf("rehearsed %+v, committed %+v", rehearsed, committed)
	}
	if rehearsed.changed != test.changed || !strings.Contains(rehearsed.err, test.wantError) ||
		(test.wantError == "") != (rehearsed.err == "") {
		t.Errorf("rehearsed %+v, want changed=%t error containing %q", rehearsed, test.changed, test.wantError)
	}
}

// normalizeNotFound drops the lock step a real Update names when it fails
// before reading; a rehearsal takes no lock.
func normalizeNotFound(result outcome) outcome {
	result.err = strings.Replace(result.err, "acquiring lock: ", "", 1)
	return result
}

type eventLogger struct {
	mu   sync.Mutex
	seen []string
}

func (l *eventLogger) Debug(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, msg)
}

func (l *eventLogger) Warn(msg string, _ ...any) { l.Debug(msg) }

func (l *eventLogger) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = nil
}

func (l *eventLogger) events() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.seen...)
}
