package sqlitestore

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
)

// applicationID marks a database as tsipam's: "TSIP". The schema version alone
// does not, because any program may number its own.
const applicationID = 0x54534950

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrations returns the migration scripts in version order. Version N is the
// state after the first N scripts.
func migrations() ([]string, error) {
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	scripts := make([]string, len(names))
	for i, name := range names {
		script, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			return nil, readErr
		}
		scripts[i] = string(script)
	}
	return scripts, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// schemaVersion reads the database's schema version without writing. It
// refuses a version this tsipam does not know, and a database tsipam did not
// create. An empty database is version 0.
func schemaVersion(ctx context.Context, q querier, latest int) (int, error) {
	// One statement reads them all, so a concurrent first migration cannot commit
	// between them and look like a foreign database.
	var version, application, objects int
	if err := q.QueryRowContext(ctx, `SELECT user_version, application_id, (SELECT count(*) FROM sqlite_schema)
		FROM pragma_user_version, pragma_application_id`,
	).Scan(&version, &application, &objects); err != nil {
		return 0, err
	}
	switch {
	case version == 0 && application == 0 && objects == 0:
		return 0, nil
	case application != applicationID || version <= 0:
		return 0, errForeignDatabase
	case version > latest:
		return 0, errNewerSchema
	default:
		return version, nil
	}
}

// migrate brings the database to the latest version inside tx. Rereading the
// version under the write lock makes it safe when two processes both saw an
// old version.
func migrate(ctx context.Context, tx *sql.Tx, scripts []string) error {
	version, err := schemaVersion(ctx, tx, len(scripts))
	if err != nil || version == len(scripts) {
		return err
	}
	for _, script := range scripts[version:] {
		if _, err = tx.ExecContext(ctx, script); err != nil {
			return fmt.Errorf("upgrading the sqlite registry: %w", err)
		}
	}
	// PRAGMA takes no bound parameters; the values are a constant and a length.
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(
		"PRAGMA application_id = %d; PRAGMA user_version = %d", applicationID, len(scripts),
	)); err != nil {
		return fmt.Errorf("upgrading the sqlite registry: %w", err)
	}
	return nil
}
