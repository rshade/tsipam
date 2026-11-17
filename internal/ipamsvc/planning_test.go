package ipamsvc_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/ipamsvc"
)

func TestPlanUsesOneRegistrySnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "alias.ipam.hujson")
	if err := os.WriteFile(path, []byte(`{"siteIDs":{"1":"branch"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc := &ipamsvc.Service{Ledger: testLedger(dir), CollectLive: func(
		_ context.Context, tailnet string, _ diagnostics.Logger,
	) (devices.Snapshot, error) {
		calls++
		if tailnet != "live.example.com" {
			t.Errorf("live source = %q, want environment tailnet", tailnet)
		}
		// A concurrent reservation update cannot change an in-flight snapshot.
		if err := os.WriteFile(path, []byte(`{"siteIDs":{"2":"new"}}`), 0o600); err != nil {
			return devices.Snapshot{}, err
		}
		return devices.Snapshot{}, nil
	}}
	got, err := svc.Plan(t.Context(), &ipam.PlanPayload{Tailnet: "alias", LiveTailnet: "live.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(got.Entries) != 1 || got.Entries[0].Key != "1" {
		t.Fatalf("calls = %d, report = %#v; want original registry snapshot", calls, got)
	}
}
