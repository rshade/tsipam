package hujsonstore_test

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
)

func TestNormalizePort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "minimum", input: "1024", want: "1024"},
		{name: "maximum", input: "65535", want: "65535"},
		{name: "leading zeroes", input: "041642", want: "41642"},
		{name: "surrounding whitespace", input: " 41642 ", want: "41642"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := registry.NormalizePort(test.input)
			if err != nil {
				t.Fatalf("NormalizePort(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Errorf("NormalizePort(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestReserveUDPPortConcurrentDistinctPortsLosesNoReservation(t *testing.T) {
	t.Parallel()

	const workers = 8
	path := regPath(t)
	start := make(chan struct{})
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for i := range workers {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			port := strconv.Itoa(41642 + index)
			device := fmt.Sprintf("branch-fw%02d", index)
			_, _, _, _, err := ledgerAt(path).ReserveUDPPort(regCtx(), registryTailnet, "branch", port, device)
			errs <- err
		}(i)
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent ReserveUDPPort: %v", err)
		}
	}

	entries, err := ledgerAt(path).ListUDPPorts(regCtx(), registryTailnet, "branch")
	if err != nil {
		t.Fatalf("ListUDPPorts: %v", err)
	}
	if len(entries) != workers {
		t.Errorf("ListUDPPorts returned %d entries, want %d", len(entries), workers)
	}
}

func TestReserveUDPPortConcurrentCollisionHasOneWinner(t *testing.T) {
	t.Parallel()

	const workers = 8
	path := regPath(t)
	type result struct {
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, workers)
	var group sync.WaitGroup
	for i := range workers {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			device := fmt.Sprintf("branch-fw%02d", index)
			_, _, _, changed, err := ledgerAt(path).ReserveUDPPort(regCtx(), registryTailnet, "branch", "41642", device)
			results <- result{changed: changed, err: err}
		}(i)
	}
	close(start)
	group.Wait()
	close(results)

	winners := 0
	collisions := 0
	for got := range results {
		if got.err == nil && got.changed {
			winners++
			continue
		}
		var collision *registry.UDPPortCollisionError
		if errors.As(got.err, &collision) && !got.changed {
			collisions++
			continue
		}
		t.Errorf("concurrent result = {changed:%t err:%v}, want winner or collision", got.changed, got.err)
	}
	if winners != 1 || collisions != workers-1 {
		t.Errorf("winners/collisions = %d/%d, want 1/%d", winners, collisions, workers-1)
	}
}

func TestReserveUDPPortCreatesFileAndReturnsCanonicalValues(t *testing.T) {
	t.Parallel()

	path := regPath(t)
	site, port, device, changed, err := ledgerAt(
		path,
	).ReserveUDPPort(regCtx(), registryTailnet, " branch-office ", "041642", " branch-fw01 ")
	if err != nil {
		t.Fatalf("ReserveUDPPort: %v", err)
	}
	if site != "branch-office" || port != "41642" || device != "branch-fw01" || !changed {
		t.Errorf("ReserveUDPPort = (%q, %q, %q, %t), want (branch-office, 41642, branch-fw01, true)",
			site, port, device, changed)
	}

	entries, err := ledgerAt(path).ListUDPPorts(regCtx(), registryTailnet, "")
	if err != nil {
		t.Fatalf("ListUDPPorts: %v", err)
	}
	if len(entries) != 1 || entries[0].Site != site || entries[0].Port != port || entries[0].Device != device {
		t.Errorf("ListUDPPorts = %+v, want one canonical reservation", entries)
	}
}

func TestReserveUDPPortExactRepeatIsByteIdempotent(t *testing.T) {
	t.Parallel()

	path := regPath(t)
	if _, _, _, _, err := ledgerAt(
		path,
	).ReserveUDPPort(regCtx(), registryTailnet, "branch-office", "41642", "branch-fw01"); err != nil {
		t.Fatalf("first ReserveUDPPort: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}

	_, _, _, changed, err := ledgerAt(
		path,
	).ReserveUDPPort(regCtx(), registryTailnet, " branch-office ", "041642", " branch-fw01 ")
	if err != nil {
		t.Fatalf("repeat ReserveUDPPort: %v", err)
	}
	if changed {
		t.Error("changed = true on exact repeat, want false")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry after repeat: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("registry changed on idempotent repeat:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestReserveUDPPortCollisionIsScopedToSite(t *testing.T) {
	t.Parallel()

	path := regPath(t)
	if _, _, _, _, err := ledgerAt(
		path,
	).ReserveUDPPort(regCtx(), registryTailnet, "branch-office", "41642", "branch-fw01"); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}

	_, _, _, changed, err := ledgerAt(
		path,
	).ReserveUDPPort(regCtx(), registryTailnet, "branch-office", "41642", "branch-fw02")
	var collision *registry.UDPPortCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("ReserveUDPPort error = %v, want *UDPPortCollisionError", err)
	}
	if changed {
		t.Error("changed = true on collision, want false")
	}
	if collision.Site != "branch-office" || collision.Port != "41642" ||
		collision.Existing != "branch-fw01" || collision.Wanted != "branch-fw02" {
		t.Errorf("collision = %+v, want canonical site/port and both devices", collision)
	}
	if got, want := err.Error(),
		`UDP port 41642 in site "branch-office" is already reserved for "branch-fw01", not "branch-fw02"`; got != want {
		t.Errorf("collision error = %q, want %q", got, want)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading registry after collision: %v", readErr)
	}
	if string(after) != string(before) {
		t.Errorf("collision changed registry:\nbefore: %s\nafter: %s", before, after)
	}

	_, _, _, changed, err = ledgerAt(path).ReserveUDPPort(regCtx(), registryTailnet, "hq-vpc", "41642", "core-router-2")
	if err != nil {
		t.Fatalf("cross-site ReserveUDPPort: %v", err)
	}
	if !changed {
		t.Error("cross-site reuse changed = false, want true")
	}
}

func TestReserveUDPPortRejectsInvalidInputWithoutCreatingFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		site   string
		port   string
		device string
	}{
		{name: "privileged port", site: "branch", port: "1023", device: "fw"},
		{name: "empty site", site: " ", port: "41642", device: "fw"},
		{name: "empty device", site: "branch", port: "41642", device: " "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			if _, _, _, _, err := ledgerAt(
				path,
			).ReserveUDPPort(regCtx(), registryTailnet, test.site, test.port, test.device); err == nil {
				t.Fatal("ReserveUDPPort error = nil, want invalid input")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("registry exists after invalid input: %v", err)
			}
		})
	}
}

func TestReleaseUDPPortAbsentPairIsByteIdempotent(t *testing.T) {
	t.Parallel()

	path := regPath(t)
	if _, _, _, _, err := ledgerAt(
		path,
	).ReserveUDPPort(regCtx(), registryTailnet, "branch", "41642", "fw"); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	for _, pair := range []struct{ site, port string }{
		{site: "branch", port: "41643"},
		{site: "unknown", port: "41642"},
	} {
		_, _, changed, releaseErr := ledgerAt(path).ReleaseUDPPort(regCtx(), registryTailnet, pair.site, pair.port)
		if releaseErr != nil {
			t.Fatalf("ReleaseUDPPort(%+v): %v", pair, releaseErr)
		}
		if changed {
			t.Errorf("ReleaseUDPPort(%+v) changed = true, want false", pair)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading registry after release: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("absent release changed registry:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestReleaseAndListUDPPortsMissingFileAreErrors(t *testing.T) {
	t.Parallel()

	path := regPath(t)
	_, _, _, err := ledgerAt(path).ReleaseUDPPort(regCtx(), registryTailnet, "branch", "41642")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReleaseUDPPort error = %v, want os.ErrNotExist", err)
	}
	if _, listErr := ledgerAt(path).ListUDPPorts(regCtx(), registryTailnet, ""); !errors.Is(listErr, os.ErrNotExist) {
		t.Errorf("ListUDPPorts error = %v, want os.ErrNotExist", listErr)
	}
}

func TestNormalizePortRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		wantIn string
	}{
		{name: "empty", input: "", wantIn: "must not be empty"},
		{name: "whitespace", input: "  ", wantIn: "must not be empty"},
		{name: "below range", input: "1023", wantIn: "1024-65535"},
		{name: "above range", input: "65536", wantIn: "1024-65535"},
		{name: "negative", input: "-41642", wantIn: "decimal integer"},
		{name: "explicit plus", input: "+41642", wantIn: "decimal integer"},
		{name: "fractional", input: "41642.0", wantIn: "decimal integer"},
		{name: "letters", input: "udp", wantIn: "decimal integer"},
		{name: "unicode digits", input: "４１６４２", wantIn: "decimal integer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := registry.NormalizePort(test.input)
			if err == nil {
				t.Fatalf("NormalizePort(%q) error = nil, want an error", test.input)
			}
			if !strings.Contains(err.Error(), test.wantIn) {
				t.Errorf("NormalizePort(%q) error = %q, want it to contain %q", test.input, err, test.wantIn)
			}
		})
	}
}

func TestNormalizeDeviceName(t *testing.T) {
	t.Parallel()

	got, err := registry.NormalizeDeviceName("  branch-fw01  ")
	if err != nil {
		t.Fatalf("NormalizeDeviceName: %v", err)
	}
	if got != "branch-fw01" {
		t.Errorf("NormalizeDeviceName = %q, want %q", got, "branch-fw01")
	}
}

func TestNormalizeDeviceNameRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "   ", "branch\nfw", "branch\tfw", "branch\x7ffw"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if _, err := registry.NormalizeDeviceName(input); err == nil {
				t.Errorf("NormalizeDeviceName(%q) error = nil, want an error", input)
			}
		})
	}
}

func TestNormalizeSiteNameRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "invalid UTF-8 byte", input: "branch\xff"},
		{name: "lone surrogate", input: "branch\xed\xa0\x80"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := registry.NormalizeSiteName(test.input)
			if err == nil {
				t.Errorf("NormalizeSiteName(%q) error = nil, want an error containing UTF-8", test.input)
			}
			if !strings.Contains(err.Error(), "UTF-8") {
				t.Errorf("NormalizeSiteName(%q) error = %q, want it to contain UTF-8", test.input, err)
			}
		})
	}
}

func TestNormalizeSiteNameAcceptsValidMultiByteUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "umlaut u", input: "büro"},
		{name: "umlaut o", input: "münchen"},
		{name: "mixed ASCII and UTF-8", input: "büro-hq"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := registry.NormalizeSiteName(test.input)
			if err != nil {
				t.Errorf("NormalizeSiteName(%q) error = %v, want nil", test.input, err)
			}
			if got != test.input {
				t.Errorf("NormalizeSiteName(%q) = %q, want %q", test.input, got, test.input)
			}
		})
	}
}

func TestNormalizeDeviceNameRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "invalid UTF-8 byte", input: "device\xff"},
		{name: "lone surrogate", input: "device\xed\xa0\x80"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := registry.NormalizeDeviceName(test.input)
			if err == nil {
				t.Errorf("NormalizeDeviceName(%q) error = nil, want an error containing UTF-8", test.input)
			}
			if !strings.Contains(err.Error(), "UTF-8") {
				t.Errorf("NormalizeDeviceName(%q) error = %q, want it to contain UTF-8", test.input, err)
			}
		})
	}
}

func TestNormalizeDeviceNameAcceptsValidMultiByteUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "umlaut o", input: "fw-münchen"},
		{name: "mixed ASCII and UTF-8", input: "core-münchen-02"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := registry.NormalizeDeviceName(test.input)
			if err != nil {
				t.Errorf("NormalizeDeviceName(%q) error = %v, want nil", test.input, err)
			}
			if got != test.input {
				t.Errorf("NormalizeDeviceName(%q) = %q, want %q", test.input, got, test.input)
			}
		})
	}
}

//nolint:gocognit // table-driven test with many cases
func TestReleaseUDPPortRepairsMalformedEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		registryRaw string
		releaseSite string
		releasePort string
		wantRepair  bool
		wantErr     bool
	}{
		{
			name:        "malformed device value",
			registryRaw: `{"udpPorts": {"hq": {"41642": ""}}}`,
			releaseSite: "hq",
			releasePort: "41642",
			wantRepair:  true,
			wantErr:     false,
		},
		{
			name:        "malformed port key",
			registryRaw: `{"udpPorts": {"hq": {"not-a-port": "fw"}}}`,
			releaseSite: "hq",
			releasePort: "41642",
			wantRepair:  false,
			wantErr:     true,
		},
		{
			name:        "malformed entry in different site",
			registryRaw: `{"udpPorts": {"hq": {"41642": ""}, "branch": {"41643": "fw"}}}`,
			releaseSite: "branch",
			releasePort: "41643",
			wantRepair:  false,
			wantErr:     true,
		},
		{
			name:        "malformed entry but (site, port) absent fails closed",
			registryRaw: `{"udpPorts": {"branch": {"41642": ""}}}`,
			releaseSite: "hq",
			releasePort: "41642",
			wantRepair:  false,
			wantErr:     true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := regPath(t)
			if err := os.WriteFile(path, []byte(test.registryRaw+"\n"), 0o600); err != nil {
				t.Fatalf("writing test registry: %v", err)
			}

			var before []byte
			if test.wantErr {
				var readErr error
				before, readErr = os.ReadFile(path)
				if readErr != nil {
					t.Fatalf("reading registry before release: %v", readErr)
				}
			}

			site, port, changed, err := ledgerAt(
				path,
			).ReleaseUDPPort(regCtx(), registryTailnet, test.releaseSite, test.releasePort)
			if (err != nil) != test.wantErr {
				t.Errorf("ReleaseUDPPort error = %v, wantErr %v", err, test.wantErr)
			}
			if (changed && test.wantRepair) || (!changed && !test.wantRepair && !test.wantErr) {
				if site != test.releaseSite || port != test.releasePort {
					t.Errorf("site/port = %q/%q, want %q/%q", site, port, test.releaseSite, test.releasePort)
				}
			}

			if test.wantErr {
				after, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatalf("reading registry after release: %v", readErr)
				}
				if string(after) != string(before) {
					t.Errorf("fail-closed release modified registry:\nbefore: %s\nafter: %s", before, after)
				}
			}

			if test.wantRepair && changed {
				entries, listErr := ledgerAt(path).ListUDPPorts(regCtx(), registryTailnet, "")
				if listErr != nil {
					t.Fatalf("ListUDPPorts after repair: %v", listErr)
				}
				for _, entry := range entries {
					if entry.Site == test.releaseSite && entry.Port == test.releasePort {
						t.Errorf("(site=%q, port=%q) still present after repair", test.releaseSite, test.releasePort)
					}
				}
			}
		})
	}
}

func TestReserveAndReleaseRejectInvalidUTF8SiteName(t *testing.T) {
	t.Parallel()

	path := regPath(t)
	// Reserve should reject invalid UTF-8 in site name
	_, _, _, _, err := ledgerAt(path).ReserveUDPPort(regCtx(), registryTailnet, "branch\xff", "41641", "fw01")
	if err == nil {
		t.Error("ReserveUDPPort with invalid UTF-8 site name error = nil, want error")
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("error does not mention UTF-8: %v", err)
	}

	// Release should also reject invalid UTF-8 in site name
	_, _, _, releaseErr := ledgerAt(path).ReleaseUDPPort(regCtx(), registryTailnet, "branch\xff", "41641")
	if releaseErr == nil {
		t.Error("ReleaseUDPPort with invalid UTF-8 site name error = nil, want error")
	}
	if !strings.Contains(releaseErr.Error(), "UTF-8") {
		t.Errorf("error does not mention UTF-8: %v", releaseErr)
	}
}
