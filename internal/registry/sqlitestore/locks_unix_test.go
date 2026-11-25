//go:build linux || darwin

package sqlitestore_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

const (
	peerEnv = "TSIPAM_TEST_PEER_DATABASE"
	// The byte range SQLite locks to share a database file.
	sharedLockStart = 1<<30 + 2
	sharedLockSize  = 510
)

// POSIX drops a process's locks on a file when it closes any descriptor of it,
// so a rehearsal must not read the database file directly. Only another process
// can ask whether the lock is still held.
func TestRehearsalKeepsThisProcessesLocks(t *testing.T) {
	t.Parallel()
	if path := os.Getenv(peerEnv); path != "" {
		assertShared(t, path)
		return
	}
	path := filepath.Join(t.TempDir(), "tsipam.db")
	store := open(t, path)
	reserveSiteID(t, store, "1")
	if _, err := rehearseReserve(t, store, "2"); err != nil {
		t.Fatal(err)
	}
	store.IdentityValues(t.Context(), tailnet, true)
	peer := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+t.Name()+"$")
	peer.Env = append(os.Environ(), peerEnv+"="+path)
	if output, err := peer.CombinedOutput(); err != nil {
		t.Fatalf("peer: %v\n%s", err, output)
	}
}

func assertShared(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	lock := syscall.Flock_t{
		Type: syscall.F_WRLCK, Whence: io.SeekStart, Start: sharedLockStart, Len: sharedLockSize,
	}
	if err = syscall.FcntlFlock(file.Fd(), syscall.F_GETLK, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Type == syscall.F_UNLCK {
		t.Error("the database is not locked while its store is open")
	}
}
