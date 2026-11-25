//go:build smoke

package smoke_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"testing"
)

func identityEnv() map[string]string {
	return map[string]string{"TAILSCALE_SANITIZE": "identity"}
}

func TestSanitize(t *testing.T) {
	t.Parallel()
	t.Run("registry output redacts identities and announces the mode", func(t *testing.T) {
		t.Parallel()
		reg := newRegistry(t)
		reg.alias = "corp-secret"
		expect(t, run(t, nil, reg.args("subnet", "reserve", "10.20.0.0/16", "hq-vpc")...), exitOK)
		res := run(t, identityEnv(), reg.args("subnet", "list", flagFormat, formatJSON)...)
		expect(t, res, exitOK)
		containsNone(t, res.stdout, "10.20.0.0", "hq-vpc", "corp-secret")
		containsAll(t, res.stderr, "TAILSCALE_SANITIZE=identity is active")
		env := decodeEnvelope(t, res.stdout)
		if env.Tailnet == reg.alias {
			t.Error("tailnet alias was not redacted")
		}
	})
	t.Run("stderr diagnostics are redacted too", func(t *testing.T) {
		t.Parallel()
		reg := newRegistry(t)
		reg.alias = "corp-secret"
		expect(t, run(t, nil, reg.args("subnet", "reserve", "10.20.0.0/16", "hq-vpc")...), exitOK)
		res := run(t, identityEnv(), reg.args("subnet", "reserve", "10.20.5.0/24", "branch")...)
		expect(t, res, exitRefused)
		containsNone(t, res.stderr, "10.20.0.0", "hq-vpc", "branch", "corp-secret")
		containsAll(t, res.stderr, "overlaps", "reserved for")
	})
	t.Run("stateless calc output is never redacted", func(t *testing.T) {
		t.Parallel()
		res := run(t, identityEnv(), "calc", "info", "192.168.1.0/24")
		expect(t, res, exitOK)
		containsAll(t, res.stdout, "192.168.1.0/24")
		res = run(t, identityEnv(), "calc", "aggregate", "10.0.0.0/25", "10.0.0.128/25")
		expect(t, res, exitOK)
		containsAll(t, res.stdout, "10.0.0.0/24")
	})
	t.Run("stable key gives stable placeholders", func(t *testing.T) {
		t.Parallel()
		reg := newRegistry(t)
		expect(t, run(t, nil, reg.args("site-id", "reserve", "1", "hq-vpc")...), exitOK)
		env := identityEnv()
		env["TAILSCALE_SANITIZE_KEY"] = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		first := run(t, env, reg.args("site-id", "list", flagFormat, formatJSON)...)
		second := run(t, env, reg.args("site-id", "list", flagFormat, formatJSON)...)
		expect(t, first, exitOK)
		expect(t, second, exitOK)
		same := func(a, b json.RawMessage) bool { return bytes.Equal(a, b) }
		if !maps.EqualFunc(decodeEnvelope(t, first.stdout).raw, decodeEnvelope(t, second.stdout).raw, same) {
			t.Errorf("placeholders changed between runs with a fixed key:\n%s\n%s", first.stdout, second.stdout)
		}
	})
}
