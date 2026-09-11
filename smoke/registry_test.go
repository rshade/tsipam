//go:build smoke

package smoke_test

import (
	"strings"
	"testing"
)

// allocationKind describes one registry allocation family through the
// commands that drive it, so every family walks the same lifecycle.
type allocationKind struct {
	name, group, listKey, keyField string
	reserve                        []string // key, target, extra flags
	sameKeyOtherTarget             []string // must collide
	invalid                        []string // must be rejected as input
	release                        []string // key plus any scoping flags
}

func allocationKinds() []allocationKind {
	return []allocationKind{
		{
			name: "site-id", group: "site-id", listKey: "site_ids", keyField: "id",
			reserve:            []string{"1", "hq-vpc"},
			sameKeyOtherTarget: []string{"1", "branch"},
			invalid:            []string{"70000", "x"},
			release:            []string{"1"},
		},
		{
			name: "subnet", group: "subnet", listKey: "subnet_pools", keyField: "cidr",
			reserve:            []string{"10.20.0.0/16", "hq-vpc"},
			sameKeyOtherTarget: []string{"10.20.5.0/24", "branch"}, // overlap counts as the same space
			invalid:            []string{"10.30.0.1/16", "x"},      // host bits set
			release:            []string{"10.20.0.0/16"},
		},
		{
			name: "service", group: "service", listKey: "service_addresses", keyField: "address",
			reserve:            []string{"100.100.100.8", "dashboard"},
			sameKeyOtherTarget: []string{"100.100.100.8/32", "other"},
			invalid:            []string{"100.100.100.0/24", "x"},
			release:            []string{"100.100.100.8"},
		},
		{
			name: "port", group: "port", listKey: "udp_ports", keyField: "port",
			reserve:            []string{"41642", "fw01", "--site", "branch"},
			sameKeyOtherTarget: []string{"41642", "fw02", "--site", "branch"},
			invalid:            []string{"0", "fw01", "--site", "branch"},
			release:            []string{"41642", "--site", "branch"},
		},
	}
}

func TestRegistryLifecycle(t *testing.T) {
	t.Parallel()
	for _, kind := range allocationKinds() {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			reg := newRegistry(t)
			reserve := func(extra ...string) result {
				return run(t, nil, reg.args(append([]string{kind.group, "reserve"}, extra...)...)...)
			}

			res := reserve(append(kind.reserve, flagFormat, formatJSON)...)
			expect(t, res, exitOK)
			env := decodeEnvelope(t, res.stdout)
			var changed bool
			env.field(t, "changed", &changed)
			if !changed || !reg.exists() {
				t.Fatalf("first reserve: changed=%v exists=%v", changed, reg.exists())
			}

			res = reserve(append(kind.reserve, flagFormat, formatJSON)...)
			expect(t, res, exitOK)
			env = decodeEnvelope(t, res.stdout)
			env.field(t, "changed", &changed)
			if changed || env.Status != "no_changes" {
				t.Errorf("re-reserve: changed=%v status=%q, want idempotent no-op", changed, env.Status)
			}

			expect(t, reserve(kind.sameKeyOtherTarget...), exitRefused)
			expect(t, reserve(kind.invalid...), exitInvalid)
			assertListsOneEntry(t, reg, kind)
			assertReleaseRoundTrip(t, reg, kind)
		})
	}
}

// assertListsOneEntry checks both list renderings after a single reserve.
func assertListsOneEntry(t *testing.T, reg registry, kind allocationKind) {
	t.Helper()
	res := run(t, nil, reg.args(kind.group, "list", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	var entries []map[string]any
	decodeEnvelope(t, res.stdout).field(t, kind.listKey, &entries)
	if len(entries) != 1 || entries[0][kind.keyField] == nil {
		t.Fatalf("list = %v, want one entry keyed by %q", entries, kind.keyField)
	}
	res = run(t, nil, reg.args(kind.group, "list")...)
	expect(t, res, exitOK)
	if !strings.Contains(res.stdout, "\t") {
		t.Errorf("human list is not tab-separated: %q", res.stdout)
	}
}

// assertReleaseRoundTrip releases the reservation, proves a second release is
// a no-op, and that an empty list is still a JSON array.
func assertReleaseRoundTrip(t *testing.T, reg registry, kind allocationKind) {
	t.Helper()
	release := append([]string{kind.group, "release"}, kind.release...)
	expect(t, run(t, nil, reg.args(release...)...), exitOK)
	res := run(t, nil, reg.args(append(release, flagFormat, formatJSON)...)...)
	expect(t, res, exitOK)
	var changed bool
	decodeEnvelope(t, res.stdout).field(t, "changed", &changed)
	if changed {
		t.Error("releasing an unreserved key reported a change")
	}

	res = run(t, nil, reg.args(kind.group, "list", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	var raw []any
	decodeEnvelope(t, res.stdout).field(t, kind.listKey, &raw)
	if raw == nil || len(raw) != 0 {
		t.Errorf("empty list must be a JSON array, got %v", raw)
	}
}

func TestRegistryMetadata(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t)
	res := run(t, nil, reg.args("site-id", "reserve", "2", "branch",
		"--owner", "platform-team", "--ticket", "OPS-1234", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	var got struct {
		Owner      string `json:"owner"`
		Ticket     string `json:"ticket"`
		ReservedAt string `json:"reserved_at"`
	}
	env := decodeEnvelope(t, res.stdout)
	env.field(t, "owner", &got.Owner)
	env.field(t, "ticket", &got.Ticket)
	env.field(t, "reserved_at", &got.ReservedAt)
	if got.Owner != "platform-team" || got.Ticket != "OPS-1234" || got.ReservedAt == "" {
		t.Errorf("reserve echoed %+v, want owner, ticket and an auto-stamped reserved_at", got)
	}

	res = run(t, nil, reg.args("site-id", "reserve", "2", "branch", "--ticket", "OPS-5678", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	env = decodeEnvelope(t, res.stdout)
	var owner, ticket string
	env.field(t, "owner", &owner)
	env.field(t, "ticket", &ticket)
	if owner != "platform-team" || ticket != "OPS-5678" {
		t.Errorf("amend: owner=%q ticket=%q, want owner kept and ticket replaced", owner, ticket)
	}
}

func TestRegistryPortsScopeBySite(t *testing.T) {
	t.Parallel()
	reg := newRegistry(t)
	expect(t, run(t, nil, reg.args("port", "reserve", "41642", "branch-fw01", "--site", "branch")...), exitOK)
	expect(t, run(t, nil, reg.args("port", "reserve", "41642", "hq-fw01", "--site", "hq")...), exitOK)
	expect(t, run(t, nil, reg.args("port", "reserve", "41643", "dev")...), exitInvalid)
	res := run(t, nil, reg.args("port", "list", "--site", "branch", flagFormat, formatJSON)...)
	expect(t, res, exitOK)
	var ports []map[string]any
	decodeEnvelope(t, res.stdout).field(t, "udp_ports", &ports)
	if len(ports) != 1 {
		t.Errorf("port list --site branch = %v, want one entry", ports)
	}
}

func TestRegistryFile(t *testing.T) {
	t.Parallel()
	t.Run("comments survive reserve and release", func(t *testing.T) {
		t.Parallel()
		reg := newRegistry(t)
		expect(t, run(t, nil, reg.args("subnet", "reserve", "10.1.0.0/24", "a")...), exitOK)
		reg.write(t, strings.Replace(reg.contents(t), "{", "{\n\t// operator note: keep me\n", 1))
		expect(t, run(t, nil, reg.args("subnet", "reserve", "10.2.0.0/24", "b")...), exitOK)
		expect(t, run(t, nil, reg.args("subnet", "release", "10.2.0.0/24")...), exitOK)
		containsAll(t, reg.contents(t), "operator note: keep me", "10.1.0.0/24")
	})
	t.Run("corrupt registry exits 2 with the decode reason", func(t *testing.T) {
		t.Parallel()
		reg := newRegistry(t)
		reg.write(t, "{\n{{{")
		res := run(t, nil, reg.args("subnet", "list")...)
		expect(t, res, exitInvalid)
		containsAll(t, res.stderr, "decoding registry")
		containsNone(t, res.stderr, "invalid response code", `"name":"fault"`)
	})
	t.Run("missing registry is reported, not created", func(t *testing.T) {
		t.Parallel()
		reg := newRegistry(t)
		for _, command := range [][]string{
			{"subnet", "list"}, {"site-id", "release", "1"}, {"calc", "aggregate", "--from-registry"},
		} {
			res := run(t, nil, reg.args(command...)...)
			expect(t, res, exitInvalid)
			containsNone(t, res.stderr, "invalid response code", `"name":"fault"`)
		}
		if reg.exists() {
			t.Error("a read-only command created the registry")
		}
	})
}
