package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/rshade/tailscale-utils/policycore/sanitize"
)

// unroutableBaseURL is an address nothing in this suite ever serves. TestMain
// pins tscli's base-url viper key to it for the whole package run so that if
// TestDevicesListTSCLIPath's own override (see below) is ever bypassed or
// raced -- for example by a future test in this package that also reaches
// internal/devices.newClient -- the resulting request fails fast against a
// refused connection instead of silently reaching the real control plane
// with fixture credentials.
const unroutableBaseURL = "http://127.0.0.1:1"

// TestMain sets the base-url floor once, single-threaded, before any test
// (parallel or not) starts, and never resets it: every viper write to
// "base-url" in this package therefore happens strictly before the parallel
// phase begins, which is what keeps it race-free without needing to touch
// internal/devices.clientMu (unexported, and scoped to the credential keys
// only -- see that type's doc comment).
func TestMain(m *testing.M) {
	viper.Set("base-url", unroutableBaseURL)
	os.Exit(m.Run())
}

type devicesAPIFixture struct {
	// Exactly one of apiKey or bearerToken is set: BasicAuth fixtures
	// authenticate a plain API key; bearerToken fixtures authenticate the
	// access token issued by the oauthTokenPath handler below.
	apiKey      string
	bearerToken string
	status      int
	body        string
}

// oauthTokenPath is tscli's OAuth client-credentials token endpoint
// (pkg/tscli/client.go: tokenURL falls back to base-url+this path).
// oauthAccessToken is the fixed token the handler issues and then requires
// on the matching device-list fixture below.
const (
	oauthTokenPath   = "/api/v2/oauth/token"
	oauthAccessToken = "test-oauth-access-token"
)

func newDevicesAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	fixtures := map[string]devicesAPIFixture{
		"/api/v2/tailnet/success.example.com/devices": {
			apiKey: "success-key",
			status: http.StatusOK,
			body: `{"devices":[` +
				`{"name":"router-1","addresses":["100.64.0.1","fd7a:115c:a1e0::1"],` +
				`"AdvertisedRoutes":["10.0.0.0/24","10.1.0.0/24"]},` +
				`{"name":"laptop-1","addresses":["100.64.0.2"]}` +
				`]}`,
		},
		"/api/v2/tailnet/error.example.com/devices": {
			apiKey: "error-key",
			status: http.StatusServiceUnavailable,
			body:   `{"message":"control plane unavailable"}`,
		},
		"/api/v2/tailnet/parallel-a.example.com/devices": {
			apiKey: "parallel-key-a",
			status: http.StatusOK,
			body:   `{"devices":[{"name":"client-a"}]}`,
		},
		"/api/v2/tailnet/parallel-b.example.com/devices": {
			apiKey: "parallel-key-b",
			status: http.StatusOK,
			body:   `{"devices":[{"name":"client-b"}]}`,
		},
		"/api/v2/tailnet/oauth.example.com/devices": {
			bearerToken: oauthAccessToken,
			status:      http.StatusOK,
			body:        `{"devices":[{"name":"oauth-device"}]}`,
		},
		// The message deliberately echoes the tailnet name back, as a real
		// control-plane error naming an unrecognized or unauthorized tailnet
		// plausibly would -- this is what testDevicesAPIErrorIsSanitized
		// exercises the free-text sanitizer sweep against.
		"/api/v2/tailnet/sanitize-error.example.com/devices": {
			apiKey: "sanitize-error-key",
			status: http.StatusServiceUnavailable,
			body:   `{"message":"tailnet sanitize-error.example.com is not authorized"}`,
		},
	}

	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == oauthTokenPath {
			serveOAuthToken(t, writer, request)
			return
		}
		fixture, ok := fixtures[request.URL.Path]
		if !ok {
			http.Error(writer, "unexpected request path", http.StatusNotFound)
			t.Errorf("request path = %q, want one of the configured device-list paths", request.URL.Path)
			return
		}
		if request.Method != http.MethodGet {
			http.Error(writer, "unexpected request method", http.StatusMethodNotAllowed)
			t.Errorf("request method = %q, want %q", request.Method, http.MethodGet)
			return
		}
		if got := request.URL.Query().Get("fields"); got != "all" {
			http.Error(writer, "missing all-fields selection", http.StatusBadRequest)
			t.Errorf("fields query = %q, want %q", got, "all")
			return
		}
		if !authenticateDeviceRequest(t, request, fixture) {
			http.Error(writer, "unexpected credentials", http.StatusUnauthorized)
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(fixture.status)
		if _, err := io.WriteString(writer, fixture.body); err != nil {
			t.Errorf("writing test API response: %v", err)
		}
	}))
}

// authenticateDeviceRequest checks request against fixture's configured
// credential, logging and returning false on mismatch so the caller can
// answer with 401 without duplicating that response across both auth styles.
func authenticateDeviceRequest(t *testing.T, request *http.Request, fixture devicesAPIFixture) bool {
	t.Helper()
	if fixture.bearerToken != "" {
		if got := request.Header.Get("Authorization"); got != "Bearer "+fixture.bearerToken {
			t.Errorf("Authorization = %q, want %q", got, "Bearer "+fixture.bearerToken)
			return false
		}
		return true
	}
	apiKey, password, authenticated := request.BasicAuth()
	if !authenticated || apiKey != fixture.apiKey || password != "" {
		t.Errorf("BasicAuth = (%q, %q, %t), want (%q, %q, true)",
			apiKey, password, authenticated, fixture.apiKey, "")
		return false
	}
	return true
}

// serveOAuthToken answers tscli's client-credentials exchange (via
// golang.org/x/oauth2/clientcredentials) with the fixed oauthAccessToken,
// after confirming the request is the standard RFC 6749 shape that library
// sends: HTTP Basic client auth and a client_credentials grant body.
func serveOAuthToken(t *testing.T, writer http.ResponseWriter, request *http.Request) {
	t.Helper()
	if request.Method != http.MethodPost {
		http.Error(writer, "unexpected oauth token method", http.StatusMethodNotAllowed)
		t.Errorf("oauth token request method = %q, want %q", request.Method, http.MethodPost)
		return
	}
	if _, _, authenticated := request.BasicAuth(); !authenticated {
		http.Error(writer, "missing oauth client credentials", http.StatusUnauthorized)
		t.Error("oauth token request had no HTTP Basic client credentials")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"access_token": oauthAccessToken,
		"token_type":   "Bearer",
		"expires_in":   3600,
	}); err != nil {
		t.Errorf("writing oauth token response: %v", err)
	}
}

func devicesEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func executeDevicesList(getenv func(string) string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := runCLI(
		context.Background(), []string{commandDevices, commandList}, getenv,
		&stdout, &stderr, nil,
	)
	return code, stdout.String(), stderr.String()
}

// assertUsageFailure checks the exit-code and empty-stdout half of a usage
// failure shared by every subtest asserting a rejected devices list
// invocation; callers still check their own stderr message.
func assertUsageFailure(t *testing.T, code int, stdout string) {
	t.Helper()
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", stdout)
	}
}

// assertDevicesSuccess checks the exit-code, stdout, and empty-stderr shared
// by every subtest asserting a successful devices list invocation.
func assertDevicesSuccess(t *testing.T, code int, stdout, stderr, want string) {
	t.Helper()
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr)
	}
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty on success", stderr)
	}
}

func TestDevicesListTSCLIPath(t *testing.T) {
	server := newDevicesAPIServer(t)
	t.Cleanup(server.Close)

	// tscli.New reads base-url from viper's default process-global instance.
	// Install this immutable URL before joining the package's parallel phase;
	// production code serializes every later credential mutation itself. Never
	// restored: TestMain's floor (see above) is the only other writer, and it
	// only runs before m.Run(), so leaving this set is harmless and avoids a
	// second write racing this test's own parallel subtests.
	viper.Set("base-url", server.URL)
	t.Parallel()
	testDevicesCredentialValidation(t)
	testDevicesSuccessfulRoundTrip(t)
	testDevicesAPIError(t)
	testDevicesParallelClientConstruction(t)
	testDevicesOAuthSuccess(t)
	testDevicesAPIErrorIsSanitized(t)
}

func testDevicesCredentialValidation(t *testing.T) {
	t.Helper()
	credentialCases := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "missing tailnet",
			env:     map[string]string{"TAILSCALE_API_KEY": "unused-key"},
			wantErr: "TAILSCALE_TAILNET is required",
		},
		{
			name:    "missing credentials",
			env:     map[string]string{"TAILSCALE_TAILNET": "unused.example.com"},
			wantErr: "TAILSCALE_API_KEY, or both TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET, are required",
		},
		{
			name: "incomplete OAuth credentials",
			env: map[string]string{
				"TAILSCALE_TAILNET":         "unused.example.com",
				"TAILSCALE_OAUTH_CLIENT_ID": "client-id",
			},
			wantErr: "TAILSCALE_API_KEY, or both TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET, are required",
		},
	}
	for _, testCase := range credentialCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := executeDevicesList(devicesEnv(testCase.env))
			assertUsageFailure(t, code, stdout)
			wantStderr := fmt.Sprintf("%s: %s\n", toolName, testCase.wantErr)
			if stderr != wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, wantStderr)
			}
		})
	}
}

func testDevicesSuccessfulRoundTrip(t *testing.T) {
	t.Helper()
	t.Run("successful round trip renders multiple devices", func(t *testing.T) {
		t.Parallel()
		code, stdout, stderr := executeDevicesList(devicesEnv(map[string]string{
			"TAILSCALE_TAILNET": "success.example.com",
			"TAILSCALE_API_KEY": "success-key",
		}))
		want := "router-1\t[100.64.0.1 fd7a:115c:a1e0::1]\troutes=[10.0.0.0/24 10.1.0.0/24]\n" +
			"laptop-1\t[100.64.0.2]\troutes=[]\n"
		assertDevicesSuccess(t, code, stdout, stderr, want)
	})
}

func testDevicesAPIError(t *testing.T) {
	t.Helper()
	t.Run("API error is wrapped", func(t *testing.T) {
		t.Parallel()
		code, stdout, stderr := executeDevicesList(devicesEnv(map[string]string{
			"TAILSCALE_TAILNET": "error.example.com",
			"TAILSCALE_API_KEY": "error-key",
		}))
		assertUsageFailure(t, code, stdout)
		if !strings.Contains(stderr, "listing devices: control plane unavailable (503)") {
			t.Errorf("stderr = %q, want the API error wrapped with device-list context", stderr)
		}
	})
}

func testDevicesParallelClientConstruction(t *testing.T) {
	t.Helper()
	// These subtests deliberately construct clients at the same time. The
	// server cross-checks each tailnet path against its API key, so either a
	// viper data race or a leaked value fails under go test -race. TestMain's
	// floor value and the single write from TestDevicesListTSCLIPath keep all
	// viper writes to "base-url" in the serial phase, preventing the race.
	for _, testCase := range []struct {
		name    string
		tailnet string
		apiKey  string
		device  string
	}{
		{name: "parallel client A", tailnet: "parallel-a.example.com", apiKey: "parallel-key-a", device: "client-a"},
		{name: "parallel client B", tailnet: "parallel-b.example.com", apiKey: "parallel-key-b", device: "client-b"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := executeDevicesList(devicesEnv(map[string]string{
				"TAILSCALE_TAILNET": testCase.tailnet,
				"TAILSCALE_API_KEY": testCase.apiKey,
			}))
			want := fmt.Sprintf("%s\t[]\troutes=[]\n", testCase.device)
			assertDevicesSuccess(t, code, stdout, stderr, want)
		})
	}
}

// testDevicesOAuthSuccess exercises the OAuth client-credentials branch of
// internal/devices.newClient end to end: unlike testDevicesCredentialValidation's
// "incomplete OAuth credentials" case (which never leaves newClient's own
// validation), this drives a real token exchange and bearer-authenticated
// device list through tscli's oauthBearerTransport -- production code this
// suite otherwise never reaches, since every other subtest supplies
// TAILSCALE_API_KEY.
func testDevicesOAuthSuccess(t *testing.T) {
	t.Helper()
	t.Run("OAuth credentials complete the round trip", func(t *testing.T) {
		t.Parallel()
		code, stdout, stderr := executeDevicesList(devicesEnv(map[string]string{
			"TAILSCALE_TAILNET":             "oauth.example.com",
			"TAILSCALE_OAUTH_CLIENT_ID":     "oauth-client-id",
			"TAILSCALE_OAUTH_CLIENT_SECRET": "oauth-client-secret",
		}))
		want := "oauth-device\t[]\troutes=[]\n"
		assertDevicesSuccess(t, code, stdout, stderr, want)
	})
}

// testDevicesAPIErrorIsSanitized exercises main.go's outputSanitizer
// pre-registration (see its doc comment): the tailnet is registered for the
// free-text error sweep before the collector runs, specifically so a
// collection failure -- which happens before the success-path
// registerOutputIdentities call ever executes -- still can't leak the
// tailnet name onto stderr. testDevicesAPIError covers this same collector
// failure with sanitization off; this is its -sanitize identity counterpart.
func testDevicesAPIErrorIsSanitized(t *testing.T) {
	t.Helper()
	t.Run("API error is sanitized when sanitize is active", func(t *testing.T) {
		t.Parallel()
		const tailnet = "sanitize-error.example.com"
		code, stdout, stderr := executeDevicesList(devicesEnv(map[string]string{
			"TAILSCALE_TAILNET": tailnet,
			"TAILSCALE_API_KEY": "sanitize-error-key",
			sanitize.LevelEnv:   string(sanitize.Identity),
			sanitize.KeyEnv:     testSanitizeKey,
		}))
		assertUsageFailure(t, code, stdout)
		assertNoLeak(t, stderr, tailnet)
		if !strings.Contains(stderr, "listing devices:") || !strings.Contains(stderr, "tailnet-") {
			t.Errorf("stderr = %q, want the wrapped error with a sanitized tailnet placeholder", stderr)
		}
	})
}
