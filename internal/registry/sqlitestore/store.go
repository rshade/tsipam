// Package sqlitestore stores registries in one SQLite database. Every tailnet
// in the database shares one write lock.
package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/registry"
)

const (
	// lockTimeout matches the HuJSON backend's lock file wait.
	lockTimeout       = 5 * time.Second
	lockRetryInterval = 20 * time.Millisecond
	databaseMode      = 0o600
)

// Store is the SQLite registry backend for one database file.
//
// The write lock ends when SQLite commits, so across processes the
// after-success hook runs just after serialization ends. Within one Store it
// still runs before the next Update starts.
type Store struct {
	path    string
	logger  diagnostics.Logger
	scripts []string
	writer  *sql.DB
	reader  *sql.DB

	// vetted is true once refuse has found nothing to refuse.
	vetted atomic.Bool

	mu         sync.Mutex
	failCommit bool
}

var (
	_ registry.Store          = (*Store)(nil)
	_ registry.HistoryReader  = (*Store)(nil)
	_ registry.IdentitySource = (*Store)(nil)
)

// Open returns a store for the database at path. It does not touch the
// filesystem, so a missing database is reported by the first transaction.
func Open(path string, logger diagnostics.Logger) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving the sqlite database path: %w", describe(err))
	}
	scripts, err := migrations()
	if err != nil {
		return nil, err
	}
	writer, err := sql.Open("sqlite", dataSource(absolute, "immediate",
		"journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(ON)"))
	if err != nil {
		return nil, err
	}
	// The reader leaves journal_mode alone: setting it writes to the file.
	reader, err := sql.Open("sqlite", dataSource(absolute, "deferred", "query_only(1)"))
	if err != nil {
		return nil, errors.Join(err, writer.Close())
	}
	writer.SetMaxOpenConns(1)
	return &Store{path: absolute, logger: logger, scripts: scripts, writer: writer, reader: reader}, nil
}

// dataSource builds a driver DSN. mode=rw keeps SQLite from creating the file,
// which would use umask permissions. The path is percent-encoded so nothing in
// it can be read as a parameter.
func dataSource(path, lock string, pragmas ...string) string {
	query := url.Values{"mode": {"rw"}, "_txlock": {lock}}
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", lockTimeout.Milliseconds()))
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	return databaseURL(path, query)
}

func databaseURL(path string, query url.Values) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	location := url.URL{Path: path}
	return "file://" + location.EscapedPath() + "?" + query.Encode()
}

// Close releases the database handles. Closing the last one folds the
// write-ahead log back into the database file.
func (s *Store) Close() error {
	return errors.Join(s.reader.Close(), s.writer.Close())
}

// FailNextCommit makes the next Update whose function returns nil fail the
// commit and leave the database unchanged.
func (s *Store) FailNextCommit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCommit = true
}

func (s *Store) takeFail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := s.failCommit
	s.failCommit = false
	return fail
}

// View reads one tailnet from a consistent snapshot without the write lock.
func (s *Store) View(
	ctx context.Context, tailnet string, opts registry.TxOptions, fn func(registry.Snapshot) error,
) error {
	return s.read(ctx, tailnet, func(tx *sql.Tx, tailnetID int64) error {
		snap := &snapshot{ctx: ctx, tx: tx, tailnetID: tailnetID}
		if err := snap.require(opts.Require); err != nil {
			return err
		}
		return fn(snap)
	})
}

// read runs fn in a read transaction on an up-to-date schema.
func (s *Store) read(ctx context.Context, tailnet string, fn func(*sql.Tx, int64) error) error {
	if err := s.prepare(ctx, tailnet); err != nil {
		return err
	}
	tx, err := s.reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return describe(err)
	}
	defer func() { _ = tx.Rollback() }()
	tailnetID, err := lookupTailnet(ctx, tx, tailnet)
	if err != nil {
		return err
	}
	return fn(tx, tailnetID)
}

// prepare reports an absent or unusable database, and upgrades an old one. It
// never creates anything.
func (s *Store) prepare(ctx context.Context, tailnet string) error {
	if _, err := os.Stat(s.path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &notFoundError{tailnet: tailnet}
		}
		return describe(err)
	}
	if err := s.refuse(ctx); err != nil {
		return err
	}
	version, err := schemaVersion(ctx, s.reader, len(s.scripts))
	if err != nil {
		return describe(err)
	}
	if version == 0 {
		return &notFoundError{tailnet: tailnet}
	}
	if version == len(s.scripts) {
		return nil
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		return describe(migrate(ctx, tx, s.scripts))
	})
}

// write runs fn under the write lock and commits when it returns nil.
func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	conn, tx, err := s.beginWrite(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	defer func() { _ = tx.Rollback() }()
	if err = fn(tx); err != nil {
		return err
	}
	return describe(tx.Commit())
}

// Update holds the database write lock while fn decides and mutates. With
// DryRun it neither locks nor touches any file.
func (s *Store) Update(
	ctx context.Context, tailnet string, opts registry.TxOptions, fn func(registry.Tx) error,
) error {
	if opts.DryRun {
		return s.rehearse(ctx, tailnet, opts, fn)
	}
	if err := s.prepareUpdate(ctx, tailnet, opts.CreateIfMissing); err != nil {
		return err
	}
	conn, dbTx, err := s.beginWrite(ctx)
	if err != nil {
		return err
	}
	// Holding the connection until the hook has run keeps the next Update on
	// this Store from starting before it.
	defer func() { _ = conn.Close() }()
	defer func() { _ = dbTx.Rollback() }()

	if err = s.apply(ctx, dbTx, tailnet, opts, fn); err != nil {
		return err
	}
	if s.takeFail() {
		return &registry.CommitError{Err: errors.New("commit failed")}
	}
	if err = dbTx.Commit(); err != nil {
		return &registry.CommitError{Err: describe(err)}
	}
	registry.RunAfterSuccess(ctx)
	return nil
}

// apply migrates the database, opens the tailnet's registry, and runs fn on it,
// all inside dbTx.
func (s *Store) apply(
	ctx context.Context, dbTx *sql.Tx, tailnet string, opts registry.TxOptions, fn func(registry.Tx) error,
) error {
	if err := migrate(ctx, dbTx, s.scripts); err != nil {
		return describe(err)
	}
	tailnetID, created, err := openTailnet(ctx, dbTx, tailnet, opts)
	if err != nil {
		return err
	}
	tx := &writeTx{
		ctx: ctx, tx: dbTx, tailnetID: tailnetID,
		opts: opts, actor: registry.ActorFrom(ctx),
	}
	s.logRegistry(created && !opts.DryRun, tx.Counts())
	return s.mutate(ctx, dbTx, created, func() error {
		if requireErr := tx.require(opts.Require); requireErr != nil {
			return requireErr
		}
		return fn(tx)
	})
}

// mutate runs fn, undoing only its own changes when it fails. A registry this
// Update just created survives that failure, as it does on the file backend.
func (s *Store) mutate(ctx context.Context, tx *sql.Tx, created bool, fn func() error) error {
	if !created {
		return fn()
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT mutation"); err != nil {
		return describe(err)
	}
	fnErr := fn()
	if fnErr == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO mutation"); err != nil {
		return fnErr
	}
	// The registry is empty either way, so a failed commit here loses nothing
	// the caller was told it has.
	_ = tx.Commit()
	return fnErr
}

// prepareUpdate is prepare for a transaction that may create the registry.
func (s *Store) prepareUpdate(ctx context.Context, tailnet string, create bool) error {
	if !create {
		return s.prepare(ctx, tailnet)
	}
	if err := s.createFile(); err != nil {
		return err
	}
	if err := s.refuse(ctx); err != nil {
		return err
	}
	// Checked before the writer opens, because its journal_mode would change a
	// database this tsipam must leave alone.
	if _, err := schemaVersion(ctx, s.reader, len(s.scripts)); err != nil {
		return describe(err)
	}
	return nil
}

func (s *Store) createFile() error {
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, databaseMode)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("creating registry: %w", describe(err))
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("creating registry: %w", describe(err))
	}
	return nil
}

// beginWrite takes the write lock, waiting at most lockTimeout for it. The
// retry covers a database not yet in WAL mode: switching it reports busy at once
// while another connection holds the write lock, ignoring busy_timeout.
func (s *Store) beginWrite(ctx context.Context) (*sql.Conn, *sql.Tx, error) {
	diagnostics.Debug(s.logger, "acquiring registry lock")
	started := time.Now()
	for {
		conn, tx, err := s.tryBeginWrite(ctx, lockTimeout-time.Since(started))
		if err == nil {
			diagnostics.Debug(s.logger, "registry lock acquired",
				"waited_ms", time.Since(started).Milliseconds())
			return conn, tx, nil
		}
		if !isBusy(err) || time.Since(started) >= lockTimeout {
			return nil, nil, describe(err)
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(lockRetryInterval):
		}
	}
}

func (s *Store) tryBeginWrite(ctx context.Context, wait time.Duration) (*sql.Conn, *sql.Tx, error) {
	connCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	conn, err := s.writer.Conn(connCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, nil, errLockTimeout
		}
		return nil, nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, tx, nil
}

func lookupTailnet(ctx context.Context, q querier, tailnet string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, "SELECT id FROM tailnets WHERE key = ?", tailnet).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, &notFoundError{tailnet: tailnet}
	}
	if err != nil {
		return 0, describe(err)
	}
	return id, nil
}

func openTailnet(
	ctx context.Context, tx *sql.Tx, tailnet string, opts registry.TxOptions,
) (int64, bool, error) {
	id, err := lookupTailnet(ctx, tx, tailnet)
	if err == nil || !opts.CreateIfMissing || !errors.Is(err, registry.ErrNotFound) {
		return id, false, err
	}
	err = tx.QueryRowContext(ctx,
		"INSERT INTO tailnets (key, created_at) VALUES (?, ?) RETURNING id", tailnet, opts.EventTime(),
	).Scan(&id)
	if err != nil {
		return 0, false, fmt.Errorf("creating registry: %w", describe(err))
	}
	return id, true, nil
}

func (s *Store) logRegistry(created bool, counts registry.Counts) {
	message := "registry loaded"
	if created {
		message = "registry created"
	}
	diagnostics.Debug(s.logger, message,
		"site_id_count", counts.SiteIDs,
		"subnet_pool_count", counts.SubnetPools,
		"service_address_count", counts.ServiceAddresses,
		"udp_port_count", counts.UDPPorts,
	)
}
