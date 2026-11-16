package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"
)

const planningRegistry = `{
  // Preserve this comment and the original formatting.
  "siteIDs":{"2":"branch-private","10":"absent-private"},
  "subnetPools":{"10.0.0.0/16":"branch-private","fd7a:115c:a1e0:b1a::/64":"branch-private"},
  "serviceAddresses":{"100.100.100.1":"svc:expected-private"},
  "udpPorts":{"branch-private":{"41642":"router-private"}}
}`

const planningDevices = `{"devices":[{"name":"router-private", "advertisedRoutes":[
  "10.0.1.0/24","fd7a:115c:a1e0:b1a:0:2:a00:0/120","2001:db8::/32"
]}]}`

const planningServices = `{"vipServices":[{"name":"svc:observed-private","addrs":["100.100.100.1"]}]}`

func planningAPIFixtures() map[string]devicesAPIFixture {
	fixtures := make(map[string]devicesAPIFixture)
	cases := []struct {
		name, deviceBody, serviceBody string
		deviceStatus, serviceStatus   int
		oauth                         bool
	}{
		{"plan-drift", planningDevices, planningServices, http.StatusOK, http.StatusOK, false},
		{"plan-oauth", planningDevices, planningServices, http.StatusOK, http.StatusOK, true},
		{"plan-empty", `{"devices":[]}`, `{"vipServices":[]}`, http.StatusOK, http.StatusOK, false},
		{"plan-device-error", `{"message":"secret-device-body"}`, `{}`, http.StatusForbidden, http.StatusOK, false},
		{
			"plan-service-error",
			planningDevices,
			`{"message":"secret-service-body"}`,
			http.StatusOK,
			http.StatusForbidden,
			false,
		},
		{"plan-device-missing", `{}`, `{"vipServices":[]}`, http.StatusOK, http.StatusOK, false},
		{"plan-service-missing", planningDevices, `{}`, http.StatusOK, http.StatusOK, false},
		{"plan-device-null", `{"devices":null}`, `{"vipServices":[]}`, http.StatusOK, http.StatusOK, false},
		{"plan-service-null", planningDevices, `{"vipServices":null}`, http.StatusOK, http.StatusOK, false},
		{"plan-device-malformed", `{"devices":`, `{"vipServices":[]}`, http.StatusOK, http.StatusOK, false},
		{"plan-service-malformed", planningDevices, `{"vipServices":false}`, http.StatusOK, http.StatusOK, false},
		{"plan-route-invalid", `{"devices":[{"name":"private","advertisedRoutes":["private-invalid"]}]}`,
			`{"vipServices":[]}`, http.StatusOK, http.StatusOK, false},
		{
			"plan-address-invalid",
			planningDevices,
			`{"vipServices":[{"name":"svc:private","addrs":["private-invalid"]}]}`,
			http.StatusOK,
			http.StatusOK,
			false,
		},
	}
	for _, tc := range cases {
		device := devicesAPIFixture{apiKey: "planning-key", status: tc.deviceStatus, body: tc.deviceBody}
		service := devicesAPIFixture{apiKey: "planning-key", status: tc.serviceStatus, body: tc.serviceBody}
		if tc.oauth {
			device.apiKey, service.apiKey = "", ""
			device.bearerToken, service.bearerToken = oauthAccessToken, oauthAccessToken
		}
		base := "/api/v2/tailnet/" + tc.name + ".example.com/"
		fixtures[base+"devices"] = device
		fixtures[base+"vip-services"] = service
	}
	return fixtures
}

func testPlanningTSCLIPath(t *testing.T) {
	t.Helper()
	testPlanningReports(t)
	testPlanningEmpty(t)
	testPlanningMatched(t)
	testPlanningCollectionErrors(t)
	testPlanningSanitization(t)
}

func runPlanning(ctx context.Context, env map[string]string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := runCLI(ctx, args, devicesEnv(env), &stdout, &stderr, nil)
	return code, stdout.String(), stderr.String()
}

func testPlanningReports(t *testing.T) {
	t.Helper()
	for _, command := range []string{commandPlan, commandCheck} {
		for _, format := range []string{formatHuman, formatJSON} {
			for _, prefix := range []string{"-", "--"} {
				t.Run(command+"/"+format+"/"+prefix, func(t *testing.T) {
					t.Parallel()
					assertPlanningReport(t, command, format, prefix)
				})
			}
		}
	}
}

func assertPlanningReport(t *testing.T, command, format, prefix string) {
	t.Helper()
	dir := t.TempDir()
	path := writeAggregateRegistry(t, dir, "registry-alias", planningRegistry)
	env := map[string]string{"TAILSCALE_TAILNET": "plan-drift.example.com", "TAILSCALE_API_KEY": "planning-key"}
	code, out, errOut := runPlanning(t.Context(), env, command,
		prefix+"registry-dir", dir, prefix+"tailnet", "registry-alias", prefix+"format", format)
	wantCode := exitOK
	if command == commandCheck {
		wantCode = exitFailure
	}
	if code != wantCode || (command == commandPlan && errOut != "") {
		t.Fatalf("exit = %d, stderr = %q; want %d", code, errOut, wantCode)
	}
	if format == formatJSON {
		assertPlanningJSON(t, command, out)
	} else {
		for _, want := range []string{"matched", "reserved_but_unadvertised", "unreserved_but_live", "conflict", "drift_count=3"} {
			if !strings.Contains(out, want) {
				t.Errorf("human report lacks %q: %s", want, out)
			}
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != planningRegistry {
		t.Fatalf("comparison changed registry: %s, %v", after, err)
	}
}

func assertPlanningJSON(t *testing.T, command, out string) {
	t.Helper()
	var result outputEnvelope
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Command != command || result.Status != statusDrift || result.Tailnet != "registry-alias" ||
		result.Comparison == nil || result.Comparison.LiveTailnet != "plan-drift.example.com" ||
		result.Comparison.DriftCount != 3 || len(result.Comparison.Entries) != 6 {
		t.Fatalf("unexpected comparison: %s", out)
	}
	if !reflect.DeepEqual(result.Comparison.Excluded, []string{"udp_port"}) {
		t.Errorf("exclusions = %v", result.Comparison.Excluded)
	}
}

func testPlanningEmpty(t *testing.T) {
	t.Helper()
	for _, command := range []string{commandPlan, commandCheck} {
		t.Run(command+"/empty", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeAggregateRegistry(t, dir, "plan-empty.example.com", `{}`)
			env := map[string]string{"TAILSCALE_TAILNET": "plan-empty.example.com", "TAILSCALE_API_KEY": "planning-key"}
			code, out, errOut := runPlanning(t.Context(), env, command, "--registry-dir", dir, "--format", "json")
			if code != exitOK || errOut != "" {
				t.Fatalf("empty comparison: exit %d, stderr %s", code, errOut)
			}
			var result outputEnvelope
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != statusOK || result.Comparison == nil || result.Comparison.DriftCount != 0 ||
				result.Comparison.Entries == nil || len(result.Comparison.Entries) != 0 {
				t.Fatalf("empty report = %s", out)
			}
		})
	}
}

func testPlanningMatched(t *testing.T) {
	t.Helper()
	t.Run("check/matched", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeAggregateRegistry(t, dir, "alias", `{
			"siteIDs":{"2":"branch"},
			"subnetPools":{"10.0.0.0/16":"branch","2001:db8::/32":"branch","fd7a:115c:a1e0:b1a::/64":"branch"},
			"serviceAddresses":{"100.100.100.1":"svc:observed-private"}
		}`)
		env := map[string]string{"TAILSCALE_TAILNET": "plan-drift.example.com", "TAILSCALE_API_KEY": "planning-key"}
		code, out, errOut := runPlanning(t.Context(), env, commandCheck,
			"--registry-dir", dir, "--tailnet", "alias", "--format", "json", "--verbose")
		if code != exitOK || !strings.Contains(errOut, "live allocation collection complete") {
			t.Fatalf("matched comparison: exit %d, stderr %s", code, errOut)
		}
		var result outputEnvelope
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != statusOK || result.Comparison == nil || result.Comparison.DriftCount != 0 ||
			len(result.Comparison.Entries) != 5 {
			t.Fatalf("matched report = %s", out)
		}
	})
}

func testPlanningCollectionErrors(t *testing.T) {
	t.Helper()
	cases := map[string]string{
		"device-error": "collecting live devices: HTTP 403", "service-error": "collecting live services: HTTP 403",
		"device-missing": "omitted devices list", "service-missing": "omitted services list",
		"device-null": "omitted devices list", "service-null": "omitted services list",
		"device-malformed": "decoding failed", "service-malformed": "decoding failed",
		"route-invalid": "invalid advertised CIDR", "address-invalid": "address 0 is invalid",
	}
	for name, message := range cases {
		for _, command := range []string{commandPlan, commandCheck} {
			t.Run(command+"/"+name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				writeAggregateRegistry(t, dir, "alias", planningRegistry)
				env := map[string]string{
					"TAILSCALE_TAILNET": "plan-" + name + ".example.com",
					"TAILSCALE_API_KEY": "planning-key",
				}
				code, out, errOut := runPlanning(t.Context(), env, command,
					"--registry-dir", dir, "--tailnet", "alias", "--format", "json", "--sanitize", "identity")
				assertUsageFailure(t, code, out)
				if !strings.Contains(errOut, message) {
					t.Errorf("stderr = %q, want %q", errOut, message)
				}
				for _, secret := range []string{"private", "secret-device-body", "secret-service-body", "planning-key"} {
					if strings.Contains(errOut, secret) {
						t.Errorf("stderr exposed %q: %s", secret, errOut)
					}
				}
			})
		}
	}
}

func testPlanningSanitization(t *testing.T) {
	t.Helper()
	for _, level := range []string{"identity", "strict"} {
		for _, format := range []string{formatHuman, formatJSON} {
			t.Run("planning sanitize/"+level+"/"+format, func(t *testing.T) {
				t.Parallel()
				assertPlanningSanitized(t, level, format)
			})
		}
	}
}

func assertPlanningSanitized(t *testing.T, level, format string) {
	t.Helper()
	dir := t.TempDir()
	writeAggregateRegistry(t, dir, "private-alias", planningRegistry)
	env := map[string]string{
		"TAILSCALE_TAILNET": "plan-oauth.example.com", "TAILSCALE_OAUTH_CLIENT_ID": "planning-client",
		"TAILSCALE_OAUTH_CLIENT_SECRET": "planning-secret", sanitize.KeyEnv: testSanitizeKey,
	}
	args := []string{
		commandPlan,
		"--registry-dir",
		dir,
		"--tailnet",
		"private-alias",
		"--format",
		format,
		"--sanitize",
		level,
	}
	code, out, errOut := runPlanning(t.Context(), env, args...)
	if code != exitOK || !strings.Contains(errOut, "output is sanitized") {
		t.Fatalf("sanitized OAuth comparison: exit %d, stderr %s", code, errOut)
	}
	for _, secret := range []string{"private", "plan-oauth.example.com", "10.0.", "100.100.", "fd7a:", "2001:db8:", "planning-secret"} {
		if strings.Contains(out+errOut, secret) {
			t.Errorf("sanitized output exposed %q: %s", secret, out+errOut)
		}
	}
	_, again, _ := runPlanning(t.Context(), env, args...)
	if out != again {
		t.Errorf("keyed report changed across repeated observations: %s vs %s", out, again)
	}
	if format == formatJSON {
		var result outputEnvelope
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		if result.Comparison == nil || result.Comparison.DriftCount != 3 || result.Comparison.Entries[0].Key != "2" {
			t.Fatalf("sanitization changed verdict or numeric site ID: %s", out)
		}
	}
}
