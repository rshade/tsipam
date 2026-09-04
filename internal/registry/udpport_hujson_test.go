package registry_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

//nolint:gocognit // the table verifies each nested HuJSON presence state in one shared operation matrix
func TestReserveUDPPortPreservesNestedHuJSONAcrossStateMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		site     string
		port     string
		device   string
		wantText []string
	}{
		{
			name: "top level absent",
			content: `{
				// existing allocation
				"siteIDs": {"1": "hq"},
			}` + "\n",
			site: "branch", port: "41642", device: "branch-fw01",
			wantText: []string{"// existing allocation", `"siteIDs"`, `"udpPorts"`, `"branch"`, `"41642"`},
		},
		{
			name: "top level null",
			content: `{
				// reserved for UDP allocation
				"udpPorts": null,
				"future": {"keep": true},
			}` + "\n",
			site: "branch", port: "41642", device: "branch-fw01",
			wantText: []string{"// reserved for UDP allocation", `"future"`, `"branch"`, `"41642"`},
		},
		{
			name: "top level empty object",
			content: `{
				"udpPorts": {
					// ports are assigned explicitly
				},
			}` + "\n",
			site: "branch", port: "41642", device: "branch-fw01",
			wantText: []string{"// ports are assigned explicitly", `"branch"`, `"41642"`},
		},
		{
			name: "site absent from populated object",
			content: `{
				"udpPorts": {
					// do not disturb hq
					"hq": {"41642": "hq-fw01"},
				},
			}` + "\n",
			site: "branch", port: "41642", device: "branch-fw01",
			wantText: []string{"// do not disturb hq", `"hq"`, `"hq-fw01"`, `"branch"`, `"branch-fw01"`},
		},
		{
			name: "site null with noncanonical raw spelling",
			content: `{
				"udpPorts": {
					// branch firewall boundary
					" branch ": null,
					"hq": {"41642": "hq-fw01"},
				},
			}` + "\n",
			site: "branch", port: "41642", device: "branch-fw01",
			wantText: []string{"// branch firewall boundary", `" branch "`, `"hq"`, `"hq-fw01"`, `"branch-fw01"`},
		},
		{
			name: "site empty object",
			content: `{
				"udpPorts": {
					"branch": {
						// first device goes here
					},
				},
			}` + "\n",
			site: "branch", port: "41642", device: "branch-fw01",
			wantText: []string{"// first device goes here", `"branch"`, `"41642"`, `"branch-fw01"`},
		},
		{
			name: "site populated",
			content: `{
				"udpPorts": {
					"branch": {
						// existing device must survive
						"41641": "branch-fw00",
					},
				},
				"unknownFutureMap": {"opaque": "value"},
			}` + "\n",
			site: "branch", port: "41642", device: "branch-fw01",
			wantText: []string{
				"// existing device must survive", `"41641"`, `"branch-fw00"`,
				`"41642"`, `"branch-fw01"`, `"unknownFutureMap"`, `"opaque"`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatalf("writing fixture: %v", err)
			}
			_, _, _, changed, err := registry.ReserveUDPPortWithLogger(
				path, test.site, test.port, test.device, nil,
			)
			if err != nil {
				t.Fatalf("ReserveUDPPort: %v", err)
			}
			if !changed {
				t.Fatal("changed = false, want true")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading registry: %v", err)
			}
			for _, want := range test.wantText {
				if !strings.Contains(string(got), want) {
					t.Errorf("registry after reserve = %s, want it to contain %q", got, want)
				}
			}
			entries, err := registry.ListUDPPortsWithLogger(path, test.site, nil)
			if err != nil {
				t.Fatalf("ListUDPPorts after reserve: %v", err)
			}
			if len(entries) == 0 {
				t.Fatal("ListUDPPorts returned no reservation after reserve")
			}
		})
	}
}

func TestUDPPortOperationsTargetNoncanonicalRawKeys(t *testing.T) {
	t.Parallel()

	path := regPath(t)
	content := `{
		"udpPorts": {
			// preserve the raw site key
			" branch ": {
				// preserve the sibling
				"41643": "branch-fw02",
				"041642": "branch-fw01",
			},
		},
	}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	_, _, _, changed, err := registry.ReserveUDPPortWithLogger(path, "branch", "41642", "branch-fw01", nil)
	if err != nil {
		t.Fatalf("idempotent ReserveUDPPort: %v", err)
	}
	if changed {
		t.Error("idempotent raw-key reserve changed = true, want false")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading after reserve: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("raw-key idempotent reserve rewrote file:\nbefore: %s\nafter: %s", before, after)
	}

	_, _, _, _, err = registry.ReserveUDPPortWithLogger(path, "branch", "41642", "other", nil)
	if _, ok := errors.AsType[*registry.UDPPortCollisionError](err); !ok {
		t.Fatalf("collision against raw key error = %v, want *UDPPortCollisionError", err)
	}

	_, _, changed, err = registry.ReleaseUDPPortWithLogger(path, "branch", "41642", nil)
	if err != nil {
		t.Fatalf("ReleaseUDPPort: %v", err)
	}
	if !changed {
		t.Error("ReleaseUDPPort changed = false, want true")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading after release: %v", err)
	}
	for _, want := range []string{`" branch "`, `"41643"`, `"branch-fw02"`, "// preserve the sibling"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("registry after raw-key release = %s, want %q preserved", got, want)
		}
	}
	if strings.Contains(string(got), `"041642"`) {
		t.Errorf("registry after raw-key release = %s, want raw port removed", got)
	}
}

func TestReleaseLastUDPPortLeavesCommentedSiteObject(t *testing.T) {
	t.Parallel()

	path := regPath(t)
	content := `{
		"udpPorts": {
			// preserve the firewall boundary
			"branch": {
				"41642": "branch-fw01",
			},
			"hq": {"41642": "hq-fw01"},
		},
	}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	_, _, changed, err := registry.ReleaseUDPPortWithLogger(path, "branch", "41642", nil)
	if err != nil {
		t.Fatalf("ReleaseUDPPort: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	for _, want := range []string{"// preserve the firewall boundary", `"branch": {}`, `"hq"`, `"hq-fw01"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("registry after final release = %s, want %q", got, want)
		}
	}
}
