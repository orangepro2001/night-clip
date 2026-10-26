package store

import (
	"database/sql"
	"errors"

	"nightclip/core/proto"
)

// migrations holds one entry per schema version. Entry i upgrades the database
// from version i to i+1; never edit a published entry, append a new one.
var migrations = [][]string{migrationV1(), migrationV2(), migrationV3(), migrationV4(), migrationV5(), migrationV6()}

func migrationV1() []string {
	return []string{
		`CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,

		`CREATE TABLE identity (
		device_id    TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		platform     TEXT NOT NULL,
		key_blob_enc BLOB NOT NULL,
		cert_der     BLOB NOT NULL,
		created_at   INTEGER NOT NULL
	)`,

		`CREATE TABLE peers (
		device_id          TEXT PRIMARY KEY,
		name               TEXT NOT NULL,
		platform           TEXT NOT NULL,
		cert_fp            TEXT NOT NULL UNIQUE,
		tailnet_addrs_json TEXT NOT NULL DEFAULT '[]',
		port               INTEGER NOT NULL DEFAULT 47821,
		role               TEXT NOT NULL DEFAULT 'auto',
		state              TEXT NOT NULL CHECK (state IN ('paired', 'revoked')),
		apply_mode         TEXT NOT NULL DEFAULT 'notify',
		paired_at          INTEGER NOT NULL,
		last_seen_at       INTEGER NOT NULL DEFAULT 0
	)`,

		`CREATE TABLE links (
		peer_id      TEXT PRIMARY KEY REFERENCES peers(device_id) ON DELETE CASCADE,
		next_seq_out INTEGER NOT NULL DEFAULT 1,
		recv_cursor  INTEGER NOT NULL DEFAULT 0,
		updated_at   INTEGER NOT NULL
	)`,

		`CREATE TABLE outbox (
		event_id     TEXT PRIMARY KEY,
		peer_id      TEXT NOT NULL REFERENCES peers(device_id) ON DELETE CASCADE,
		sequence     INTEGER NOT NULL,
		kind         TEXT NOT NULL,
		envelope_enc BLOB NOT NULL,
		created_at   INTEGER NOT NULL,
		expires_at   INTEGER NOT NULL,
		state        TEXT NOT NULL CHECK (state IN ('queued', 'sent', 'acked', 'expired')),
		attempts     INTEGER NOT NULL DEFAULT 0,
		acked_at     INTEGER,
		ack_result   TEXT,
		UNIQUE (peer_id, sequence)
	)`,

		`CREATE TABLE inbox (
		event_id    TEXT PRIMARY KEY,
		peer_id     TEXT NOT NULL,
		sequence    INTEGER NOT NULL,
		kind        TEXT NOT NULL,
		payload_enc BLOB NOT NULL,
		received_at INTEGER NOT NULL,
		expires_at  INTEGER NOT NULL,
		state       TEXT NOT NULL CHECK (state IN ('received', 'applied', 'ignored', 'expired', 'error')),
		applied_at  INTEGER,
		UNIQUE (peer_id, sequence)
	)`,

		// No Go API yet: the file-transfer phase (WP7) fills this in.
		`CREATE TABLE transfers (
		transfer_id TEXT PRIMARY KEY,
		peer_id     TEXT NOT NULL,
		direction   TEXT NOT NULL,
		name        TEXT NOT NULL,
		size        INTEGER NOT NULL DEFAULT 0,
		sha256      TEXT NOT NULL DEFAULT '',
		path        TEXT NOT NULL DEFAULT '',
		"offset"    INTEGER NOT NULL DEFAULT 0,
		state       TEXT NOT NULL,
		error       TEXT NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL,
		expires_at  INTEGER NOT NULL
	)`,

		`CREATE TABLE clip_state (
		id                    INTEGER PRIMARY KEY CHECK (id = 1),
		last_applied_event_id TEXT NOT NULL DEFAULT '',
		last_applied_hash     TEXT NOT NULL DEFAULT '',
		last_local_sent_hash  TEXT NOT NULL DEFAULT '',
		suppress_until        INTEGER NOT NULL DEFAULT 0
	)`,

		`CREATE TABLE revoked_fps (
		cert_fp    TEXT PRIMARY KEY,
		revoked_at INTEGER NOT NULL
	)`,

		`CREATE INDEX idx_outbox_peer_state ON outbox(peer_id, state)`,
		`CREATE INDEX idx_outbox_expires ON outbox(expires_at)`,
		`CREATE INDEX idx_inbox_expires ON inbox(expires_at)`,
		`CREATE INDEX idx_inbox_received ON inbox(received_at)`,
	}
}

// migrationV2 remembers whether the sender flagged an inbox event as sensitive,
// so a history screen can hide its text without re-guessing.
func migrationV2() []string {
	return []string{
		`ALTER TABLE inbox ADD COLUMN sensitive INTEGER NOT NULL DEFAULT 0`,
	}
}

// migrationV3 gives file transfers the columns the transfer protocol needs: who
// moves the bytes (mode), what the file is (mime_type) and where the receiver
// wants it (save_dir).
func migrationV3() []string {
	return []string{
		`ALTER TABLE transfers ADD COLUMN mode TEXT NOT NULL DEFAULT 'pull'`,
		`ALTER TABLE transfers ADD COLUMN mime_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transfers ADD COLUMN save_dir TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX idx_transfers_state ON transfers(state)`,
	}
}

// migrationV4 adds the per-peer switch that keeps automatic broadcasts away from
// one device (docs/plan-auto-broadcast.md §4.1). Existing peers keep receiving.
func migrationV4() []string {
	return []string{
		`ALTER TABLE peers ADD COLUMN send_disabled INTEGER NOT NULL DEFAULT 0`,
	}
}

// migrationV5 prepares the password vault's device-to-device sync: a per-peer switch (off for every
// existing peer: the vault is shared only with devices the user names) and the bookkeeping that
// keeps two devices from pulling the same file over and over.
func migrationV5() []string {
	return []string{
		`ALTER TABLE peers ADD COLUMN vault_sync INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE vault_sync (
		peer_id    TEXT PRIMARY KEY REFERENCES peers(device_id) ON DELETE CASCADE,
		seen_hash  TEXT NOT NULL DEFAULT '',
		sent_hash  TEXT NOT NULL DEFAULT '',
		last_ok_at INTEGER NOT NULL DEFAULT 0,
		state      TEXT NOT NULL DEFAULT 'idle',
		detail     TEXT NOT NULL DEFAULT ''
	)`,
	}
}

// migrationV6 makes the vault a full replica on every already-paired device by
// default. The old per-peer switch stays as an advanced opt-out, but it must not
// hide the feature from devices whose shell has no sync screen (Windows): a
// paired device is trusted by the pairing (mTLS + SAS) and lives in the tailnet.
func migrationV6() []string {
	return []string{
		`UPDATE peers SET vault_sync = 1 WHERE state = 'paired'`,
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
