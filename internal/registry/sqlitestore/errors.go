package sqlitestore

import (
	"errors"
	"fmt"
	"os"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/rshade/tsipam/internal/registry"
)

// primaryCodeMask reduces an extended SQLite result code to its primary code.
const primaryCodeMask = 0xff

var (
	errLockTimeout = fmt.Errorf(
		"acquiring lock: timed out after %s waiting for a concurrent tsipam to finish", lockTimeout,
	)
	errNewerSchema = errors.New(
		"the sqlite registry was written by a newer tsipam; a newer tsipam is required to use it",
	)
	errForeignDatabase = errors.New("the selected file is a SQLite database that tsipam did not create")
)

// notFoundError is an absent registry. Its text leaves out the database path,
// which sanitized output must not carry.
type notFoundError struct {
	tailnet string
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf(
		"no registry for tailnet %q in the selected sqlite database (a reserve command creates it)", e.tailnet,
	)
}

func (e *notFoundError) Unwrap() error { return registry.ErrNotFound }

func resultCode(err error) int {
	sqliteErr, ok := errors.AsType[*sqlite.Error](err)
	if !ok {
		return sqlite3.SQLITE_OK
	}
	return sqliteErr.Code() & primaryCodeMask
}

func isBusy(err error) bool {
	code := resultCode(err)
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}

// describe gives a driver or filesystem error the text operators see. The
// driver's text carries no path, and a filesystem error's path is dropped.
func describe(err error) error {
	if pathErr, ok := errors.AsType[*os.PathError](err); ok {
		return pathErr.Err
	}
	switch resultCode(err) {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return errLockTimeout
	case sqlite3.SQLITE_NOTADB:
		return fmt.Errorf("the selected file is not a SQLite database: %w", err)
	case sqlite3.SQLITE_CANTOPEN:
		return fmt.Errorf("the selected sqlite database cannot be opened: %w", err)
	case sqlite3.SQLITE_CONSTRAINT:
		return fmt.Errorf("sqlite registry integrity check failed: %w", err)
	default:
		return err
	}
}
