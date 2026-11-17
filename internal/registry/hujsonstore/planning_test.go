package hujsonstore_test

import (
	"os"
	"testing"

	"github.com/rshade/tsipam/internal/registry/hujsonstore"
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
			path := hujsonstore.Path(t.TempDir(), "example.ts.net")
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ledgerAt(path).LoadForPlanning(regCtx(), registryTailnet)
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
	path := hujsonstore.Path(t.TempDir(), "example.ts.net")
	if _, err := ledgerAt(path).LoadForPlanning(regCtx(), registryTailnet); err == nil {
		t.Fatal("missing registry should fail")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing registry was created: %v", err)
	}
}
