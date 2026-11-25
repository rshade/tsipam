package hujsonstore_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

// dirState is every file in dir with its bytes, mode, and modification time.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := make(map[string]string, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		state[entry.Name()] = fmt.Sprintf("%s %s %q", info.Mode(), info.ModTime(), raw)
	}
	return state
}

func assertDirUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := dirState(t, dir)
	if !maps.Equal(before, after) {
		t.Errorf("directory changed: before %v, after %v", before, after)
	}
}

func rehearsingLedger(dir string, logger *captureLogger) *registry.Ledger {
	return registry.NewLedger(hujsonstore.New(dir, logger), logger).Rehearsal()
}

const commentedRegistry = `{
  // Site IDs are handed out by netops.
  "siteIDs": {"1": "hq"},
}
`

func writeRegistry(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(hujsonstore.Path(dir, registryTailnet), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRehearsalLeavesTheDirectoryUnchanged(t *testing.T) {
	t.Parallel()
	reserve := func(led *registry.Ledger) (bool, error) {
		_, _, _, changed, err := led.ReserveSiteID(
			regCtx(), registryTailnet, "2", "branch", registry.ReservationMetadata{},
		)
		return changed, err
	}
	release := func(led *registry.Ledger) (bool, error) {
		_, changed, err := led.ReleaseSiteID(regCtx(), registryTailnet, "1")
		return changed, err
	}
	tests := []struct {
		name     string
		content  string
		readOnly bool
		run      func(*registry.Ledger) (bool, error)
	}{
		{name: "present", content: commentedRegistry, run: reserve},
		{name: "absent", run: reserve},
		{name: "release", content: commentedRegistry, run: release},
		{name: "read-only directory", content: commentedRegistry, readOnly: true, run: reserve},
		{name: "release in a read-only directory", content: commentedRegistry, readOnly: true, run: release},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if test.content != "" {
				writeRegistry(t, dir, test.content)
			}
			if test.readOnly {
				makeReadOnly(t, dir)
			}
			before := dirState(t, dir)
			logger := &captureLogger{}
			changed, err := test.run(rehearsingLedger(dir, logger))
			if err != nil || !changed {
				t.Fatalf("rehearsal = changed %t, error %v; want true and nil", changed, err)
			}
			assertDirUnchanged(t, dir, before)
			assertMessages(t, logger.events, []string{"registry loaded"})
		})
	}
}

// makeReadOnly removes write permission from dir for the test. Root ignores
// the mode, so the case then proves only that rehearsal does not write.
func makeReadOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Error(err)
		}
	})
}

func TestRehearsalIgnoresAHeldLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRegistry(t, dir, commentedRegistry)
	if err := os.WriteFile(hujsonstore.Path(dir, registryTailnet)+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := dirState(t, dir)
	_, changed, err := rehearsingLedger(dir, &captureLogger{}).ReleaseSiteID(regCtx(), registryTailnet, "1")
	if err != nil || !changed {
		t.Fatalf("rehearsal = changed %t, error %v; want true and nil", changed, err)
	}
	assertDirUnchanged(t, dir, before)
}

func TestRehearsalMissingDirectory(t *testing.T) {
	t.Parallel()
	for _, create := range []bool{true, false} {
		t.Run(fmt.Sprintf("create=%t", create), func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "missing")
			err := hujsonstore.New(dir, nil).Update(
				context.Background(), registryTailnet,
				registry.TxOptions{DryRun: true, CreateIfMissing: create},
				func(registry.Tx) error { return errors.New("fn ran") },
			)
			assertNotFound(t, err, fmt.Sprintf(
				"registry directory %q does not exist: no such file or directory", dir,
			))
			if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("stat = %v, want the directory still missing", statErr)
			}
		})
	}
}

func TestRehearsalMissingFileWithoutCreate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, _, err := rehearsingLedger(dir, &captureLogger{}).ReleaseSiteID(regCtx(), registryTailnet, "1")
	assertNotFound(t, err, fmt.Sprintf(
		"no registry for tailnet %q in %q (a reserve command creates it): no such file or directory",
		registryTailnet, dir,
	))
	assertDirUnchanged(t, dir, map[string]string{})
}

func TestRehearsalRequiresValidMaps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRegistry(t, dir, "{\"siteIDs\":{\"65536\":\"branch\"}}\n")
	_, _, _, _, err := rehearsingLedger(dir, &captureLogger{}).ReserveSiteID(
		regCtx(), registryTailnet, "1", "hq", registry.ReservationMetadata{},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid site ID key \"65536\"") {
		t.Fatalf("error = %v, want the invalid key named", err)
	}
}

// TestRehearsalReportsReleaseRepair covers the one release outcome the
// in-memory store cannot reach: a malformed entry the delete repairs.
func TestRehearsalReportsReleaseRepair(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		run     func(*registry.Ledger) (bool, error)
	}{
		{
			name:    "flat map",
			content: "{\"siteIDs\":{\"01\":{\"site\":\"hq\",\"owner\":\"bad\\u0007owner\"}}}\n",
			run: func(led *registry.Ledger) (bool, error) {
				_, changed, err := led.ReleaseSiteID(regCtx(), registryTailnet, "1")
				return changed, err
			},
		},
		{
			name:    "nested map",
			content: "{\"udpPorts\":{\"hq\":{\"41642\":\"bad\\u0007device\"}}}\n",
			run: func(led *registry.Ledger) (bool, error) {
				_, _, changed, err := led.ReleaseUDPPort(regCtx(), registryTailnet, "hq", "41642")
				return changed, err
			},
		},
		{
			name:    "undecidable",
			content: "{\"siteIDs\":{\"65536\":\"a\",\"65537\":\"b\"}}\n",
			run: func(led *registry.Ledger) (bool, error) {
				_, changed, err := led.ReleaseSiteID(regCtx(), registryTailnet, "1")
				return changed, err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeRegistry(t, dir, test.content)
			before := dirState(t, dir)
			logger := &captureLogger{}
			rehearsed, rehearseErr := test.run(rehearsingLedger(dir, logger))
			assertDirUnchanged(t, dir, before)
			if slices.ContainsFunc(logger.events, func(event logged) bool {
				return strings.Contains(event.msg, "removed")
			}) {
				t.Errorf("the rehearsal logged a removal: %+v", logger.events)
			}

			committed, commitErr := test.run(registry.NewLedger(hujsonstore.New(dir, nil), nil))
			if rehearsed != committed || fmt.Sprint(rehearseErr) != fmt.Sprint(commitErr) {
				t.Errorf("rehearsed (%t, %v), committed (%t, %v)", rehearsed, rehearseErr, committed, commitErr)
			}
			after, err := os.ReadFile(hujsonstore.Path(dir, registryTailnet))
			if err != nil {
				t.Fatal(err)
			}
			if committed == bytes.Equal(after, []byte(test.content)) {
				t.Errorf("committed = %t with registry bytes %q", committed, after)
			}
		})
	}
}
