package policycheck_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rshade/tsipam/internal/policycheck"
	"github.com/rshade/tsipam/internal/registry"
)

//nolint:gochecknoglobals // Golden update flags must be registered at package scope.
var updateGolden = flag.Bool("update", false, "update policy comparison goldens")

func TestCompareFixtures(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"matched", "authored-unreserved", "reserved-unreferenced", "operations"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkCompareFixture(t, name)
		})
	}
}

func checkCompareFixture(t *testing.T, name string) {
	t.Helper()
	policy := readFixture(t, name+".policy.hujson")
	pools, err := registry.ListSubnetPoolsWithLogger(filepath.Join("testdata", name+".registry.hujson"), nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := policycheck.Compare(policy, pools)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(pools)
	reordered, err := policycheck.Compare(policy, pools)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	again, err := json.MarshalIndent(reordered, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("pool order changed report")
	}
	got = append(got, '\n')
	golden := name + ".report.golden.json"
	if *updateGolden {
		if err = os.WriteFile(filepath.Join("testdata", golden), got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if want := readFixture(t, golden); !bytes.Equal(got, want) {
		t.Fatalf("report differs:\ngot: %s\nwant: %s", got, want)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSinglePoolContainment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, member string
		pools        []string
		drift        int
		authored     int
	}{
		{"child", "192.0.2.128/25", []string{"192.0.2.0/24"}, 0, 1},
		{"broader", "192.0.2.0/24", []string{"192.0.2.0/25", "192.0.2.128/25"}, 1, 1},
		{"masked", "192.0.2.42/24", []string{"192.0.2.0/24"}, 0, 1},
		{"host", "192.0.2.42", []string{"192.0.2.0/24"}, 0, 1},
		{"range", "192.0.2.0-192.0.2.255", []string{"192.0.2.0/24"}, 0, 1},
		{"IPv6", "2001:db8:1::/48", []string{"2001:db8::/32"}, 0, 1},
		{"IPv6 entire space", "::/0", []string{"::/0"}, 0, 1},
		{"different family", "2001:db8::/32", []string{"192.0.2.0/24"}, 2, 1},
		{"mapped authored", "::ffff:192.0.2.0/120", []string{"192.0.2.0/24"}, 0, 1},
		{"mapped pool", "192.0.2.0/24", []string{"::ffff:192.0.2.0/120"}, 0, 1},
		{"native IPv6 containment", "::ffff:192.0.2.0/120", []string{"::/0"}, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			policy := fmt.Appendf(nil, "{\"ipsets\":{\"ipset:test\":[%q]}}", tc.member)
			pools := make([]registry.SubnetPoolEntry, len(tc.pools))
			for i, pool := range tc.pools {
				pools[i] = registry.SubnetPoolEntry{CIDR: pool, Site: "site"}
			}
			report, err := policycheck.Compare(policy, pools)
			if err != nil {
				t.Fatal(err)
			}
			if report.DriftCount != tc.drift || len(report.Authored) != tc.authored {
				t.Fatalf("unexpected report: %+v", report)
			}
			for _, reserved := range report.Reserved {
				if tc.name != "different family" && reserved.State != policycheck.Matched {
					t.Fatalf("overlapped reservation is unreferenced: %+v", reserved)
				}
			}
		})
	}
}

func TestResolveEffectiveMembers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, policy string
		cidrs        []string
	}{
		{"adjacent kept separate", `{"ipsets":{"ipset:a":["192.0.2.0/25","192.0.2.128/25","192.0.2.0/25"]}}`,
			[]string{"192.0.2.0/25", "192.0.2.128/25"}},
		{"remove then add", `{"ipsets":{"ipset:a":["add 192.0.2.0/24","remove 192.0.2.0/24","add 192.0.2.7"]}}`,
			[]string{"192.0.2.7/32"}},
		{"subtract half", `{"ipsets":{"ipset:a":["add 192.0.2.0/24","remove 192.0.2.128/25"]}}`,
			[]string{"192.0.2.0/25"}},
		{"mapped removal", `{"ipsets":{"ipset:a":["add ::ffff:192.0.2.0/120","remove 192.0.2.0/24"]}}`, []string{}},
		{"plain removal", `{"ipsets":{"ipset:a":["add 192.0.2.0/24","remove ::ffff:192.0.2.0/120"]}}`, []string{}},
		{"legacy keys", `{"Hosts":{"office":"192.0.2.5"},"IPSets":{"ipset:a":["host:office"]},"grants":[]}`,
			[]string{"192.0.2.5/32"}},
		{"IPv6 range", `{"ipsets":{"ipset:a":["2001:db8::2-2001:db8::3"]}}`, []string{"2001:db8::2/127"}},
		{"absent", `{"grants":[]}`, []string{}},
		{"empty", `{"ipsets":{"ipset:a":[]}}`, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report, err := policycheck.Compare([]byte(tc.policy), nil)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(report.Authored))
			for index, entry := range report.Authored {
				got[index] = entry.CIDR
			}
			if !slices.Equal(got, tc.cidrs) {
				t.Fatalf("CIDRs = %v, want %v", got, tc.cidrs)
			}
		})
	}
}

func TestRejectIncompletePolicy(t *testing.T) {
	t.Parallel()
	cases := []string{
		"{\"ipsets\":{\"ipset:\xff\":[]}}",
		`{"secret`, `[]`, `null`,
		`{"ipsets":null}`, `{"hosts":[]}`,
		`{"ipsets":{},"IPSets":{}}`,
		`{"ipsets":{"ipset:secret":[],"ipset:secret":[]}}`,
		`{"hosts":{"secret":"192.0.2.1","secret":"192.0.2.2"}}`,
		`{"ipsets":{"secret":[]}}`, `{"ipsets":{"ipset:":[]}}`,
		`{"ipsets":{"ipset:secret":null}}`, `{"ipsets":{"ipset:secret":[null]}}`,
		`{"hosts":{"secret":null}}`,
		`{"ipsets":{"ipset:secret":["ipset:missing"]}}`,
		`{"ipsets":{"ipset:secret":["host:missing"]}}`,
		`{"ipsets":{"ipset:secret":["ipset:secret"]}}`,
		`{"ipsets":{"ipset:secret":["ipset:b"],"ipset:b":["ipset:secret"]}}`,
		`{"ipsets":{"ipset:secret":["autogroup:internet"]}}`,
		`{"ipsets":{"ipset:secret":["tag:secret"]}}`,
		`{"ipsets":{"ipset:secret":["192.0.2.0/99"]}}`,
		`{"ipsets":{"ipset:secret":["fe80::1%secret"]}}`,
		`{"ipsets":{"ipset:secret":["192.0.2.0/24","remove 192.0.2.0/25"]}}`,
		`{"ipsets":{"ipset:secret":["replace 192.0.2.0/24"]}}`,
		`{"ipsets":{"ipset:secret":[""]}}`,
		`{"hosts":{"secret":"host:other"},"ipsets":{"ipset:a":["host:secret"]}}`,
		`{"ipsets":{"ipset:secret":["192.0.2.2-192.0.2.1"]}}`,
	}
	for index, policy := range cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()
			report, err := policycheck.Compare([]byte(policy), nil)
			if err == nil {
				t.Fatalf("accepted incomplete policy: %s", policy)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked policy content: %v", err)
			}
			if len(report.Authored) != 0 || len(report.Reserved) != 0 {
				t.Fatalf("partial report: %+v", report)
			}
		})
	}
}

func TestRejectExpansionDepth(t *testing.T) {
	t.Parallel()
	sets := make(map[string][]string)
	for index := range 129 {
		sets[fmt.Sprintf("ipset:%03d", index)] = []string{fmt.Sprintf("ipset:%03d", index+1)}
	}
	sets["ipset:129"] = []string{"192.0.2.1"}
	policy, err := json.Marshal(map[string]any{"ipsets": sets})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = policycheck.Compare(policy, nil); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("depth result = %v", err)
	}
	// Resolve leaves first so parent expansions reuse cached results.
	var reversed strings.Builder
	reversed.WriteString(`{"ipsets":{`)
	for index := 129; index >= 0; index-- {
		if index != 129 {
			reversed.WriteByte(',')
		}
		name := fmt.Sprintf("ipset:%03d", index)
		members, marshalErr := json.Marshal(sets[name])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		fmt.Fprintf(&reversed, "%q:%s", name, members)
	}
	reversed.WriteString("}}")
	if _, err = policycheck.Compare(
		[]byte(reversed.String()),
		nil,
	); err == nil ||
		!strings.Contains(err.Error(), "nesting") {
		t.Fatalf("cached depth result = %v", err)
	}
}

func TestRejectExpansionCount(t *testing.T) {
	t.Parallel()
	members := make([]string, 65537)
	for index := range members {
		members[index] = fmt.Sprintf("2001:db8::%x:%x", index>>16, index&0xffff)
	}
	policy, err := json.Marshal(map[string]any{"ipsets": map[string][]string{"ipset:large": members}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = policycheck.Compare(policy, nil); err == nil || !strings.Contains(err.Error(), "65536") {
		t.Fatalf("prefix limit result = %v", err)
	}
}
