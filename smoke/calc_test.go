//go:build smoke

package smoke_test

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	splitV4Children     = 256
	splitULAChildren    = 65536
	split4in6Children   = 4096
	splitOverLimitCount = "4294967296"
	ipv6SpaceHosts      = "340282366920938463463374607431768211456"
)

func TestDiscovery(t *testing.T) {
	t.Parallel()
	t.Run("help names exit codes", func(t *testing.T) {
		t.Parallel()
		res := run(t, nil, "--help")
		expect(t, res, exitOK)
		containsAll(t, strings.ToLower(res.stdout), "exit")
	})
	t.Run("schema is JSON and catalogs every documented command", func(t *testing.T) {
		t.Parallel()
		res := run(t, nil, "-schema")
		expect(t, res, exitOK)
		var schema struct {
			Commands []struct {
				Name string `json:"name"`
			} `json:"commands"`
			ExitCodes []struct {
				Code int `json:"code"`
			} `json:"exit_codes"`
		}
		if err := json.Unmarshal([]byte(res.stdout), &schema); err != nil {
			t.Fatalf("schema is not JSON: %v", err)
		}
		names := map[string]bool{}
		for _, c := range schema.Commands {
			names[c.Name] = true
		}
		for _, want := range []string{
			"calc info", "calc split", "calc aggregate", "calc next-free",
			"site-id reserve", "site-id release", "site-id list",
			"subnet reserve", "subnet release", "subnet list",
			"service reserve", "service release", "service list",
			"port reserve", "port release", "port list",
			"devices list", "plan", "check", "check-policy",
		} {
			if !names[want] {
				t.Errorf("schema lacks command %q", want)
			}
		}
		if len(schema.ExitCodes) != exitInvalid+1 {
			t.Errorf("schema lists %d exit codes, want 3", len(schema.ExitCodes))
		}
	})
	t.Run("unknown flag and subcommand exit 2", func(t *testing.T) {
		t.Parallel()
		expect(t, run(t, nil, "--no-such-flag"), exitInvalid)
		expect(t, run(t, nil, "frobnicate"), exitInvalid)
	})
}

func TestCalcInfo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, cidr string
		want       []string
		absent     []string
	}{
		{"README /24", "192.168.1.0/24", []string{
			"prefix\t192.168.1.0/24", "network\t192.168.1.0", "broadcast\t192.168.1.255",
			"first_usable\t192.168.1.1", "last_usable\t192.168.1.254", "usable_hosts\t254",
		}, nil},
		{"README /31 has no broadcast", "10.0.0.0/31", []string{
			"prefix\t10.0.0.0/31", "first_usable\t10.0.0.0", "last_usable\t10.0.0.1", "usable_hosts\t2",
		}, []string{"broadcast"}},
		{"README IPv6 masks host bits", "2001:db8:1::42/64", []string{
			"prefix\t2001:db8:1::/64", "network\t2001:db8:1::",
			"last_usable\t2001:db8:1:0:ffff:ffff:ffff:ffff", "usable_hosts\t18446744073709551616",
		}, []string{"broadcast"}},
		{"4in6 /120 normalizes to IPv4", "::ffff:10.0.0.0/120", []string{"prefix\t10.0.0.0/24"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := run(t, nil, "calc", "info", tc.cidr)
			expect(t, res, exitOK)
			containsAll(t, res.stdout, tc.want...)
			containsNone(t, res.stdout, tc.absent...)
		})
	}
	t.Run("JSON usable_hosts is an exact decimal string", func(t *testing.T) {
		t.Parallel()
		res := run(t, nil, "calc", "info", "::/0", flagFormat, formatJSON)
		expect(t, res, exitOK)
		var info struct {
			UsableHosts string `json:"usable_hosts"`
		}
		decodeEnvelope(t, res.stdout).field(t, "block_info", &info)
		if info.UsableHosts != ipv6SpaceHosts {
			t.Errorf("usable_hosts = %q, want %q", info.UsableHosts, ipv6SpaceHosts)
		}
	})
	t.Run("malformed CIDR exits 2", func(t *testing.T) {
		t.Parallel()
		expect(t, run(t, nil, "calc", "info", "not-a-cidr"), exitInvalid)
	})
}

func TestCalcSplit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, parent, prefix string
		count                int
		first, last          string
	}{
		{"README /16 into /24", "10.0.0.0/16", "24", splitV4Children, "10.0.0.0/24", "10.0.255.0/24"},
		{"README ULA /48 into /64 at the limit", "fd7a:115c:a1e0::/48", "64", splitULAChildren,
			"fd7a:115c:a1e0::/64", "fd7a:115c:a1e0:ffff::/64"},
		{"4in6 /104 into IPv4 /20", "::ffff:10.0.0.0/104", "20", split4in6Children, "10.0.0.0/20", "10.255.240.0/20"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := run(t, nil, "calc", "split", tc.parent, "--prefix", tc.prefix)
			expect(t, res, exitOK)
			got := lines(res.stdout)
			if len(got) != tc.count || got[0] != tc.first || got[len(got)-1] != tc.last {
				t.Errorf("got %d lines (%s .. %s), want %d (%s .. %s)",
					len(got), got[0], got[len(got)-1], tc.count, tc.first, tc.last)
			}
		})
	}
	t.Run("README over-limit request is refused with exit 1", func(t *testing.T) {
		t.Parallel()
		res := run(t, nil, "calc", "split", "2001:db8::/32", "--prefix", "64")
		expect(t, res, exitRefused)
		containsAll(t, res.stderr, splitOverLimitCount, "too many")
	})
	t.Run("JSON carries subnets", func(t *testing.T) {
		t.Parallel()
		res := run(t, nil, "calc", "split", "10.0.0.0/30", "--prefix", "32", flagFormat, formatJSON)
		expect(t, res, exitOK)
		var subnets []string
		decodeEnvelope(t, res.stdout).field(t, "subnets", &subnets)
		if len(subnets) != 4 || subnets[0] != "10.0.0.0/32" {
			t.Errorf("subnets = %v", subnets)
		}
	})
}

func TestCalcAggregate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"README v4 siblings", []string{"10.0.0.0/25", "10.0.0.128/25"}, "10.0.0.0/24"},
		{"README v6 recursive", []string{"fd12:3456:789a:1::/64", "fd12:3456:789a:2::/63", "fd12:3456:789a::/64"},
			"fd12:3456:789a::/62"},
		{"README mixed families, v4 first",
			[]string{"2001:db8::/49", "10.0.0.128/25", "2001:db8:0:8000::/49", "10.0.0.0/25"},
			"10.0.0.0/24\n2001:db8::/48"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := run(t, nil, append([]string{"calc", "aggregate"}, tc.args...)...)
			expect(t, res, exitOK)
			if got := strings.TrimRight(res.stdout, "\n"); got != tc.want {
				t.Errorf("stdout = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("JSON carries supernets in order", func(t *testing.T) {
		t.Parallel()
		res := run(t, nil, "calc", "aggregate", "10.0.0.0/24", "10.1.0.0/24", flagFormat, formatJSON)
		expect(t, res, exitOK)
		var supernets []string
		decodeEnvelope(t, res.stdout).field(t, "supernets", &supernets)
		if strings.Join(supernets, ",") != "10.0.0.0/24,10.1.0.0/24" {
			t.Errorf("supernets = %v", supernets)
		}
	})
	t.Run("invalid invocations exit 2", func(t *testing.T) {
		t.Parallel()
		reg := newRegistry(t)
		expect(t, run(t, nil, "calc", "aggregate"), exitInvalid)
		expect(t, run(t, nil, "calc", "aggregate", "--from-registry", "10.0.0.0/24"), exitInvalid)
		expect(t, run(t, nil, reg.args("calc", "aggregate", "--from-registry")...), exitInvalid)
		if reg.exists() {
			t.Error("aggregate created a registry")
		}
	})
	t.Run("from-registry aggregates each family", func(t *testing.T) {
		t.Parallel()
		reg := newRegistry(t)
		for _, cidr := range []string{"10.0.0.0/25", "10.0.0.128/25", "2001:db8::/49", "2001:db8:0:8000::/49"} {
			expect(t, run(t, nil, reg.args("subnet", "reserve", cidr, "site")...), exitOK)
		}
		res := run(t, nil, reg.args("calc", "aggregate", "--from-registry")...)
		expect(t, res, exitOK)
		if got := strings.TrimRight(res.stdout, "\n"); got != "10.0.0.0/24\n2001:db8::/48" {
			t.Errorf("stdout = %q", got)
		}
		res = run(t, nil, reg.args("calc", "aggregate", "--from-registry", flagFormat, formatJSON)...)
		expect(t, res, exitOK)
		env := decodeEnvelope(t, res.stdout)
		var supernets []string
		env.field(t, "supernets", &supernets)
		if env.Tailnet != reg.alias || len(supernets) != 2 {
			t.Errorf("tailnet = %q supernets = %v", env.Tailnet, supernets)
		}
	})
}

// TestCalcEdges pins the boundary behavior the README describes in prose:
// whole address spaces, single-address blocks, invalid prefixes, and the
// distinction between a refused split (exit 1) and a malformed one (exit 2).
func TestCalcEdges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		code int
		want string // exact stdout for exit 0; a stderr fragment otherwise
	}{
		{
			"info /0 counts every host but two",
			[]string{"calc", "info", "0.0.0.0/0"},
			exitOK,
			"usable_hosts\t4294967294",
		},
		{"info /32 is one usable host", []string{"calc", "info", "10.0.0.7/32"}, exitOK, "usable_hosts\t1"},
		{"info /128 is one usable host", []string{"calc", "info", "2001:db8::1/128"}, exitOK, "usable_hosts\t1"},
		{"info 4in6 /128 becomes /32", []string{"calc", "info", "::ffff:10.0.0.1/128"}, exitOK, "prefix\t10.0.0.1/32"},
		{
			"info /95 4in6-looking stays IPv6",
			[]string{"calc", "info", "::ffff:10.0.0.0/95"},
			exitOK,
			"prefix\t::fffe:0:0/95",
		},
		{"info prefix out of range", []string{"calc", "info", "10.0.0.0/33"}, exitInvalid, "out of range"},
		{"info bare address", []string{"calc", "info", "10.0.0.0"}, exitInvalid, "no '/'"},
		{
			"split same size is the parent",
			[]string{"calc", "split", "10.0.0.0/24", "--prefix", "24"},
			exitOK,
			"10.0.0.0/24",
		},
		{
			"split shorter than parent",
			[]string{"calc", "split", "10.0.0.0/24", "--prefix", "23"},
			exitInvalid,
			"shorter than parent",
		},
		{
			"split beyond address length",
			[]string{"calc", "split", "10.0.0.0/24", "--prefix", "33"},
			exitInvalid,
			"exceeds address length",
		},
		{"split masks host bits", []string{"calc", "split", "10.0.0.5/24", "--prefix", "26"}, exitOK,
			"10.0.0.0/26\n10.0.0.64/26\n10.0.0.128/26\n10.0.0.192/26"},
		{"split IPv6 to host routes", []string{"calc", "split", "2001:db8::/126", "--prefix", "128"}, exitOK,
			"2001:db8::/128\n2001:db8::1/128\n2001:db8::2/128\n2001:db8::3/128"},
		{
			"split one past the limit is refused",
			[]string{"calc", "split", "2001:db8::/111", "--prefix", "128"},
			exitRefused,
			"131072",
		},
		{"split needs --prefix", []string{"calc", "split", "10.0.0.0/24"}, exitInvalid, "prefix"},
		{
			"aggregate duplicates collapse",
			[]string{"calc", "aggregate", "10.0.0.0/24", "10.0.0.0/24"},
			exitOK,
			"10.0.0.0/24",
		},
		{
			"aggregate covered collapses",
			[]string{"calc", "aggregate", "10.0.0.0/16", "10.0.5.0/24"},
			exitOK,
			"10.0.0.0/16",
		},
		{
			"aggregate odd run keeps remainder",
			[]string{"calc", "aggregate", "10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24"},
			exitOK,
			"10.0.0.0/23\n10.0.2.0/24",
		},
		{
			"aggregate halves make the whole space",
			[]string{"calc", "aggregate", "0.0.0.0/1", "128.0.0.0/1"},
			exitOK,
			"0.0.0.0/0",
		},
		{"aggregate IPv6 halves", []string{"calc", "aggregate", "::/1", "8000::/1"}, exitOK, "::/0"},
		{"aggregate masks host bits", []string{"calc", "aggregate", "10.0.0.1/24"}, exitOK, "10.0.0.0/24"},
		{
			"aggregate 4in6 merges with IPv4",
			[]string{"calc", "aggregate", "::ffff:10.0.0.0/120", "10.0.1.0/24"},
			exitOK,
			"10.0.0.0/23",
		},
		{"aggregate malformed", []string{"calc", "aggregate", "not-a-cidr"}, exitInvalid, "invalid CIDR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := run(t, nil, tc.args...)
			expect(t, res, tc.code)
			if tc.code == exitOK {
				got := strings.TrimRight(res.stdout, "\n")
				if strings.Contains(tc.want, "\t") {
					containsAll(t, got, tc.want)
				} else if got != tc.want {
					t.Errorf("stdout = %q, want %q", got, tc.want)
				}
				return
			}
			containsAll(t, res.stderr, tc.want)
		})
	}
}
