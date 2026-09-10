package ipamsvc_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/ipamsvc"
)

func TestCheckPolicyService(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "alias.ipam.hujson")
	if err := os.WriteFile(
		path,
		[]byte(`{"subnetPools":{"192.0.2.0/24":"office"},"siteIDs":false}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	svc := &ipamsvc.Service{RegistryDir: dir}
	payload := &ipam.CheckPolicyPayload{Tailnet: "alias", Policy: `{"ipsets":{"ipset:a":["192.0.2.0/25"]}}`}
	report, err := svc.CheckPolicy(t.Context(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if report.DriftCount != 0 || len(report.Authored) != 1 || len(report.Reserved) != 1 ||
		report.Authored[0].Pools[0] != "192.0.2.0/24" || report.Reserved[0].IPSets[0] != "ipset:a" {
		t.Fatalf("wrong result: %+v", report)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, canceledErr := svc.CheckPolicy(ctx, payload); canceledErr == nil || result != nil {
		t.Fatalf("canceled check = %+v, %v", result, canceledErr)
	}
	payload.Policy = `{"ipsets":{"ipset:a":["autogroup:internet"]}}`
	if result, invalidErr := svc.CheckPolicy(t.Context(), payload); invalidErr == nil || result != nil {
		t.Fatalf("incomplete check = %+v, %v", result, invalidErr)
	}
}
