package sqlitestore_test

import (
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rshade/tsipam/internal/registry"
)

const (
	walHeaderSize      = 32
	walFrameHeaderSize = 24
	walSaltOffset      = 16
)

// rowsOf lists every row of the tables a registry lives in.
func rowsOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	var dump strings.Builder
	for _, query := range []string{
		"SELECT site_id || '|' || site FROM site_ids ORDER BY site_id",
		"SELECT id || '|' || action || '|' || key FROM events ORDER BY id",
		"SELECT integrity_check FROM pragma_integrity_check",
	} {
		dump.WriteString(strings.Join(columnOf(t, db, query), "\n") + "\n")
	}
	return dump.String()
}

func columnOf(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var values []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

// TestImageMatchesSQLite compares the copy with SQLite's own reading across
// growth, deletion, and checkpoints that leave the log full, partly copied, and
// restarted.
func TestImageMatchesSQLite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	store := open(t, path)
	ledger := registry.NewLedger(store, nil)
	raw := rawDB(t, path)
	for step := range 120 {
		reserveSiteID(t, store, strconv.Itoa(step))
		if step%3 == 2 {
			if _, _, err := ledger.ReleaseSiteID(t.Context(), tailnet, strconv.Itoa(step-1)); err != nil {
				t.Fatal(err)
			}
		}
		switch step {
		case 30:
			mustExec(t, raw, "PRAGMA wal_checkpoint(PASSIVE)")
		case 60:
			mustExec(t, raw, "PRAGMA wal_checkpoint(RESTART)")
		case 90:
			mustExec(t, raw, "PRAGMA wal_checkpoint(TRUNCATE)")
		}
		if step%10 != 0 && step%30 != 1 {
			continue
		}
		image, err := store.OpenImage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		got, want := rowsOf(t, image), rowsOf(t, raw)
		_ = image.Close()
		if got != want {
			t.Fatalf("step %d: image holds\n%s\nwant\n%s", step, got, want)
		}
	}
}

// TestImageIsConsistentUnderWriters checks an invariant every commit keeps: one
// event per reserved site ID, in a database that passes its own checks.
func TestImageIsConsistentUnderWriters(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	seed := open(t, path)
	reserveSiteID(t, seed, "0")
	const writers, writes = 2, 150
	var group sync.WaitGroup
	for writer := range writers {
		store := open(t, path)
		raw := rawDB(t, path)
		group.Go(func() {
			for write := range writes {
				_, _, _, _, err := registry.NewLedger(store, nil).ReserveSiteID(t.Context(), tailnet,
					strconv.Itoa(1+writer*writes+write), "hq-vpc", registry.ReservationMetadata{})
				if err != nil {
					t.Error(err)
					return
				}
				// Moving the commit from the log to the database right away is what
				// would split a pair read only once. A busy checkpoint is fine.
				_, _ = raw.ExecContext(t.Context(), "PRAGMA wal_checkpoint(TRUNCATE)")
			}
		})
	}
	finished := make(chan struct{})
	go func() { group.Wait(); close(finished) }()
	reads, last := 0, 0
	for done := false; !done; reads++ {
		select {
		case <-finished:
			done = true
		default:
		}
		image, err := seed.OpenImage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var sites, events int
		var check string
		if err = image.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM site_ids),
			(SELECT count(*) FROM events), (SELECT group_concat(integrity_check) FROM pragma_integrity_check)`,
		).Scan(&sites, &events, &check); err != nil {
			t.Fatal(err)
		}
		_ = image.Close()
		if sites != events || check != "ok" || sites < last {
			t.Fatalf("image holds %d site IDs after %d, %d events, check %q", sites, last, events, check)
		}
		last = sites
	}
	if last != writers*writes+1 {
		t.Errorf("last image holds %d site IDs, want %d", last, writers*writes+1)
	}
	t.Logf("%d images read", reads)
}

// damagedLog copies an open database whose log holds three commits after the
// first, and lets damage change the copy's log.
func damagedLog(t *testing.T, damage func(wal []byte, frameSize int) []byte) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "tsipam.db")
	store := open(t, source)
	reserveSiteID(t, store, "0")
	mustExec(t, rawDB(t, source), "PRAGMA wal_checkpoint(TRUNCATE)")
	for id := 1; id <= 3; id++ {
		reserveSiteID(t, store, strconv.Itoa(id))
	}
	copied := filepath.Join(t.TempDir(), "tsipam.db")
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(source + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if suffix == "-wal" {
			frameSize := walFrameHeaderSize + int(binary.BigEndian.Uint32(data[8:]))
			data = damage(data, frameSize)
		}
		if err = os.WriteFile(copied+suffix, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return copied
}

func lastFrame(wal []byte, frameSize int) int { return len(wal) - frameSize }

// TestImageRecoversADamagedLogAsSQLiteDoes reads each damaged pair twice: as an
// image, then by opening it, which makes SQLite run its own recovery.
func TestImageRecoversADamagedLogAsSQLiteDoes(t *testing.T) {
	t.Parallel()
	cases := map[string]func(wal []byte, frameSize int) []byte{
		"intact":       func(wal []byte, _ int) []byte { return wal },
		"empty":        func([]byte, int) []byte { return nil },
		"short header": func(wal []byte, _ int) []byte { return wal[:walHeaderSize-1] },
		"header only":  func(wal []byte, _ int) []byte { return wal[:walHeaderSize] },
		"torn last frame": func(wal []byte, _ int) []byte {
			return wal[:len(wal)-1]
		},
		"wrong page checksum": func(wal []byte, frameSize int) []byte {
			wal[lastFrame(wal, frameSize)+walFrameHeaderSize+100] ^= 1
			return wal
		},
		"wrong checksum in the middle": func(wal []byte, frameSize int) []byte {
			wal[walHeaderSize+frameSize+walFrameHeaderSize] ^= 1
			return wal
		},
		"stale salt": func(wal []byte, frameSize int) []byte {
			wal[lastFrame(wal, frameSize)+8] ^= 1
			return wal
		},
		"uncommitted last frame": func(wal []byte, frameSize int) []byte {
			return resum(wal, frameSize, func(index, frames int, frame []byte) {
				if index == frames-1 {
					binary.BigEndian.PutUint32(frame[4:], 0)
				}
			})
		},
		"wrong header checksum":        func(wal []byte, _ int) []byte { wal[walSaltOffset] ^= 1; return wal },
		"wrong stored header checksum": func(wal []byte, _ int) []byte { wal[24] ^= 1; return wal },
		"wrong magic":                  func(wal []byte, _ int) []byte { wal[0] ^= 1; return wal },
		"big-endian checksums": func(wal []byte, frameSize int) []byte {
			wal[3] |= 1
			return resum(wal, frameSize, func(int, int, []byte) {})
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := damagedLog(t, damage)
			image, err := open(t, path).OpenImage(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got := rowsOf(t, image)
			_ = image.Close()
			if want := rowsOf(t, rawDB(t, path)); got != want {
				t.Errorf("image holds\n%s\nSQLite recovers\n%s", got, want)
			}
		})
	}
}

// resum rewrites every checksum in wal after change has edited its frames.
func resum(wal []byte, frameSize int, change func(index, frames int, frame []byte)) []byte {
	var order binary.ByteOrder = binary.LittleEndian
	if wal[3]&1 != 0 {
		order = binary.BigEndian
	}
	var first, second uint32
	add := func(data []byte) {
		for ; len(data) >= 8; data = data[8:] {
			first += order.Uint32(data) + second
			second += order.Uint32(data[4:]) + first
		}
	}
	store := func(data []byte) {
		binary.BigEndian.PutUint32(data, first)
		binary.BigEndian.PutUint32(data[4:], second)
	}
	add(wal[:24])
	store(wal[24:])
	frames := (len(wal) - walHeaderSize) / frameSize
	for index := range frames {
		frame := wal[walHeaderSize+index*frameSize:][:frameSize]
		change(index, frames, frame)
		add(frame[:8])
		add(frame[walFrameHeaderSize:])
		store(frame[16:])
	}
	return wal
}

// TestHotJournalIsRefused covers a crash before the database was in WAL mode,
// when its recovery data is in a rollback journal.
func TestHotJournalIsRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tsipam.db")
	closedDatabase(t, path)
	if err := os.WriteFile(path+"-journal", []byte("recovery data"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := rehearseReserve(t, open(t, path), "9")
	if err == nil || !strings.Contains(err.Error(), "interrupted write") {
		t.Errorf("rehearsal = %t, %v; want it refused", changed, err)
	}
}

func TestInterruptedCheckpointIsRefused(t *testing.T) {
	t.Parallel()
	changed, err := rehearseReserve(t, open(t, interruptedCheckpoint(t)), "9")
	if err == nil || !strings.Contains(err.Error(), "interrupted write") {
		t.Errorf("rehearsal = %t, %v; want it refused", changed, err)
	}
}

// A real command recovers what a rehearsal refuses to read, so its error text
// must still be sanitized from the registry's values.
func TestIdentitiesOfAnInterruptedCheckpoint(t *testing.T) {
	t.Parallel()
	store := open(t, interruptedCheckpoint(t))
	if sites := store.IdentityValues(t.Context(), tailnet, false).Sites; len(sites) == 0 {
		t.Error("no identities were read")
	}
}

// interruptedCheckpoint is the state a crash leaves when a checkpoint of a
// database that grew inside its log copied only page 1.
func interruptedCheckpoint(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "tsipam.db")
	reserveSiteID(t, open(t, source), "0")
	database, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.ReadFile(source + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	pageSize := int(binary.BigEndian.Uint32(wal[8:]))
	frameSize := walFrameHeaderSize + pageSize
	for rest := wal[walHeaderSize:]; len(rest) >= frameSize; rest = rest[frameSize:] {
		if binary.BigEndian.Uint32(rest) == 1 {
			copy(database, rest[walFrameHeaderSize:frameSize])
		}
	}
	copied := filepath.Join(t.TempDir(), "tsipam.db")
	for suffix, data := range map[string][]byte{"": database, "-wal": wal} {
		if err = os.WriteFile(copied+suffix, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return copied
}

// SQLite keeps the log beside the file a symbolic link leads to.
func TestRehearsalThroughASymbolicLink(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "real.db")
	reserveSiteID(t, open(t, target), "1")
	link := filepath.Join(t.TempDir(), "tsipam.db")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	if changed, err := rehearseReserve(t, open(t, link), "1"); err != nil || changed {
		t.Errorf("rehearsed repeat = %t, %v; want no change", changed, err)
	}
}

// A log left beside a deleted or emptied database is not part of the next one.
func TestRehearsalIgnoresAnOrphanLog(t *testing.T) {
	t.Parallel()
	for name, orphan := range map[string]func(path string) error{
		"deleted": os.Remove,
		"emptied": func(path string) error { return os.Truncate(path, 0) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := damagedLog(t, func(wal []byte, _ int) []byte { return wal })
			if err := orphan(path); err != nil {
				t.Fatal(err)
			}
			store := open(t, path)
			if changed, err := rehearseReserve(t, store, "3"); err != nil || !changed {
				t.Errorf("rehearsal = %t, %v; want a change", changed, err)
			}
			reserveSiteID(t, store, "3")
			if entries, err := listSiteIDs(t, store); err != nil || len(entries) != 1 {
				t.Errorf("the real run left %v, %v; want only its own entry", entries, err)
			}
		})
	}
}

// Closing the last connection copies the log into the database, so a database
// that is refused must not be opened at all.
func TestRefusedDatabaseWithALogIsLeftUnchanged(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		change string
		want   string
	}{
		"newer schema": {"PRAGMA user_version = 99", "newer tsipam"},
		"foreign":      {"PRAGMA application_id = 7", "did not create"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), "tsipam.db")
			reserveSiteID(t, open(t, source), "1")
			mustExec(t, rawDB(t, source), tc.change)
			dir := t.TempDir()
			path := filepath.Join(dir, "tsipam.db")
			copyFiles(t, source, path, "", "-wal")
			before := dirState(t, dir)
			store := open(t, path)
			_, _, _, _, reserveErr := registry.NewLedger(store, nil).ReserveSiteID(
				t.Context(), tailnet, "9", "hq-vpc", registry.ReservationMetadata{},
			)
			_, listErr := listSiteIDs(t, store)
			for _, err := range []error{reserveErr, listErr} {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error = %v, want %q", err, tc.want)
				}
			}
			if sites := store.IdentityValues(t.Context(), tailnet, false).Sites; len(sites) == 0 {
				t.Error("a refused registry's identities were not read")
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if after := dirState(t, dir); !reflect.DeepEqual(after, before) {
				t.Errorf("directory changed from %v to %v", before, after)
			}
		})
	}
}

func copyFiles(t *testing.T, source, target string, suffixes ...string) {
	t.Helper()
	for _, suffix := range suffixes {
		data, err := os.ReadFile(source + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target+suffix, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Any program may number its own schema.
func TestForeignDatabaseWithAMatchingVersionIsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "tsipam.db")
	raw := rawDB(t, path)
	mustExec(t, raw, "CREATE TABLE other (value TEXT)")
	mustExec(t, raw, "PRAGMA user_version = 1")
	before := dirState(t, dir)
	store := open(t, path)
	_, _, _, _, err := registry.NewLedger(store, nil).ReserveSiteID(
		t.Context(), tailnet, "9", "hq-vpc", registry.ReservationMetadata{},
	)
	if err == nil || !strings.Contains(err.Error(), "did not create") {
		t.Errorf("reserve error = %v, want the database refused", err)
	}
	if _, rehearseErr := rehearseReserve(t, store, "9"); rehearseErr == nil || rehearseErr.Error() != err.Error() {
		t.Errorf("rehearsed error = %v, want %v", rehearseErr, err)
	}
	if after := dirState(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("directory changed from %v to %v", before, after)
	}
}
