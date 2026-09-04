package registry //nolint:testpackage // scoped UDP-port Load state is intentionally unexported

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//nolint:gocognit // the table checks every loader state and its related raw/canonical bookkeeping together
func TestLoadUDPPortsAbsentNullEmptyAndPopulated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		content           string
		wantTopPresent    bool
		wantSitePresent   bool
		wantDevice        string
		wantRawSite       string
		wantRawPort       string
		wantCanonicalSite string
		wantCanonicalPort string
	}{
		{name: "absent", content: `{}` + "\n"},
		{name: "null", content: `{"udpPorts":null}` + "\n"},
		{name: "empty object", content: `{"udpPorts":{}}` + "\n", wantTopPresent: true},
		{
			name:           "null site",
			content:        `{"udpPorts":{" branch-office ":null}}` + "\n",
			wantTopPresent: true, wantRawSite: " branch-office ", wantCanonicalSite: "branch-office",
		},
		{
			name:           "populated canonical view retains raw keys",
			content:        `{"udpPorts":{" branch-office ":{"041642":" branch-fw01 "}}}` + "\n",
			wantTopPresent: true, wantSitePresent: true, wantDevice: "branch-fw01",
			wantRawSite: " branch-office ", wantRawPort: "041642",
			wantCanonicalSite: "branch-office", wantCanonicalPort: "41642",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := serviceRegistryPath(t)
			writeRegistryFixture(t, path, test.content)
			reg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if mapErr := reg.mapError(udpPortsKey); mapErr != nil {
				t.Fatalf("udpPorts map error = %v, want nil", mapErr)
			}
			if reg.udpPortsPresent != test.wantTopPresent {
				t.Errorf("udpPortsPresent = %t, want %t", reg.udpPortsPresent, test.wantTopPresent)
			}
			if test.wantCanonicalSite == "" {
				if len(reg.UDPPorts) != 0 {
					t.Errorf("UDPPorts = %+v, want empty", reg.UDPPorts)
				}
				return
			}
			if reg.udpPortSiteRawKeys[test.wantCanonicalSite] != test.wantRawSite {
				t.Errorf("raw site = %q, want %q", reg.udpPortSiteRawKeys[test.wantCanonicalSite], test.wantRawSite)
			}
			if reg.udpPortSitePresent[test.wantCanonicalSite] != test.wantSitePresent {
				t.Errorf(
					"site present = %t, want %t",
					reg.udpPortSitePresent[test.wantCanonicalSite],
					test.wantSitePresent,
				)
			}
			if test.wantCanonicalPort == "" {
				if len(reg.UDPPorts[test.wantCanonicalSite]) != 0 {
					t.Errorf(
						"UDPPorts[%q] = %+v, want empty",
						test.wantCanonicalSite,
						reg.UDPPorts[test.wantCanonicalSite],
					)
				}
				return
			}
			if got := reg.UDPPorts[test.wantCanonicalSite][test.wantCanonicalPort]; got != test.wantDevice {
				t.Errorf("UDPPorts[%q][%q] = %q, want %q",
					test.wantCanonicalSite, test.wantCanonicalPort, got, test.wantDevice)
			}
			if got := reg.udpPortRawKeys[test.wantCanonicalSite][test.wantCanonicalPort]; got != test.wantRawPort {
				t.Errorf("raw port = %q, want %q", got, test.wantRawPort)
			}
		})
	}
}

func TestRawUDPPortsBestEffortExtractsMalformedIdentityInventory(t *testing.T) {
	t.Parallel()

	path := serviceRegistryPath(t)
	writeRegistryFixture(t, path, `{
		"udpports": {
			" branch ": {
				"041642": " branch-fw01 ",
				"bad-port": {"unexpected": true},
			},
			"hq": null,
		}
	}`+"\n")

	entries := RawUDPPorts(path)
	if len(entries) != 3 {
		t.Fatalf("RawUDPPorts = %+v, want three best-effort entries", entries)
	}
	if entries[0] != (UDPPortEntry{Site: " branch ", Port: "041642", Device: " branch-fw01 "}) {
		t.Errorf("RawUDPPorts[0] = %+v, want raw branch reservation", entries[0])
	}
	if entries[1] != (UDPPortEntry{Site: " branch ", Port: "bad-port"}) {
		t.Errorf("RawUDPPorts[1] = %+v, want malformed branch leaf inventory", entries[1])
	}
	if entries[2] != (UDPPortEntry{Site: "hq"}) {
		t.Errorf("RawUDPPorts[2] = %+v, want null-site identity inventory", entries[2])
	}

	if got := RawUDPPorts(filepath.Join(t.TempDir(), "missing.hujson")); got != nil {
		t.Errorf("RawUDPPorts(missing) = %+v, want nil", got)
	}
	broken := serviceRegistryPath(t)
	if err := os.WriteFile(broken, []byte(`{"udpPorts":`), 0o600); err != nil {
		t.Fatalf("writing broken fixture: %v", err)
	}
	if got := RawUDPPorts(broken); got != nil {
		t.Errorf("RawUDPPorts(broken) = %+v, want nil", got)
	}
}

func TestLoadRejectsInvalidUDPPortRegistryShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantIn  string
	}{
		{name: "near miss top level", content: `{"udpports":{}}`, wantIn: `must be spelled "udpPorts"`},
		{
			name: "duplicate top level", content: `{"udpPorts":{},"udpPorts":{}}`,
			wantIn: `multiple top-level keys match "udpPorts"`,
		},
		{
			name: "exact duplicate site", content: `{"udpPorts":{"branch":{},"branch":{}}}`,
			wantIn: "duplicate udpPorts site key",
		},
		{
			name: "canonical duplicate site", content: `{"udpPorts":{"branch":{}," branch ":{}}}`,
			wantIn: "site is defined twice",
		},
		{name: "empty canonical site", content: `{"udpPorts":{"   ":{}}}`, wantIn: "site name must not be empty"},
		{
			name: "string site value", content: `{"udpPorts":{"branch":"device"}}`,
			wantIn: "must be an object or null",
		},
		{name: "array site value", content: `{"udpPorts":{"branch":[]}}`, wantIn: "must be an object or null"},
		{name: "boolean site value", content: `{"udpPorts":{"branch":true}}`, wantIn: "must be an object or null"},
		{
			name: "exact duplicate port", content: `{"udpPorts":{"branch":{"41642":"a","41642":"b"}}}`,
			wantIn: "duplicate UDP-port entry key",
		},
		{
			name: "canonical duplicate port", content: `{"udpPorts":{"branch":{"41642":"a","041642":"b"}}}`,
			wantIn: "is defined twice",
		},
		{name: "privileged port", content: `{"udpPorts":{"branch":{"1023":"a"}}}`, wantIn: "1024-65535"},
		{name: "non-numeric port", content: `{"udpPorts":{"branch":{"udp":"a"}}}`, wantIn: "decimal integer"},
		{
			name: "empty device", content: `{"udpPorts":{"branch":{"41642":""}}}`,
			wantIn: "device name must not be empty",
		},
		{
			name: "null device", content: `{"udpPorts":{"branch":{"41642":null}}}`,
			wantIn: "device name must not be empty",
		},
		{name: "object device", content: `{"udpPorts":{"branch":{"41642":{}}}}`, wantIn: "must be a string"},
		{
			name: "control character device", content: `{"udpPorts":{"branch":{"41642":"bad\u0007device"}}}`,
			wantIn: "control characters",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := serviceRegistryPath(t)
			writeRegistryFixture(t, path, test.content+"\n")
			reg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			mapErr := reg.mapError(udpPortsKey)
			if mapErr == nil {
				t.Fatal("udpPorts map error = nil, want malformed map rejection")
			}
			if !strings.Contains(mapErr.Error(), test.wantIn) {
				t.Errorf("udpPorts map error = %q, want it to contain %q", mapErr, test.wantIn)
			}
		})
	}
}

func TestUDPPortValidationErrorsAreScopedByMap(t *testing.T) {
	t.Parallel()

	t.Run("malformed existing map does not block UDP ports", func(t *testing.T) {
		t.Parallel()
		path := serviceRegistryPath(t)
		writeRegistryFixture(t, path, `{
			"subnetPools":{"10.0.0.1/24":"hq"},
			"udpPorts":{"branch":{"41642":"branch-fw01"}}
		}`+"\n")
		reg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if mapErr := reg.mapError(udpPortsKey); mapErr != nil {
			t.Fatalf("udpPorts map error = %v, want nil", mapErr)
		}
		if mapErr := reg.mapError(subnetPoolsKey); mapErr == nil {
			t.Fatal("subnetPools map error = nil, want malformed map rejection")
		}
	})

	t.Run("malformed UDP ports do not block existing maps", func(t *testing.T) {
		t.Parallel()
		path := serviceRegistryPath(t)
		writeRegistryFixture(t, path, `{
			"siteIDs":{"1":"hq"},
			"subnetPools":{"10.0.0.0/24":"hq"},
			"serviceAddresses":{"100.100.100.7":"svc:payments"},
			"udpPorts":{"branch":{"1023":"branch-fw01"}}
		}`+"\n")
		reg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if mapErr := reg.mapError(udpPortsKey); mapErr == nil {
			t.Fatal("udpPorts map error = nil, want malformed map rejection")
		}
		for _, key := range []string{siteIDsKey, subnetPoolsKey, serviceAddressesKey} {
			if mapErr := reg.mapError(key); mapErr != nil {
				t.Errorf("%s map error = %v, want nil", key, mapErr)
			}
		}
	})
}
