//go:build smoke

package smoke_test

import (
	"encoding/json"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type comparison struct {
	LiveTailnet string   `json:"live_tailnet"`
	DriftCount  int      `json:"drift_count"`
	Excluded    []string `json:"excluded"`
	Entries     []struct {
		Kind     string   `json:"kind"`
		State    string   `json:"state"`
		Key      string   `json:"key"`
		Observed []string `json:"observed"`
	} `json:"entries"`
}

// liveEnv returns the credentials the live commands need, skipping the test
// unless -live was passed and the environment carries them.
func liveEnv(t *testing.T) map[string]string {
	t.Helper()
	if os.Getenv(envLive) == "" {
		t.Skipf("set %s=1 (under esc run or with TAILSCALE_* exported) to run tailnet-backed cases", envLive)
	}
	env := map[string]string{}
	for _, key := range []string{
		"TAILSCALE_TAILNET", "TAILSCALE_API_KEY",
		"TAILSCALE_OAUTH_CLIENT_ID", "TAILSCALE_OAUTH_CLIENT_SECRET",
	} {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
	if env["TAILSCALE_TAILNET"] == "" || (env["TAILSCALE_API_KEY"] == "" && env["TAILSCALE_OAUTH_CLIENT_ID"] == "") {
		t.Skipf("%s set but TAILSCALE_TAILNET and credentials are not in the environment", envLive)
	}
	return env
}

// liveRegistry seeds an empty registry file: plan and check read rather than create.
func liveRegistry(t *testing.T) registry {
	t.Helper()
	reg := newRegistry(t)
	reg.write(t, "{}\n")
	return reg
}

func TestLiveDevicesList(t *testing.T) {
	t.Parallel()
	env := liveEnv(t)
	human := run(t, env, "devices", "list")
	expect(t, human, exitOK)
	res := run(t, env, "devices", "list", flagFormat, formatJSON)
	expect(t, res, exitOK)
	var devices []map[string]json.RawMessage
	decodeEnvelope(t, res.stdout).field(t, "devices", &devices)
	if len(devices) != len(lines(human.stdout)) {
		t.Errorf("JSON lists %d devices, human output %d rows", len(devices), len(lines(human.stdout)))
	}
	for _, device := range devices {
		for _, key := range []string{
			"name", "addresses", "advertised_routes", "enabled_routes", "tags",
			"user", "os", "hostname", "authorized", "ephemeral",
		} {
			if _, ok := device[key]; !ok {
				t.Errorf("device missing %s", key)
			}
		}
	}
	env["TAILSCALE_SANITIZE"] = "identity"
	res = run(t, env, "devices", "list", flagFormat, formatJSON)
	expect(t, res, exitOK)
	for _, device := range devices {
		for _, key := range []string{"name", "user", "hostname"} {
			if value := jsonString(t, device[key]); value != "" {
				containsNone(t, res.stdout, value)
			}
		}
		for _, tag := range jsonStrings(t, device["tags"]) {
			if tag != "" && !strings.Contains(res.stdout, tag) {
				t.Errorf("identity output dropped tag %q", tag)
			}
		}
	}
}

func jsonString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode string: %v", err)
	}
	return value
}

func jsonStrings(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatalf("decode strings: %v", err)
	}
	return values
}

func TestLivePlanCheckRoundTrip(t *testing.T) {
	t.Parallel()
	env := liveEnv(t)
	reg := liveRegistry(t)

	res := run(t, env, reg.args("plan", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	var cmp comparison
	decodeEnvelope(t, res.stdout).field(t, "comparison", &cmp)
	if cmp.LiveTailnet != env["TAILSCALE_TAILNET"] || strings.Join(cmp.Excluded, ",") != "udp_port" {
		t.Errorf("live_tailnet=%q excluded=%v", cmp.LiveTailnet, cmp.Excluded)
	}
	live := reserveLiveEntries(t, reg, cmp)
	t.Logf("live allocations discovered and reserved: %d", live)
	if live == 0 {
		t.Log("no advertised routes or services on this tailnet; matching rules not exercised")
	}

	res = run(t, env, reg.args("check", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	converged := decodeEnvelope(t, res.stdout)
	converged.field(t, "comparison", &cmp)
	if converged.Status != "ok" || cmp.DriftCount != 0 {
		t.Fatalf("after reserving every live allocation: status=%q drift=%d", converged.Status, cmp.DriftCount)
	}

	assertStaleReservationDrifts(t, env, reg)
}

// reserveLiveEntries reserves every unreserved_but_live row so the registry
// converges on the tailnet, and reports how many rows there were.
func reserveLiveEntries(t *testing.T, reg registry, cmp comparison) int {
	t.Helper()
	live := 0
	for _, entry := range cmp.Entries {
		if entry.State != "unreserved_but_live" {
			t.Errorf("empty registry produced state %q for %s %s", entry.State, entry.Kind, entry.Key)
			continue
		}
		live++
		group, label := "", "smoke"
		switch entry.Kind {
		case "site_id":
			group = "site-id"
		case "subnet_pool":
			group = "subnet"
		case "service_address":
			group = "service"
			if len(entry.Observed) > 0 {
				label = entry.Observed[0]
			}
		default:
			t.Fatalf("unknown allocation kind %q", entry.Kind)
		}
		expect(t, run(t, nil, reg.args(group, "reserve", entry.Key, label)...), exitOK)
	}
	return live
}

// assertStaleReservationDrifts adds one reservation nothing advertises and
// proves check reports exactly that row, unsanitized and sanitized.
func assertStaleReservationDrifts(t *testing.T, env map[string]string, reg registry) {
	t.Helper()
	const stalePool = "192.0.2.0/24"
	expect(t, run(t, nil, reg.args("subnet", "reserve", stalePool, "never-advertised")...), exitOK)
	res := run(t, env, reg.args("check", flagFormat, formatJSON)...)
	if res.code != exitRefused {
		t.Fatalf("stale reservation: exit=%d, want 1\n%s", res.code, res.stderr)
	}
	drifted := decodeEnvelope(t, res.stdout)
	var cmp comparison
	drifted.field(t, "comparison", &cmp)
	stale := 0
	for _, entry := range cmp.Entries {
		if entry.State == "reserved_but_unadvertised" && entry.Key == stalePool {
			stale++
		}
	}
	if drifted.Status != "drift" || cmp.DriftCount != 1 || stale != 1 {
		t.Errorf("stale reservation: status=%q drift=%d stale rows=%d", drifted.Status, cmp.DriftCount, stale)
	}

	env["TAILSCALE_SANITIZE"] = "identity"
	res = run(t, env, reg.args("check", flagFormat, formatJSON)...)
	if res.code != exitRefused {
		t.Fatalf("sanitized check exit=%d, want 1", res.code)
	}
	containsNone(t, res.stdout, stalePool, env["TAILSCALE_TAILNET"], reg.alias)
}

func TestLiveNextFree(t *testing.T) {
	t.Parallel()
	env := liveEnv(t)
	reg := liveRegistry(t)
	const pool = "10.250.0.0/16"

	first := run(t, env, reg.args("calc", "next-free", pool, "--size", "24")...)
	expect(t, first, exitOK)
	suggestion := strings.TrimSpace(first.stdout)
	if len(lines(first.stdout)) != 1 || !strings.HasSuffix(suggestion, "/24") {
		t.Fatalf("single result = %q, want one bare /24", first.stdout)
	}

	res := run(t, env, reg.args("calc", "next-free", pool, "--size", "24", "--count", "3", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	var blocks []string
	decodeEnvelope(t, res.stdout).field(t, "free_blocks", &blocks)
	if len(blocks) != 3 || blocks[0] != suggestion {
		t.Errorf("free_blocks = %v, want three starting with %s", blocks, suggestion)
	}

	expect(t, run(t, nil, reg.args("subnet", "reserve", suggestion, "composed")...), exitOK)
	res = run(t, env, reg.args("calc", "next-free", pool, "--size", "24")...)
	expect(t, res, exitOK)
	if strings.TrimSpace(res.stdout) == suggestion {
		t.Error("next-free did not advance past a fresh reservation")
	}

	res = run(t, env, reg.args("calc", "next-free", suggestion, "--size", "24", flagFormat, formatJSON)...)
	expect(t, res, exitInvalid)
	containsAll(t, res.stderr, "no free /24 block in "+suggestion)

	res = run(t, env, reg.args("calc", "next-free", "2001:db8::/32", "--size", "48", "--count", "2")...)
	expect(t, res, exitOK)
	if got := lines(res.stdout); len(got) != 2 || !strings.HasSuffix(got[0], "/48") {
		t.Errorf("IPv6 next-free = %v", got)
	}
}

func TestLiveSubnetAllocate(t *testing.T) {
	t.Parallel()
	env := liveEnv(t)
	reg := liveRegistry(t)
	const pool = "10.251.0.0/16"
	allocate := reg.args("subnet", "allocate", pool, "composed", "--size", "24", "--ticket", "SMOKE-1",
		flagFormat, formatJSON)

	first := run(t, env, allocate...)
	expect(t, first, exitOK)
	var cidr string
	var changed bool
	firstEnvelope := decodeEnvelope(t, first.stdout)
	firstEnvelope.field(t, "cidr", &cidr)
	firstEnvelope.field(t, "changed", &changed)
	if !strings.HasPrefix(cidr, "10.251.") || !strings.HasSuffix(cidr, "/24") || !changed {
		t.Fatalf("allocate = cidr %q changed %t, want a fresh /24 inside %s", cidr, changed, pool)
	}
	listed := run(t, nil, reg.args("subnet", "list")...)
	expect(t, listed, exitOK)
	containsAll(t, listed.stdout, cidr+"\tcomposed", "ticket=SMOKE-1")
	before := reg.contents(t)

	replay := run(t, env, allocate...)
	expect(t, replay, exitOK)
	var replayCIDR string
	replayEnvelope := decodeEnvelope(t, replay.stdout)
	replayEnvelope.field(t, "cidr", &replayCIDR)
	replayEnvelope.field(t, "changed", &changed)
	if replayCIDR != cidr || changed {
		t.Errorf("ticket replay = cidr %q changed %t, want %q unchanged", replayCIDR, changed, cidr)
	}
	if after := reg.contents(t); after != before {
		t.Errorf("ticket replay rewrote the registry:\nbefore: %s\nafter:  %s", before, after)
	}

	exhausted := run(t, env, reg.args("subnet", "allocate", cidr, "other", "--size", "24")...)
	expect(t, exhausted, exitInvalid)
	containsAll(t, exhausted.stderr, "no free /24 block in "+cidr)
	if after := reg.contents(t); after != before {
		t.Errorf("exhaustion rewrote the registry:\nbefore: %s\nafter:  %s", before, after)
	}
}

const (
	supernetBits = 16
	narrowerBits = 1
	minRouteBits = 20
	maxRouteBits = 30
)

// liveIPv4Route returns one advertised IPv4 route narrow enough to sit inside
// a /16 and wide enough to carve, or skips when the tailnet has none.
func liveIPv4Route(t *testing.T, env map[string]string, reg registry) netip.Prefix {
	t.Helper()
	res := run(t, env, reg.args("plan", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	var cmp comparison
	decodeEnvelope(t, res.stdout).field(t, "comparison", &cmp)
	for _, entry := range cmp.Entries {
		if entry.Kind != "subnet_pool" || entry.State != "unreserved_but_live" {
			continue
		}
		route, err := netip.ParsePrefix(entry.Key)
		if err != nil || !route.Addr().Is4() || route.Bits() < minRouteBits || route.Bits() > maxRouteBits {
			continue
		}
		return route
	}
	t.Skip("no advertised IPv4 route between /20 and /30 on this tailnet; matching rules need one")
	return netip.Prefix{}
}

func poolStates(t *testing.T, env map[string]string, reg registry) map[string]string {
	t.Helper()
	res := run(t, env, reg.args("plan", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	var cmp comparison
	decodeEnvelope(t, res.stdout).field(t, "comparison", &cmp)
	states := map[string]string{}
	for _, entry := range cmp.Entries {
		if entry.Kind == "subnet_pool" {
			states[entry.Key] = entry.State
		}
	}
	return states
}

// TestLiveMatchingRules proves the documented asymmetry against a real route:
// next-free never suggests it, a pool containing it matches, and a
// reservation narrower than it does not.
func TestLiveMatchingRules(t *testing.T) {
	t.Parallel()
	env := liveEnv(t)
	reg := liveRegistry(t)
	route := liveIPv4Route(t, env, reg)
	supernet := netip.PrefixFrom(route.Addr(), supernetBits).Masked()
	narrower := netip.PrefixFrom(route.Addr(), route.Bits()+narrowerBits)
	size := strconv.Itoa(route.Bits())

	res := run(t, env, reg.args("calc", "next-free", supernet.String(), "--size", size, "--all")...)
	expect(t, res, exitOK)
	if slices.Contains(lines(res.stdout), route.String()) {
		t.Errorf("next-free offered the live route %s", route)
	}

	expect(t, run(t, nil, reg.args("subnet", "reserve", supernet.String(), "covering")...), exitOK)
	if states := poolStates(t, env, reg); states[supernet.String()] != "matched" || states[route.String()] != "" {
		t.Errorf("after reserving %s: states = %v, want it matched and %s absorbed", supernet, states, route)
	}

	expect(t, run(t, nil, reg.args("subnet", "release", supernet.String())...), exitOK)
	expect(t, run(t, nil, reg.args("subnet", "reserve", narrower.String(), "half")...), exitOK)
	states := poolStates(t, env, reg)
	if states[route.String()] != "unreserved_but_live" || states[narrower.String()] != "reserved_but_unadvertised" {
		t.Errorf("after reserving %s inside live %s: states = %v", narrower, route, states)
	}
}

// TestLiveNextFreeEdges pins next-free's validation and exhaustion behavior
// against the live route set. Exhaustion exits 2 with nothing on stdout so
// the README's `subnet reserve "$(tail-ipam calc next-free ...)"` composition
// fails at the right command (#347, #356).
func TestLiveNextFreeEdges(t *testing.T) {
	t.Parallel()
	env := liveEnv(t)
	reg := liveRegistry(t)
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"size shorter than pool", []string{"10.10.0.0/16", "--size", "15"}, exitInvalid, "must be between pool /16"},
		{"size past address length", []string{"10.10.0.0/16", "--size", "33"}, exitInvalid, "address length /32"},
		{"count zero", []string{"10.10.0.0/16", "--size", "24", "--count", "0"}, exitInvalid, "count must be positive"},
		{
			"all and count conflict",
			[]string{"10.10.0.0/16", "--size", "24", "--count", "2", "--all"},
			exitInvalid,
			"cannot be used together",
		},
		{"host bits masked", []string{"192.0.2.77/24", "--size", "26"}, exitOK, "192.0.2.0/26"},
		{
			"/127 pool yields both /128s",
			[]string{"2001:db8::/127", "--size", "128", "--all"},
			exitOK,
			"2001:db8::/128\n2001:db8::1/128",
		},
		{"pool-sized request of a free pool", []string{"198.51.100.0/24", "--size", "24"}, exitOK, "198.51.100.0/24"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := run(t, env, reg.args(append([]string{"calc", "next-free"}, tc.args...)...)...)
			expect(t, res, tc.code)
			if tc.code == exitOK {
				if got := strings.TrimRight(res.stdout, "\n"); got != tc.want {
					t.Errorf("stdout = %q, want %q", got, tc.want)
				}
				return
			}
			containsAll(t, res.stderr, tc.want)
		})
	}
	t.Run("exhausted pool exits 2 with the message on stderr", func(t *testing.T) {
		t.Parallel()
		expect(t, run(t, nil, reg.args("subnet", "reserve", "203.0.113.0/24", "taken")...), exitOK)
		for _, format := range []string{"human", formatJSON} {
			res := run(t, env, reg.args("calc", "next-free", "203.0.113.0/24", "--size", "25", flagFormat, format)...)
			expect(t, res, exitInvalid)
			containsAll(t, res.stderr, "no free /25 block in 203.0.113.0/24")
		}
	})
}
