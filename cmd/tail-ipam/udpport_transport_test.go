package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUDPPortCrossTransportInvalidUTF8 verifies that invalid UTF-8 in a label
// argument is rejected identically by reserve, release, and list, regardless
// of whether the label is sent as a POST body field (reserve site/device) or
// a query parameter (release/list site). This guards against the class of bug
// where JSON encoding rewrites invalid UTF-8 to U+FFFD before validation.
func TestUDPPortCrossTransportInvalidUTF8(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	invalidSite := "branch" + string([]byte{0xff})
	invalidDevice := "fw" + string([]byte{0xff})

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "reserve with invalid UTF-8 site",
			args: []string{"reserve", "41642", "fw01", "--site", invalidSite},
		},
		{
			name: "release with invalid UTF-8 site",
			args: []string{"release", "41642", "--site", invalidSite},
		},
		{
			name: "list with invalid UTF-8 site",
			args: []string{"list", "--site", invalidSite},
		},
		{
			name: "reserve with invalid UTF-8 device",
			args: []string{"reserve", "41642", invalidDevice, "--site", "branch"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stdout, _, err := runUDPPort(t, dir, test.args...)
			if err == nil {
				t.Fatal("command error = nil, want rejection of invalid UTF-8")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(err.Error(), "UTF-8") {
				t.Errorf("error = %q, want it to mention UTF-8", err)
			}
			if code := exitCodeFor(err); code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "local.ipam.hujson")); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("registry exists after invalid UTF-8 site: %v", statErr)
			}
		})
	}
}

// testReserveReleaseRoundTrip verifies reserve, release, and final list
// operations for a single site/device/port combination.
func testReserveReleaseRoundTrip(t *testing.T, dir, site, device, port string) {
	t.Helper()

	// Output always carries the canonical (trimmed) spelling, never the raw
	// argv, so assertions compare against that.
	wantSite := strings.TrimSpace(site)
	wantDevice := strings.TrimSpace(device)

	// Reserve with exact argv
	stdout, stderr, reserveErr := runUDPPort(
		t, dir, "reserve", port, device, "--site", site,
	)
	if reserveErr != nil {
		t.Fatalf("reserve error: %v", reserveErr)
	}
	if stderr != "" {
		t.Errorf("reserve stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, wantSite) || !strings.Contains(stdout, wantDevice) {
		t.Errorf("reserve stdout = %q, want site %q and device %q", stdout, wantSite, wantDevice)
	}

	// The reservation is visible, with both labels intact, before release.
	stdout, _, listErr := runUDPPort(t, dir, "list", "--site", site)
	if listErr != nil {
		t.Fatalf("list after reserve error: %v", listErr)
	}
	if !strings.Contains(stdout, port) || !strings.Contains(stdout, wantDevice) {
		t.Errorf("list stdout = %q, want port %q and device %q", stdout, port, wantDevice)
	}

	// Release with SAME exact argv (same whitespace, same encoding)
	stdout, stderr, releaseErr := runUDPPort(
		t, dir, "release", port, "--site", site,
	)
	if releaseErr != nil {
		t.Fatalf("release with matching argv error: %v; stdout: %q", releaseErr, stdout)
	}
	if stderr != "" {
		t.Errorf("release stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "released") {
		t.Errorf("release stdout = %q, want 'released'", stdout)
	}

	// Verify it's actually gone (list returns empty or no match)
	stdout, _, listErr = runUDPPort(t, dir, "list", "--site", site)
	if listErr != nil {
		t.Fatalf("final list error: %v", listErr)
	}
	if strings.Contains(stdout, wantDevice) {
		t.Errorf("final list still contains device %q: %q", wantDevice, stdout)
	}
}

// TestUDPPortReserveReleaseConsistency verifies that site/device label
// canonicalization is consistent across transports: reserve sends both in
// a JSON body, while release sends site as a query parameter. Both must
// apply the same canonicalization so release finds the registry entry.
func TestUDPPortReserveReleaseConsistency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		site   string
		device string
		port   string
	}{
		{
			name:   "ascii",
			site:   "hq",
			device: "fw01",
			port:   "41642",
		},
		{
			name:   "multi-byte UTF-8",
			site:   "münchen",
			device: "büro-fw01",
			port:   "41643",
		},
		{
			name:   "whitespace trim",
			site:   " branch ",
			device: " fw02 ",
			port:   "41644",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			testReserveReleaseRoundTrip(t, t.TempDir(), test.site, test.device, test.port)
		})
	}
}
