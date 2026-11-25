package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/diagnostics"
)

// runSubnetAllocateCLI drives the full runCLI path with a stub device
// collector, so exit codes and sanitized stderr match a real invocation.
func runSubnetAllocateCLI(
	t *testing.T, registryDir string, devices []deviceRecord, args ...string,
) (int, string, string) {
	t.Helper()
	listDevices := func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
		return devices, nil
	}
	var outBuf, errBuf bytes.Buffer
	code := runCLI(
		context.Background(),
		append([]string{commandSubnet, "allocate"}, append(args, "--registry-dir", registryDir)...),
		fixedKeyGetenv, &outBuf, &errBuf, listDevices,
	)
	return code, outBuf.String(), errBuf.String()
}

func TestSubnetAllocateSkipsRegistryAndLiveExclusions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, _, err := runSubnet(t, dir, "reserve", "10.0.0.0/24", "branch"); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	router := []deviceRecord{{Name: "router", AdvertisedRoutes: []string{"10.0.1.0/24"}}}

	code, stdout, stderr := runSubnetAllocateCLI(t, dir, router, "10.0.0.0/16", "hq-vpc", "--size", "24")
	if code != exitOK {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "allocated subnet CIDR 10.0.2.0/24 for \"hq-vpc\"\n" {
		t.Errorf("stdout = %q, want the third child allocated for hq-vpc", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}

	listed, _, err := runSubnet(t, dir, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listed, "10.0.2.0/24\thq-vpc") {
		t.Errorf("list = %q, want the allocated block", listed)
	}
}

func TestSubnetAllocateJSONEnvelopeAndTicketReplay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	code, stdout, stderr := runSubnetAllocateCLI(t, dir, nil,
		"10.0.0.0/16", "hq-vpc", "--size", "24", "--ticket", "OPS-1", "--format", formatJSON)
	if code != exitOK {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	var allocated outputEnvelope
	if err := json.Unmarshal([]byte(stdout), &allocated); err != nil {
		t.Fatalf("decode allocate JSON: %v", err)
	}
	if allocated.Command != commandSubnetAllocate || allocated.Status != statusOK ||
		allocated.Changed == nil || !*allocated.Changed ||
		allocated.CIDR != "10.0.0.0/24" || allocated.Site != "hq-vpc" || allocated.Ticket != "OPS-1" ||
		allocated.Message != "allocated subnet CIDR 10.0.0.0/24 for \"hq-vpc\"" {
		t.Errorf("allocate envelope = %+v, want a changed hq-vpc allocation carrying the ticket", allocated)
	}
	before, err := os.ReadFile(filepath.Join(dir, "local.ipam.hujson"))
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}

	code, stdout, stderr = runSubnetAllocateCLI(t, dir, nil,
		"10.0.0.0/16", "hq-vpc", "--size", "24", "--ticket", "OPS-1", "--format", formatJSON)
	if code != exitOK {
		t.Fatalf("replay exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	var replayed outputEnvelope
	if err = json.Unmarshal([]byte(stdout), &replayed); err != nil {
		t.Fatalf("decode replay JSON: %v", err)
	}
	if replayed.Status != statusNoChanges || replayed.Changed == nil || *replayed.Changed ||
		replayed.CIDR != "10.0.0.0/24" ||
		replayed.Message != "subnet CIDR 10.0.0.0/24 already allocated for \"hq-vpc\"" {
		t.Errorf("replay envelope = %+v, want the same block with changed=false", replayed)
	}
	after, err := os.ReadFile(filepath.Join(dir, "local.ipam.hujson"))
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("replay rewrote the registry:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestSubnetAllocateExhaustionExitsUsageWithEmptyStdout(t *testing.T) {
	t.Parallel()
	for _, format := range []string{formatHuman, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if _, _, err := runSubnet(t, dir, "reserve", "10.0.0.0/25", "branch"); err != nil {
				t.Fatalf("seeding reservation: %v", err)
			}
			router := []deviceRecord{{Name: "router", AdvertisedRoutes: []string{"10.0.0.128/25"}}}

			code, stdout, stderr := runSubnetAllocateCLI(t, dir, router,
				"10.0.0.0/24", "hq-vpc", "--size", "25", "--format", format)
			if code != exitUsage {
				t.Errorf("exit = %d, want %d", code, exitUsage)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, "no free /25 block in 10.0.0.0/24") {
				t.Errorf("stderr = %q, want the exhaustion message", stderr)
			}
			listed, _, err := runSubnet(t, dir, "list")
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if strings.Contains(listed, "hq-vpc") {
				t.Errorf("exhaustion wrote a reservation: %q", listed)
			}
		})
	}
}

func TestSubnetAllocateSanitizesExhaustionMessage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	router := []deviceRecord{{Name: "router", AdvertisedRoutes: []string{"10.0.0.0/24"}}}

	code, stdout, stderr := runSubnetAllocateCLI(t, dir, router,
		"10.0.0.0/24", "hq-vpc", "--size", "24", "--sanitize", "identity")
	if code != exitUsage || stdout != "" {
		t.Fatalf("exit = %d stdout = %q, want %d and empty", code, stdout, exitUsage)
	}
	if strings.Contains(stderr, "10.0.0.0/24") {
		t.Errorf("sanitized exhaustion stderr leaks the pool: %s", stderr)
	}
	if !strings.Contains(stderr, "no free /24 block in ") {
		t.Errorf("stderr = %q, want the exhaustion template with a placeholder", stderr)
	}
}

func TestSubnetAllocateValidatesBeforeCollectingDevices(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   []string
		wantIn string
	}{
		{
			name: "size wider than pool", args: []string{"10.0.0.0/24", "hq-vpc", "--size", "16"},
			wantIn: "between pool /24",
		},
		{
			name: "size beyond family", args: []string{"10.0.0.0/24", "hq-vpc", "--size", "33"},
			wantIn: "address length /32",
		},
		{name: "malformed pool", args: []string{"10.0.0.0", "hq-vpc", "--size", "24"}, wantIn: "not a valid prefix"},
		{name: "missing size", args: []string{"10.0.0.0/24", "hq-vpc"}, wantIn: `"size" not set`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			listDevices := func(context.Context, diagnostics.Logger) ([]deviceRecord, error) {
				t.Error("device collector ran before validation")
				return nil, nil
			}
			var outBuf, errBuf bytes.Buffer
			code := runCLI(context.Background(),
				append([]string{commandSubnet, "allocate"}, append(tc.args, "--registry-dir", dir)...),
				fixedKeyGetenv, &outBuf, &errBuf, listDevices)
			if code != exitUsage {
				t.Errorf("exit = %d, want %d", code, exitUsage)
			}
			if !strings.Contains(failureText(t, errBuf.String()), tc.wantIn) {
				t.Errorf("stderr = %q, want it to contain %q", errBuf.String(), tc.wantIn)
			}
			if _, err := os.Stat(filepath.Join(dir, "local.ipam.hujson")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("registry was created by a rejected allocation (stat err = %v)", err)
			}
		})
	}
}

func TestSubnetAllocateRefusesWithoutDeviceCollector(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, _, err := runSubnet(t, dir, "allocate", "10.0.0.0/16", "hq-vpc", "--size", "24")
	if err == nil || !strings.Contains(err.Error(), "device collector is not configured") {
		t.Fatalf("error = %v, want the collector refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "local.ipam.hujson")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("registry was created without a live read (stat err = %v)", statErr)
	}
}

func TestSubnetAllocateConcurrentRunsReceiveDistinctBlocks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const runs = 2
	var wg sync.WaitGroup
	codes := make([]int, runs)
	outs := make([]string, runs)
	errs := make([]string, runs)
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], outs[i], errs[i] = runSubnetAllocateCLI(t, dir, nil,
				"10.0.0.0/16", "site-"+string(rune('a'+i)), "--size", "24", "--format", formatJSON)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i := range runs {
		if codes[i] != exitOK {
			t.Errorf("run %d exit = %d, want 0 (stderr: %s)", i, codes[i], errs[i])
			continue
		}
		var envelope outputEnvelope
		if err := json.Unmarshal([]byte(outs[i]), &envelope); err != nil {
			t.Fatalf("run %d: decode JSON: %v", i, err)
		}
		if !strings.HasSuffix(envelope.CIDR, "/24") || !strings.HasPrefix(envelope.CIDR, "10.0.") {
			t.Errorf("run %d allocated %q, want a /24 inside 10.0.0.0/16", i, envelope.CIDR)
		}
		if seen[envelope.CIDR] {
			t.Errorf("both concurrent runs received %s", envelope.CIDR)
		}
		seen[envelope.CIDR] = true
	}
}
