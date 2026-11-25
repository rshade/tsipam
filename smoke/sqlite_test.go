//go:build smoke

package smoke_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSQLiteRegistry walks a SQLite registry through the environment selector:
// reserve, list, release, and the history all three leave behind.
func TestSQLiteRegistry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := map[string]string{"TSIPAM_REGISTRY": "sqlite://" + filepath.Join(dir, "tsipam.db")}

	expect(t, run(t, env, "subnet", "list"), exitInvalid)
	rehearsed := run(t, env, "subnet", "reserve", "10.20.0.0/16", "hq-vpc", "--dry-run")
	expect(t, rehearsed, exitOK)
	if !strings.HasPrefix(rehearsed.stdout, "dry run: would reserve") {
		t.Errorf("rehearsal = %q", rehearsed.stdout)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the rehearsal left %v, %v", entries, err)
	}
	expect(t, run(t, env, "subnet", "reserve", "10.20.0.0/16", "hq-vpc", "--ticket", "OPS-1"), exitOK)
	expect(t, run(t, env, "subnet", "reserve", "10.20.5.0/24", "branch"), exitRefused)
	res := run(t, env, "subnet", "list")
	expect(t, res, exitOK)
	if !strings.HasPrefix(res.stdout, "10.20.0.0/16\thq-vpc\tticket=OPS-1 reserved=") {
		t.Errorf("list = %q, want the reservation", res.stdout)
	}
	expect(t, run(t, env, "subnet", "release", "10.20.0.0/16", "--yes"), exitOK)

	res = run(t, env, "registry", "history", flagFormat, formatJSON)
	expect(t, res, exitOK)
	var events []struct {
		Action string `json:"action"`
		Key    string `json:"key"`
	}
	decodeEnvelope(t, res.stdout).field(t, "events", &events)
	var got []string
	for _, event := range events {
		got = append(got, event.Action+" "+event.Key)
	}
	if want := []string{"release 10.20.0.0/16", "reserve 10.20.0.0/16"}; !slices.Equal(got, want) {
		t.Errorf("history = %v, want %v", got, want)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "tsipam.db" {
		t.Errorf("directory holds %v, want only tsipam.db", entries)
	}
}

func TestHistoryNeedsSQLite(t *testing.T) {
	t.Parallel()
	res := run(t, nil, newRegistry(t).args("registry", "history")...)
	expect(t, res, exitInvalid)
	if !strings.Contains(res.stderr, "requires the sqlite backend") {
		t.Errorf("stderr = %q, want the backend named", res.stderr)
	}
}
