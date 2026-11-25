package sqlitestore

import (
	"context"
	"database/sql"
)

// AddMigration appends a migration script, as a later tsipam would ship one.
func (s *Store) AddMigration(script string) {
	s.scripts = append(s.scripts, script)
}

// OpenImage opens the copy a rehearsal runs on.
func (s *Store) OpenImage(ctx context.Context) (*sql.DB, error) {
	return s.openImage(ctx)
}
