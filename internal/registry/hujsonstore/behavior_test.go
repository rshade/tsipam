package hujsonstore_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

func TestPutKeepsRawSiteIDSpelling(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := "{\"siteIDs\":{\"01\":\"hq\"}}\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	store := hujsonstore.New(dir, nil)
	err := store.Update(context.Background(), "example.ts.net", registry.TxOptions{}, func(tx registry.Tx) error {
		return tx.PutSiteID("1", registry.Entry{Site: "branch"})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "\"01\"") || strings.Contains(string(got), "\"1\"") {
		t.Fatalf("registry = %s, want raw key 01 and no canonical duplicate", got)
	}
}

func TestDeleteRepairsMalformedSiteID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := "{\"siteIDs\":{\"01\":{\"site\":\"hq\",\"owner\":\"bad\\u0007owner\"}}}\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	store := hujsonstore.New(dir, nil)
	err := store.Update(context.Background(), "example.ts.net", registry.TxOptions{}, func(tx registry.Tx) error {
		if _, readErr := tx.SiteIDs(); readErr == nil {
			return errors.New("malformed site IDs were readable")
		}
		removed, delErr := tx.DeleteSiteID("1")
		if delErr != nil {
			return delErr
		}
		if !removed {
			return errors.New("delete did not repair")
		}
		reporter, ok := tx.(interface{ Repaired() bool })
		if !ok || !reporter.Repaired() {
			return errors.New("delete was not marked repaired")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.View(context.Background(), "example.ts.net", registry.TxOptions{}, func(snap registry.Snapshot) error {
		sites, readErr := snap.SiteIDs()
		if readErr != nil {
			return readErr
		}
		if _, present := sites["1"]; present {
			return errors.New("repaired id is still reserved")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeleteOverlapUsesPartialSubnetView(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := "{\"subnetPools\":{\"10.0.0.0/8\":\"a\",\"10.1.0.0/16\":\"b\"}}\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	store := hujsonstore.New(dir, nil)
	err := store.Update(context.Background(), "example.ts.net", registry.TxOptions{}, func(tx registry.Tx) error {
		if _, readErr := tx.SubnetPools(); readErr == nil {
			return errors.New("overlapping pools were readable")
		}
		removed, delErr := tx.DeleteSubnetPool("10.1.0.0/16")
		if delErr != nil || !removed {
			return errors.New("overlap delete failed")
		}
		pools, readErr := tx.SubnetPools()
		if readErr != nil {
			return readErr
		}
		if _, present := pools["10.0.0.0/8"]; !present || len(pools) != 1 {
			return errors.New("remaining pool was not visible")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRequireBlocksLoadedEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := "{\"siteIDs\":{\"65536\":\"branch\"}}\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := &captureLogger{}
	store := hujsonstore.New(dir, logger)
	called := false
	err := store.Update(context.Background(), "example.ts.net", registry.TxOptions{
		Require: []registry.MapKind{registry.MapSiteIDs},
	}, func(registry.Tx) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid site ID key \"65536\"") {
		t.Fatalf("Update error = %v", err)
	}
	if called {
		t.Fatal("fn ran despite Require")
	}
	for _, event := range logger.events {
		if event.msg == "registry loaded" || event.msg == "registry created" {
			t.Fatalf("emitted %q", event.msg)
		}
	}
}

func TestPutPreservesComment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := "{\n\t// reserved for the HQ migration, do not release before Q1\n\t\"siteIDs\": {\n\t\t\"1\": \"hq-vpc\",\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	store := hujsonstore.New(dir, nil)
	err := store.Update(context.Background(), "example.ts.net", registry.TxOptions{}, func(tx registry.Tx) error {
		return tx.PutSiteID("2", registry.Entry{Site: "branch-office"})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "// reserved for the HQ migration, do not release before Q1"
	if !strings.Contains(string(got), want) {
		t.Fatalf("registry = %s, want the comment preserved", got)
	}
}

func TestPutRejectsInvalidMapInsteadOfReplacingIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := []byte(`{"siteIDs":{"1":"hq","1":"branch"}}`)
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	store := hujsonstore.New(dir, nil)
	called := false
	err := store.Update(context.Background(), "example.ts.net", registry.TxOptions{}, func(tx registry.Tx) error {
		called = true
		return tx.PutSiteID("2", registry.Entry{Site: "lab"})
	})
	if !called {
		t.Fatal("fn did not run")
	}
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("PutSiteID error = %v, want the duplicate-key map error", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, seed) {
		t.Fatalf("registry = %s, want the invalid map left unchanged", got)
	}
}

func TestPutUDPPortRejectsPartialMap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := []byte(`{"udpPorts":{"branch":{"nope":"fw","41641":"fw"},"hq":{"41641":"fw","41642":"edge"}}}`)
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	store := hujsonstore.New(dir, nil)
	called := false
	err := store.Update(context.Background(), "example.ts.net", registry.TxOptions{}, func(tx registry.Tx) error {
		called = true
		return tx.PutUDPPort("hq", "41642", "edge")
	})
	if !called {
		t.Fatal("fn did not run")
	}
	if err == nil || !strings.Contains(err.Error(), "UDP port") {
		t.Fatalf("PutUDPPort error = %v, want the invalid UDP map error", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, seed) {
		t.Fatalf("registry = %s, want the partial UDP map left unchanged", got)
	}
}

func TestRepairWriteFailureIsCommitError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("ulimit-based write-failure injection needs a POSIX shell")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell available to inject the write failure")
	}
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := []byte(`{
	"serviceAddresses": {
		"100.100.100.7": {"service":"svc:a","owner":"bad\u0007owner"},
		"10.0.0.5": "svc:b"
	}
}
`)
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	const script = `ulimit -f 0 && trap '' XFSZ && exec "$0" "$@"`
	cmd := exec.Command("sh", "-c", script, os.Args[0], "-test.run=^TestHelperRepairWriteFailure$")
	cmd.Env = append(os.Environ(), "TSIPAM_REPAIR_WRITE_FAIL=1", "TSIPAM_REPAIR_WRITE_PATH="+path)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run()
	output := out.String()
	if strings.Contains(output, "HELPER_RESULT: unexpected-success") {
		t.Skip("environment does not enforce RLIMIT_FSIZE")
	}
	const wantPrefix = `repairing serviceAddresses: removing "100.100.100.7" would fix it, but writing the repaired registry failed:`
	if !strings.Contains(output, wantPrefix) {
		t.Fatalf("helper output = %q, want prefix %q", output, wantPrefix)
	}
	if strings.Contains(output, "releasing service address") {
		t.Fatalf("helper output = %q, want the repair sentence without a release prefix", output)
	}
	if !strings.Contains(output, "HELPER_RESULT: commit-error") {
		t.Fatalf("helper output = %q, want a CommitError", output)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, seed) {
		t.Fatalf("registry = %s, want the unrepaired bytes", got)
	}
}

func TestHelperRepairWriteFailure(t *testing.T) {
	t.Parallel()
	if os.Getenv("TSIPAM_REPAIR_WRITE_FAIL") != "1" {
		t.Skip("only runs as TestRepairWriteFailureIsCommitError's subprocess helper")
	}
	path := os.Getenv("TSIPAM_REPAIR_WRITE_PATH")
	ledger := registry.NewLedger(hujsonstore.New(filepath.Dir(path), nil), nil)
	_, _, err := ledger.ReleaseServiceAddress(context.Background(), "example.ts.net", "100.100.100.7")
	if err == nil {
		fmt.Fprintln(os.Stdout, "HELPER_RESULT: unexpected-success")
		os.Exit(1)
	}
	if _, ok := errors.AsType[*registry.CommitError](err); ok {
		fmt.Fprintln(os.Stdout, "HELPER_RESULT: commit-error")
	}
	fmt.Fprintln(os.Stdout, err.Error())
	os.Exit(1)
}

func TestRawReaders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := hujsonstore.Path(dir, "example.ts.net")
	seed := `{
		"siteIDs": {"1": {"site": "hq", "owner": "netops"}},
		"udpPorts": {"hq": {"41641": "fw"}}
	}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, _, _, owners := hujsonstore.RawEntries(path)
	if sites["1"] != "hq" || len(owners) != 1 || owners[0] != "netops" {
		t.Fatalf("RawEntries sites=%v owners=%v", sites, owners)
	}
	ports := hujsonstore.RawUDPPorts(path)
	if len(ports) != 1 || ports[0].Site != "hq" || ports[0].Port != "41641" || ports[0].Device != "fw" {
		t.Fatalf("RawUDPPorts = %+v", ports)
	}
}
