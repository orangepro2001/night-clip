package store

import (
	"database/sql"
	"errors"

	"moonpassword/core/proto"
)

// migrations holds one entry per schema version. Entry i upgrades the database
// from version i to i+1; never edit a published entry, append a new one.
//
// The schema starts again at v1 with this database file: the ClipBridge database
// (clipbridge.db, schema v1-v8: peers, inbox, transfers, notes) is never opened.
var migrations = [][]string{migrationV1()}

// migrationV1: plain settings, and secrets sealed with the database key (sign-in tokens,
// the account's Secret Key and similar values that must not sit on disk in the clear).
func migrationV1() []string {
	return []string{
		`CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,

		`CREATE TABLE secrets (
		name       TEXT PRIMARY KEY,
		value_enc  BLOB NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	}
}

// migrate brings the database up to len(migrations). It is idempotent and each
// step runs in its own transaction.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return proto.Errorf(proto.CodeDBUnavailable, "read schema version: %v", err)
	}
	var version int
	err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		version = 0
	case err != nil:
		return proto.Errorf(proto.CodeDBUnavailable, "read schema version: %v", err)
	}
	if version > len(migrations) {
		return proto.Errorf(proto.CodeDBUnavailable,
			"database schema v%d is newer than this core supports (v%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		if err := applyMigration(db, i); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(db *sql.DB, from int) error {
	tx, err := db.Begin()
	if err != nil {
		return proto.Errorf(proto.CodeDBUnavailable, "begin migration v%d: %v", from+1, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	for _, stmt := range migrations[from] {
		if _, err := tx.Exec(stmt); err != nil {
			return proto.Errorf(proto.CodeDBUnavailable, "migration v%d: %v", from+1, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM schema_version`); err != nil {
		return proto.Errorf(proto.CodeDBUnavailable, "migration v%d: %v", from+1, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, from+1); err != nil {
		return proto.Errorf(proto.CodeDBUnavailable, "migration v%d: %v", from+1, err)
	}
	if err := tx.Commit(); err != nil {
		return proto.Errorf(proto.CodeDBUnavailable, "commit migration v%d: %v", from+1, err)
	}
	return nil
}
