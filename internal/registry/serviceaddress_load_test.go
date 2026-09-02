package registry //nolint:testpackage // scoped Load validation state is intentionally unexported

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadServiceAddressesAbsentEmptyAndValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    map[string]ServiceEntry
	}{
		{name: "absent", content: `{}` + "\n"},
		{name: "empty", content: `{"serviceAddresses": {}}` + "\n"},
		{
			name: "bare and slash 32 keys with string and object values",
			content: `{
				"serviceAddresses": {
					"100.100.100.7": "payments",
					"100.100.100.8/32": {"service":"svc:search","owner":"platform"}
				}
			}` + "\n",
			want: map[string]ServiceEntry{
				"100.100.100.7": {Service: "svc:payments"},
				"100.100.100.8": {
					Service:             "svc:search",
					ReservationMetadata: ReservationMetadata{Owner: "platform"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := serviceRegistryPath(t)
			writeRegistryFixture(t, path, tc.content)
			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(got.ServiceAddresses) != len(tc.want) {
				t.Fatalf("ServiceAddresses = %+v, want %+v", got.ServiceAddresses, tc.want)
			}
			for address, wantEntry := range tc.want {
				if got.ServiceAddresses[address] != wantEntry {
					t.Errorf("ServiceAddresses[%q] = %+v, want %+v", address, got.ServiceAddresses[address], wantEntry)
				}
			}
		})
	}
}

func TestLoadRejectsInvalidServiceAddressRegistryShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		wantIn  string
	}{
		{
			name:    "canonical alias duplicate",
			content: `{"serviceAddresses":{"100.100.100.7":"svc:a","100.100.100.7/32":"svc:b"}}`,
			wantIn:  "defined twice",
		},
		{
			name:    "exact duplicate entry",
			content: `{"serviceAddresses":{"100.100.100.7":"svc:a","100.100.100.7":"svc:b"}}`,
			wantIn:  "duplicate serviceAddresses entry key",
		},
		{
			name:    "near miss top level key",
			content: `{"serviceaddresses":{"100.100.100.7":"svc:a"}}`,
			wantIn:  `must be spelled "serviceAddresses"`,
		},
		{
			name:    "duplicate top level key",
			content: `{"serviceAddresses":{},"serviceAddresses":{"100.100.100.7":"svc:a"}}`,
			wantIn:  `multiple top-level keys match "serviceAddresses"`,
		},
		{
			name:    "wider prefix key",
			content: `{"serviceAddresses":{"100.100.100.7/24":"svc:a"}}`,
			wantIn:  "IPv4 host or exact /32",
		},
		{
			name:    "IPv6 key",
			content: `{"serviceAddresses":{"2001:db8::7":"svc:a"}}`,
			wantIn:  "IPv4 host or exact /32",
		},
		{
			name:    "empty string service",
			content: `{"serviceAddresses":{"100.100.100.7":""}}`,
			wantIn:  "service name must not be empty",
		},
		{
			name:    "object missing service",
			content: `{"serviceAddresses":{"100.100.100.7":{"owner":"platform"}}}`,
			wantIn:  "service name must not be empty",
		},
		{
			name:    "unsupported value shape",
			content: `{"serviceAddresses":{"100.100.100.7":[]}}`,
			wantIn:  "service registry entry must be a string or an object",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := serviceRegistryPath(t)
			writeRegistryFixture(t, path, tc.content+"\n")
			reg, loadErr := Load(path)
			if loadErr != nil {
				t.Fatalf("Load: %v", loadErr)
			}
			err := reg.mapError(serviceAddressesKey)
			if err == nil {
				t.Fatal("ListServiceAddresses error = nil, want malformed map rejection")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("ListServiceAddresses error = %q, want it to contain %q", err, tc.wantIn)
			}
		})
	}
}

func TestServiceAddressValidationErrorsAreScopedByMap(t *testing.T) {
	t.Parallel()

	t.Run("malformed subnet map does not block service list", func(t *testing.T) {
		t.Parallel()
		path := serviceRegistryPath(t)
		writeRegistryFixture(t, path, `{
			"subnetPools":{"10.0.0.1/24":"hq"},
			"serviceAddresses":{"100.100.100.7":"svc:payments"}
		}`+"\n")
		reg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if mapErr := reg.mapError(serviceAddressesKey); mapErr != nil {
			t.Fatalf("serviceAddresses map error = %v, want nil", mapErr)
		}
		if mapErr := reg.mapError(subnetPoolsKey); mapErr == nil {
			t.Fatal("subnetPools map error = nil, want malformed subnet rejection")
		}
		if len(reg.ServiceAddresses) != 1 || reg.ServiceAddresses["100.100.100.7"].Service != "svc:payments" {
			t.Errorf("ServiceAddresses = %+v, want payments entry", reg.ServiceAddresses)
		}
	})

	t.Run("malformed service map does not block site or subnet lists", func(t *testing.T) {
		t.Parallel()
		path := serviceRegistryPath(t)
		writeRegistryFixture(t, path, `{
			"siteIDs":{"1":"hq"},
			"subnetPools":{"10.0.0.0/24":"hq"},
			"serviceAddresses":{"100.100.100.7/24":"svc:payments"}
		}`+"\n")
		reg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if mapErr := reg.mapError(serviceAddressesKey); mapErr == nil {
			t.Fatal("serviceAddresses map error = nil, want malformed address rejection")
		}
		if mapErr := reg.mapError(siteIDsKey); mapErr != nil {
			t.Errorf("siteIDs map error = %v, want nil", mapErr)
		}
		if mapErr := reg.mapError(subnetPoolsKey); mapErr != nil {
			t.Errorf("subnetPools map error = %v, want nil", mapErr)
		}
		if len(reg.SiteIDs) != 1 || len(reg.SubnetPools) != 1 {
			t.Errorf("site/subnet entries = %+v/%+v, want one valid entry each", reg.SiteIDs, reg.SubnetPools)
		}
	})
}

func TestReleaseServiceAddressAgainstMalformedMapFailsWhenNoView(t *testing.T) {
	t.Parallel()

	path := serviceRegistryPath(t)
	writeRegistryFixture(t, path, `{
		"serviceAddresses": {
			"999.1.1.1": "svc:a",
			"10.0.0.5": "svc:b"
		}
	}`+"\n")

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if mapErr := reg.mapError(serviceAddressesKey); mapErr == nil {
		t.Fatal("mapError = nil, want malformed address rejection")
	}

	address, changed, releaseErr := ReleaseServiceAddressWithLogger(path, "10.0.0.5", nil)
	if releaseErr == nil {
		t.Fatal("ReleaseServiceAddress error = nil, want failure when map has no view")
	}
	if changed {
		t.Error("changed = true despite error, want false")
	}
	if address != "" {
		t.Errorf("address = %q, want empty on error", address)
	}
	if !strings.Contains(releaseErr.Error(), "cannot determine") {
		t.Errorf("error = %q, want 'cannot determine' message", releaseErr)
	}
}

func TestServiceAddressLoadValidationErrorsHaveProperContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "bad address key",
			content: `{"serviceAddresses":{"999.1.1.1":"svc:a"}}`,
			wantErr: "invalid service address key",
		},
		{
			name:    "non-/32 address key",
			content: `{"serviceAddresses":{"10.0.0.5/24":"svc:a"}}`,
			wantErr: "invalid service address key",
		},
		{
			name:    "empty service value",
			content: `{"serviceAddresses":{"10.0.0.5":""}}`,
			wantErr: "service address 10.0.0.5 (key \"10.0.0.5\")",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := serviceRegistryPath(t)
			writeRegistryFixture(t, path, tc.content+"\n")
			reg, loadErr := Load(path)
			if loadErr != nil {
				t.Fatalf("Load: %v", loadErr)
			}
			err := reg.mapError(serviceAddressesKey)
			if err == nil {
				t.Fatal("mapError = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want to contain %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), "decoding registry") {
				t.Errorf("error = %q, want 'decoding registry' context frame", err)
			}
			var invalidErr *InvalidInputError
			if errors.As(err, &invalidErr) {
				t.Error("error chain contains *InvalidInputError, but stored-state should strip the marker")
			}
		})
	}
}

func serviceRegistryPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tail-ipam.hujson")
}

func writeRegistryFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing registry fixture: %v", err)
	}
}
