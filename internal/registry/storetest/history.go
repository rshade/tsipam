package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rshade/tsipam/internal/registry"
)

const (
	historySite     = "lab-east"
	historyAddress  = "100.100.100.10"
	historyPool     = "10.90.0.0/24"
	historyTicket   = "AUD-1"
	historyPoolBits = 24
)

// historyFixture is a ledger over a history-keeping store with a settable
// clock.
type historyFixture struct {
	store  registry.Store
	ledger *registry.Ledger
	now    time.Time
}

// newHistoryFixture skips the case when the store keeps no history.
func newHistoryFixture(t *testing.T, newStore NewStore) *historyFixture {
	t.Helper()
	store := newStore(t)
	if _, ok := store.(registry.HistoryReader); !ok {
		t.Skip("store does not implement registry.HistoryReader")
	}
	fixture := &historyFixture{
		store:  store,
		ledger: registry.NewLedger(store, nil),
		now:    time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC),
	}
	fixture.ledger.SetNow(func() time.Time { return fixture.now })
	return fixture
}

func (f *historyFixture) history(t *testing.T, filter registry.HistoryFilter) []registry.Event {
	t.Helper()
	if filter.Limit == 0 {
		filter.Limit = registry.DefaultHistoryLimit
	}
	events, err := f.ledger.History(context.Background(), contractTailnet, filter)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	return events
}

func (f *historyFixture) reserveSiteID(
	ctx context.Context, t *testing.T, id, site string, metadata registry.ReservationMetadata,
) {
	t.Helper()
	if _, _, _, _, err := f.ledger.ReserveSiteID(ctx, contractTailnet, id, site, metadata); err != nil {
		t.Fatalf("ReserveSiteID(%s): %v", id, err)
	}
}

func runHistory(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("history", func(t *testing.T) {
		t.Parallel()
		t.Run("one event per changed reservation", func(t *testing.T) {
			t.Parallel()
			historyPerChange(t, newHistoryFixture(t, newStore))
		})
		t.Run("no-op and refusal record nothing", func(t *testing.T) {
			t.Parallel()
			historyNoOps(t, newHistoryFixture(t, newStore))
		})
		t.Run("rollback discards the event", func(t *testing.T) {
			t.Parallel()
			historyRollback(t, newHistoryFixture(t, newStore))
		})
		t.Run("amendment keeps before and after", func(t *testing.T) {
			t.Parallel()
			historyAmendment(t, newHistoryFixture(t, newStore))
		})
		t.Run("release keeps the released value", func(t *testing.T) {
			t.Parallel()
			historyRelease(t, newHistoryFixture(t, newStore))
		})
		t.Run("actor comes from the context", func(t *testing.T) {
			t.Parallel()
			historyActor(t, newHistoryFixture(t, newStore))
		})
		t.Run("filters and order", func(t *testing.T) {
			t.Parallel()
			historyFilters(t, newHistoryFixture(t, newStore))
		})
		t.Run("absent registry is not found", func(t *testing.T) {
			t.Parallel()
			fixture := newHistoryFixture(t, newStore)
			_, err := fixture.ledger.History(
				context.Background(), contractTailnet, registry.HistoryFilter{Limit: 1},
			)
			if !errors.Is(err, registry.ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound", err)
			}
		})
	})
}

func historyPerChange(t *testing.T, f *historyFixture) {
	t.Helper()
	ctx := context.Background()
	f.reserveSiteID(ctx, t, "7", historySite, registry.ReservationMetadata{Ticket: historyTicket})
	if _, _, _, _, err := f.ledger.AllocateSubnetPool(
		ctx, contractTailnet, "10.90.0.0/16", historyPoolBits, historySite, nil, registry.ReservationMetadata{},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := f.ledger.ReserveServiceAddress(
		ctx, contractTailnet, historyAddress, "dashboard", registry.ReservationMetadata{},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := f.ledger.ReserveUDPPort(
		ctx, contractTailnet, historySite, "41642", "hq-fw01",
	); err != nil {
		t.Fatal(err)
	}

	const at = "2026-03-01T12:00:00Z"
	want := []registry.Event{
		{
			At: at, Action: registry.ActionReserve, Kind: registry.MapUDPPorts, Key: "41642", Site: historySite,
			After: &registry.EventValue{Target: "hq-fw01"},
		},
		{
			At: at, Action: registry.ActionReserve, Kind: registry.MapServiceAddresses, Key: historyAddress,
			After: &registry.EventValue{Target: "svc:dashboard"},
		},
		{
			At: at, Action: registry.ActionAllocate, Kind: registry.MapSubnetPools, Key: historyPool,
			After: &registry.EventValue{Target: historySite},
		},
		{
			At: at, Action: registry.ActionReserve, Kind: registry.MapSiteIDs, Key: "7",
			After: &registry.EventValue{
				Target: historySite, Ticket: historyTicket, ReservedAt: at,
			},
		},
	}
	if got := f.history(t, registry.HistoryFilter{}); !reflect.DeepEqual(got, want) {
		t.Errorf("events = %+v\nwant %+v", got, want)
	}
}

func historyNoOps(t *testing.T, f *historyFixture) {
	t.Helper()
	ctx := context.Background()
	f.reserveSiteID(ctx, t, "7", historySite, registry.ReservationMetadata{})
	f.reserveSiteID(ctx, t, "7", historySite, registry.ReservationMetadata{})
	if _, _, _, _, err := f.ledger.ReserveSiteID(
		ctx, contractTailnet, "7", "branch-office", registry.ReservationMetadata{},
	); err == nil {
		t.Fatal("collision was accepted")
	}
	if _, changed, err := f.ledger.ReleaseSiteID(ctx, contractTailnet, "8"); err != nil || changed {
		t.Fatalf("absent release = %t, %v", changed, err)
	}
	if got := f.history(t, registry.HistoryFilter{}); len(got) != 1 {
		t.Errorf("events = %+v, want only the first reserve", got)
	}
}

func historyRollback(t *testing.T, f *historyFixture) {
	t.Helper()
	ctx := context.Background()
	boom := errors.New("boom")
	err := f.store.Update(ctx, contractTailnet, registry.TxOptions{
		CreateIfMissing: true, Action: registry.ActionReserve,
	}, func(tx registry.Tx) error {
		if putErr := tx.PutSiteID("7", registry.Entry{Site: historySite}); putErr != nil {
			return putErr
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update error = %v, want boom", err)
	}
	if got := f.history(t, registry.HistoryFilter{}); len(got) != 0 {
		t.Errorf("events = %+v, want none after rollback", got)
	}
}

func historyAmendment(t *testing.T, f *historyFixture) {
	t.Helper()
	ctx := context.Background()
	for _, ticket := range []string{historyTicket, "AUD-2"} {
		if _, _, _, _, err := f.ledger.ReserveSubnetPool(
			ctx, contractTailnet, historyPool, historySite, registry.ReservationMetadata{Ticket: ticket},
		); err != nil {
			t.Fatal(err)
		}
	}
	events := f.history(t, registry.HistoryFilter{Limit: 1})
	if len(events) != 1 || events[0].Before == nil || events[0].After == nil {
		t.Fatalf("events = %+v, want one amendment with before and after", events)
	}
	if events[0].Before.Ticket != historyTicket || events[0].After.Ticket != "AUD-2" {
		t.Errorf("tickets = %q -> %q, want AUD-1 -> AUD-2", events[0].Before.Ticket, events[0].After.Ticket)
	}
}

//nolint:mnd // the counts are the fixture's own event counts
func historyRelease(t *testing.T, f *historyFixture) {
	t.Helper()
	ctx := context.Background()
	if _, _, _, _, err := f.ledger.ReserveServiceAddress(
		ctx, contractTailnet, historyAddress, "dashboard", registry.ReservationMetadata{},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.ledger.ReleaseServiceAddress(ctx, contractTailnet, historyAddress); err != nil {
		t.Fatal(err)
	}
	events := f.history(t, registry.HistoryFilter{Key: historyAddress})
	if len(events) != 2 {
		t.Fatalf("events = %+v, want the release and the earlier reserve", events)
	}
	released := events[0]
	if released.Action != registry.ActionRelease || released.After != nil ||
		released.Before == nil || released.Before.Target != "svc:dashboard" {
		t.Errorf("release event = %+v, want before svc:dashboard and no after", released)
	}
}

//nolint:mnd // the counts are the fixture's own event counts
func historyActor(t *testing.T, f *historyFixture) {
	t.Helper()
	f.reserveSiteID(context.Background(), t, "7", historySite, registry.ReservationMetadata{})
	f.reserveSiteID(
		registry.WithActor(context.Background(), "user:alice@example.com"), t,
		"8", "branch-office", registry.ReservationMetadata{},
	)
	events := f.history(t, registry.HistoryFilter{})
	if len(events) != 2 {
		t.Fatalf("events = %+v, want two", events)
	}
	if events[0].Actor != "user:alice@example.com" || events[1].Actor != "" {
		t.Errorf("actors = %q, %q; want the context actor, then none", events[0].Actor, events[1].Actor)
	}
}

//nolint:mnd // the counts are the fixture's own event counts
func historyFilters(t *testing.T, f *historyFixture) {
	t.Helper()
	ctx := context.Background()
	start := f.now
	f.reserveSiteID(ctx, t, "1", historySite, registry.ReservationMetadata{})
	f.now = start.Add(time.Hour)
	f.reserveSiteID(ctx, t, "2", historySite, registry.ReservationMetadata{})
	f.now = f.now.Add(time.Hour)
	if _, _, _, _, err := f.ledger.ReserveSubnetPool(
		ctx, contractTailnet, historyPool, historySite, registry.ReservationMetadata{},
	); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		filter registry.HistoryFilter
		want   []string
	}{
		{name: "newest first", want: []string{historyPool, "2", "1"}},
		{name: "kind", filter: registry.HistoryFilter{Kind: registry.MapSiteIDs}, want: []string{"2", "1"}},
		{name: "key", filter: registry.HistoryFilter{Key: "1"}, want: []string{"1"}},
		{
			name:   "since is inclusive",
			filter: registry.HistoryFilter{Since: start.Add(time.Hour)},
			want:   []string{historyPool, "2"},
		},
		{
			name:   "since with a fraction excludes the whole second before it",
			filter: registry.HistoryFilter{Since: start.Add(time.Hour + time.Millisecond)},
			want:   []string{historyPool},
		},
		{
			name: "since in another zone",
			filter: registry.HistoryFilter{
				Since: f.now.In(time.FixedZone("", -6*60*60)),
			},
			want: []string{historyPool},
		},
		{name: "limit", filter: registry.HistoryFilter{Limit: 2}, want: []string{historyPool, "2"}},
		{name: "no match", filter: registry.HistoryFilter{Key: "99"}, want: []string{}},
	}
	for _, tc := range cases {
		keys := []string{}
		for _, event := range f.history(t, tc.filter) {
			keys = append(keys, event.Key)
		}
		if !reflect.DeepEqual(keys, tc.want) {
			t.Errorf("%s: keys = %v, want %v", tc.name, keys, tc.want)
		}
	}
	if _, err := f.ledger.History(ctx, contractTailnet, registry.HistoryFilter{}); err == nil {
		t.Error("a zero limit was accepted")
	}
}
