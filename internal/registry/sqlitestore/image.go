package sqlitestore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	sqlite3 "modernc.org/sqlite/lib"

	"github.com/rshade/tsipam/internal/registry"
)

const (
	walSuffix     = "-wal"
	journalSuffix = "-journal"
)

var (
	errUnstable = fmt.Errorf(
		"the sqlite registry kept changing for %s, so no consistent copy could be read", lockTimeout,
	)
	errInterrupted = errors.New(
		"the sqlite registry holds an interrupted write, or is damaged; a command that is not a dry run can recover it",
	)
)

// serializer and deserializer are the driver connection's methods for moving a
// whole database in and out of memory.
type serializer interface{ Serialize() ([]byte, error) }

type deserializer interface{ Deserialize(data []byte) error }

// image returns the committed database as bytes, or nil when the file is
// absent or empty. It takes no lock and creates, changes, and removes no file,
// which an ordinary connection cannot do: opening a WAL database creates its
// -wal and -shm files and needs a writable directory.
//
// The database and its log are read twice, and accepted when neither changed.
// Both then held those bytes at the moment between the two readings, so they
// belong together even if a checkpoint was running: SQLite keeps the pair
// recoverable at every moment, which is what makes it crash-safe.
func (s *Store) image(ctx context.Context) ([]byte, error) {
	started := time.Now()
	for {
		data, settled, err := s.readImage(ctx)
		if settled {
			return data, err
		}
		if time.Since(started) >= lockTimeout {
			return nil, unreadable(err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockRetryInterval):
		}
	}
}

// readImage makes one attempt at image. When it is not settled, the files were
// changing and another attempt may succeed; the error is then the last reading's,
// if it failed.
func (s *Store) readImage(ctx context.Context) ([]byte, bool, error) {
	first, firstErr := s.readPair(ctx)
	second, secondErr := s.readPair(ctx)
	if firstErr == nil && secondErr == nil && first.equal(second) {
		data, err := second.committed()
		return data, true, err
	}
	settled := firstErr != nil && secondErr != nil && !passing(firstErr) && !passing(secondErr)
	return nil, settled, secondErr
}

// refuse reports a database this tsipam must leave alone without opening it for
// real. An ordinary connection that closes last copies the log into the database,
// even one that may not write, so only a database with a log is checked. One that
// cannot be read this way is left to the ordinary connection.
func (s *Store) refuse(ctx context.Context) error {
	if s.vetted.Load() || !s.hasLog() {
		return nil
	}
	data, settled, err := s.readImage(ctx)
	if !settled || err != nil {
		return nil
	}
	db, err := openCopy(ctx, data)
	if err != nil {
		return nil
	}
	defer func() { _ = db.Close() }()
	_, err = schemaVersion(ctx, db, len(s.scripts))
	if errors.Is(err, errNewerSchema) || errors.Is(err, errForeignDatabase) {
		return err
	}
	s.vetted.Store(err == nil)
	return nil
}

func (s *Store) hasLog() bool {
	path, err := filepath.EvalSymlinks(s.path)
	if err != nil {
		return false
	}
	info, err := os.Stat(path + walSuffix)
	return err == nil && info.Size() > 0
}

// passing reports whether err can be a checkpoint or a first write in progress.
// A checkpoint copies page 1 first, so the file can briefly name more pages than
// it has.
func passing(err error) bool {
	return errors.Is(err, errInterrupted) || resultCode(err) == sqlite3.SQLITE_CORRUPT
}

// unreadable is the error for a database that never gave two equal readings,
// the last of which failed with err.
func unreadable(err error) error {
	switch {
	case err == nil:
		return errUnstable
	case passing(err):
		return errInterrupted
	default:
		return err
	}
}

type filePair struct {
	database, wal []byte
}

// committed returns the database the pair describes. A log beside an empty
// database belongs to one that is gone, and SQLite deletes it.
func (p filePair) committed() ([]byte, error) {
	if len(p.database) == 0 {
		return nil, nil
	}
	return applyWAL(p.database, p.wal)
}

func (p filePair) equal(other filePair) bool {
	return bytes.Equal(p.database, other.database) && bytes.Equal(p.wal, other.wal)
}

func (s *Store) readPair(ctx context.Context) (filePair, error) {
	// SQLite keeps the log beside the file a symbolic link leads to.
	path, err := filepath.EvalSymlinks(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return filePair{}, nil
	}
	if err != nil {
		return filePair{}, err
	}
	database, err := readDatabase(ctx, path)
	if err != nil {
		return filePair{}, err
	}
	// SQLite locks the database and -shm files but never the log, so reading
	// the log directly is safe.
	wal, err := os.ReadFile(path + walSuffix)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return filePair{}, err
	}
	journal, err := os.Stat(path + journalSuffix)
	if err == nil && journal.Size() > 0 {
		return filePair{}, errInterrupted
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return filePair{}, err
	}
	return filePair{database: database, wal: wal}, nil
}

// readDatabase reads the database file through SQLite rather than directly.
// POSIX locks belong to the process, so closing any descriptor of our own on
// the file would drop the locks another connection in this process holds.
func readDatabase(ctx context.Context, path string) ([]byte, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().IsRegular() && info.Size() == 0 {
		return nil, nil
	}
	db, err := sql.Open("sqlite", databaseURL(path, url.Values{"mode": {"ro"}, "immutable": {"1"}}))
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	// Serialize reports an unreadable file only as an empty one.
	if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(new(int)); err != nil {
		return nil, err
	}
	var data []byte
	err = conn.Raw(func(driverConn any) error {
		source, ok := driverConn.(serializer)
		if !ok {
			return errors.ErrUnsupported
		}
		var readErr error
		data, readErr = source.Serialize()
		return readErr
	})
	return data, err
}

// openImage opens a private in-memory copy of the registry database.
func (s *Store) openImage(ctx context.Context) (*sql.DB, error) {
	data, err := s.image(ctx)
	if err != nil {
		return nil, err
	}
	return openCopy(ctx, data)
}

func openCopy(ctx context.Context, data []byte) (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:?_txlock=immediate&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	// Every connection to :memory: is a database of its own.
	db.SetMaxOpenConns(1)
	if len(data) == 0 {
		return db, nil
	}
	if err = load(ctx, db, data); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

func load(ctx context.Context, db *sql.DB, data []byte) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return conn.Raw(func(driverConn any) error {
		target, ok := driverConn.(deserializer)
		if !ok {
			return errors.ErrUnsupported
		}
		return target.Deserialize(data)
	})
}

// rehearse runs an Update against a private copy of the database and discards
// it (Store rule 9). The copy is migrated like the real database would be, so
// an old one rehearses exactly.
func (s *Store) rehearse(
	ctx context.Context, tailnet string, opts registry.TxOptions, fn func(registry.Tx) error,
) error {
	if err := s.checkRehearsal(tailnet, opts.CreateIfMissing); err != nil {
		return err
	}
	db, err := s.openImage(ctx)
	if err != nil {
		return describe(err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return describe(err)
	}
	defer func() { _ = tx.Rollback() }()
	return s.apply(ctx, tx, tailnet, opts, fn)
}

// checkRehearsal reports what would keep a real Update from finding or
// creating the database file.
func (s *Store) checkRehearsal(tailnet string, create bool) error {
	_, err := os.Stat(s.path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return describe(err)
	}
	if !create {
		return &notFoundError{tailnet: tailnet}
	}
	if _, err = os.Stat(filepath.Dir(s.path)); err != nil {
		return fmt.Errorf("creating registry: %w", describe(err))
	}
	return nil
}
