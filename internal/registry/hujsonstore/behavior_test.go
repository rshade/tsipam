package hujsonstore_test

import (
	"context"
	"errors"
	"os"
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
