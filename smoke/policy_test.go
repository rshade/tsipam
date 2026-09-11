//go:build smoke

package smoke_test

import (
	"os"
	"path/filepath"
	"testing"
)

const policyWithDrift = `{
  "hosts": { "hq-gw": "10.20.0.1" },
  "ipsets": {
    "ipset:hq":     ["10.20.0.0/24", "host:hq-gw"],
    "ipset:branch": ["2001:db8:20:1::/64"],
    "ipset:rogue":  ["172.16.0.0/12"],
  },
  "acls": [{ "action": "accept", "src": ["*"], "dst": ["*:*"] }],
}
`

const policyConsistent = `{
  "hosts": { "hq-gw": "10.20.0.1" },
  "ipsets": {
    "ipset:hq":     ["10.20.0.0/24", "host:hq-gw"],
    "ipset:branch": ["2001:db8:20:1::/64"],
  },
}
`

type policyComparison struct {
	DriftCount int `json:"drift_count"`
	Authored   []struct {
		IPSet string `json:"ipset"`
		CIDR  string `json:"cidr"`
		State string `json:"state"`
	} `json:"authored"`
	Reserved []struct {
		CIDR  string `json:"cidr"`
		State string `json:"state"`
	} `json:"reserved"`
}

func writePolicy(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.hujson")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func reservedPools(t *testing.T) registry {
	t.Helper()
	reg := newRegistry(t)
	expect(t, run(t, nil, reg.args("subnet", "reserve", "10.20.0.0/16", "hq-vpc")...), exitOK)
	expect(t, run(t, nil, reg.args("subnet", "reserve", "2001:db8:20::/48", "branch")...), exitOK)
	return reg
}

func TestCheckPolicy(t *testing.T) {
	t.Parallel()
	t.Run("drift exits 1 and classifies both directions", func(t *testing.T) {
		t.Parallel()
		reg := reservedPools(t)
		res := run(t, nil, reg.args("check-policy", writePolicy(t, policyWithDrift), flagFormat, formatJSON)...)
		if res.code != exitRefused {
			t.Fatalf("exit = %d, want 1\n%s", res.code, res.stderr)
		}
		env := decodeEnvelope(t, res.stdout)
		var cmp policyComparison
		env.field(t, "policy_comparison", &cmp)
		if env.Status != "drift" || cmp.DriftCount != 1 {
			t.Errorf("status=%q drift_count=%d, want drift/1", env.Status, cmp.DriftCount)
		}
		states := map[string]string{}
		for _, row := range cmp.Authored {
			states[row.CIDR] = row.State
		}
		if states["172.16.0.0/12"] != "authored_but_unreserved" || states["10.20.0.0/24"] != "matched" ||
			states["10.20.0.1/32"] != "matched" {
			t.Errorf("authored states = %v", states)
		}
		for _, row := range cmp.Reserved {
			if row.State != "matched" {
				t.Errorf("reserved %s state = %q, want matched", row.CIDR, row.State)
			}
		}
	})
	t.Run("consistent policy exits 0", func(t *testing.T) {
		t.Parallel()
		reg := reservedPools(t)
		res := run(t, nil, reg.args("check-policy", writePolicy(t, policyConsistent))...)
		expect(t, res, exitOK)
		containsAll(t, res.stdout, "drift_count=0")
	})
	t.Run("incomplete input exits 2 without a report", func(t *testing.T) {
		t.Parallel()
		reg := reservedPools(t)
		cases := map[string]string{
			"missing file": filepath.Join(t.TempDir(), "absent.hujson"),
			"cycle":        writePolicy(t, `{ "ipsets": { "ipset:a": ["ipset:b"], "ipset:b": ["ipset:a"] } }`),
			"dynamic":      writePolicy(t, `{ "ipsets": { "ipset:x": ["autogroup:internet"] } }`),
		}
		for name, path := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				expect(t, run(t, nil, reg.args("check-policy", path, flagFormat, formatJSON)...), exitInvalid)
			})
		}
	})
}
