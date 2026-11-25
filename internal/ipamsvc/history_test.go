package ipamsvc_test

import (
	"errors"
	"path/filepath"
	"testing"

	goa "goa.design/goa/v3/pkg"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/ipamsvc"
	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/sqlitestore"
)

func sqliteService(t *testing.T) *ipamsvc.Service {
	t.Helper()
	store, err := sqlitestore.Open(filepath.Join(t.TempDir(), "tsipam.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &ipamsvc.Service{Ledger: registry.NewLedger(store, nil)}
}

func TestListHistoryOnSQLite(t *testing.T) {
	t.Parallel()
	svc := sqliteService(t)
	ctx := registry.WithActor(t.Context(), "user:alice@example.com")
	if _, err := svc.ReserveUDPPort(ctx, &ipam.ReserveUDPPortPayload{
		Tailnet: "alias", Site: "hq-vpc", Port: "41642", Device: "hq-fw01",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseUDPPort(t.Context(), &ipam.ReleaseUDPPortPayload{
		Tailnet: "alias", Site: "hq-vpc", Port: "41642",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := svc.ListHistory(t.Context(), &ipam.ListHistoryPayload{
		Tailnet: "alias", Kind: new("udp_port"), Key: new("41642"),
		Since: new("2020-01-01T00:00:00Z"), Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	released, reserved := events[0], events[1]
	if released.Action != "release" || released.After != nil || released.Actor != nil ||
		released.Before == nil || released.Before.Target != "hq-fw01" {
		t.Errorf("release = %+v", released)
	}
	if reserved.Action != "reserve" || reserved.Before != nil || reserved.Site == nil || *reserved.Site != "hq-vpc" ||
		reserved.Actor == nil || *reserved.Actor != "user:alice@example.com" {
		t.Errorf("reserve = %+v", reserved)
	}
}

func TestListHistoryErrors(t *testing.T) {
	t.Parallel()
	file := &ipamsvc.Service{Ledger: testLedger(t.TempDir())}
	sqlite := sqliteService(t)
	cases := []struct {
		name    string
		svc     *ipamsvc.Service
		payload *ipam.ListHistoryPayload
		want    string
	}{
		{"file backend", file, &ipam.ListHistoryPayload{Tailnet: "alias", Limit: 1}, "unsupported"},
		{"unknown kind", sqlite, &ipam.ListHistoryPayload{
			Tailnet: "alias", Kind: new("vlan"), Limit: 1}, "invalid_input"},
		{"bad since", sqlite, &ipam.ListHistoryPayload{
			Tailnet: "alias", Since: new("yesterday"), Limit: 1}, "invalid_input"},
		{"zero limit", sqlite, &ipam.ListHistoryPayload{Tailnet: "alias"}, "invalid_input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.svc.ListHistory(t.Context(), tc.payload)
			serviceErr, ok := errors.AsType[*goa.ServiceError](err)
			if !ok || serviceErr.Name != tc.want {
				t.Errorf("error = %v, want %s", err, tc.want)
			}
		})
	}
	_, err := sqlite.ListHistory(t.Context(), &ipam.ListHistoryPayload{Tailnet: "alias", Limit: 1})
	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("missing registry error = %v, want ErrNotFound", err)
	}
}
