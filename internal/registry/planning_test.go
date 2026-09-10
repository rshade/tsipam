package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

func TestLoadForPlanningValidatesObservableMaps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, contents string
		wantError      bool
	}{
		{"empty", `{}`, false},
		{"unobservable ports", `{"udpPorts":false}`, false},
		{"site IDs", `{"siteIDs":{"65536":"branch"}}`, true},
		{"pools", `{"subnetPools":{"10.0.0.1/24":"branch"}}`, true},
		{"services", `{"serviceAddresses":{"invalid":"svc:one"}}`, true},
		{"ambiguous aliases", `{"siteIDs":{"1":"one","01":"two"}}`, true},
		{"invalid document", `{`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "registry.hujson")
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := registry.LoadForPlanning(path)
			if (err != nil) != tc.wantError {
				t.Fatalf("LoadForPlanning error = %v, want error %t", err, tc.wantError)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || string(after) != tc.contents {
				t.Fatalf("registry modified: %s, %v", after, readErr)
			}
		})
	}
}

func TestLoadForPlanningDoesNotCreateMissingRegistry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing.hujson")
	if _, err := registry.LoadForPlanning(path); err == nil {
		t.Fatal("missing registry should fail")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing registry was created: %v", err)
	}
}
